// Package ops implements moreco's user-facing operations on a worktree:
// pull, push, status and resolve.
package ops

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bruno-brant/moreco/internal/manifest"
	"github.com/bruno-brant/moreco/internal/pathtok"
	"github.com/bruno-brant/moreco/internal/repoid"
	"github.com/bruno-brant/moreco/internal/store"
	"github.com/bruno-brant/moreco/internal/textmerge"
	"github.com/bruno-brant/moreco/internal/worktree"
)

var (
	// ErrConflicts: a previous pull left conflicts that are not resolved.
	ErrConflicts = errors.New("there are unresolved conflicts; fix the files and run `moreco resolve`")
	// ErrBehind: the store has changes for this project the worktree lacks.
	ErrBehind = errors.New("the store has changes for this project that this worktree has not pulled; run `moreco pull` first")
)

// Kind is what happened to a file.
type Kind string

const (
	Created  Kind = "created"  // pull: new file from the store
	Updated  Kind = "updated"  // pull: replaced with the store version
	Deleted  Kind = "deleted"  // pull: removed (gone from the store); push: removed from the store
	Merged   Kind = "merged"   // pull: store changes merged into local edits
	Conflict Kind = "conflict" // pull: needs manual resolution
	Skipped  Kind = "skipped"  // tracked by the project's own git; left alone
	Added    Kind = "added"    // push: new in the store
	Changed  Kind = "changed"  // push: changed in the store
)

// Action is one file-level outcome.
type Action struct {
	Path string
	Kind Kind
	Note string
}

// Report is the result of an operation.
type Report struct {
	Folder      string
	StoreCommit string
	Actions     []Action
}

// Session is an operation on one worktree, holding the store lock.
type Session struct {
	store  *store.Store
	wt     *worktree.Worktree
	repo   *manifest.Repo
	codec  *pathtok.Codec
	state  *worktree.State
	unlock func()
}

// Options control Open.
type Options struct {
	// Offline skips syncing the store with its remote.
	Offline bool
}

// Open prepares an operation on the worktree containing dir.
func Open(dir string, s *store.Store, opt Options) (*Session, error) {
	id, err := repoid.Identify(dir)
	if err != nil {
		return nil, err
	}
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			unlock()
		}
	}()
	if !opt.Offline {
		if err := s.Sync(); err != nil {
			return nil, fmt.Errorf("syncing store: %w", err)
		}
	}
	m, err := s.Manifest()
	if err != nil {
		return nil, err
	}
	repo, _, err := m.Find(id)
	if err != nil {
		return nil, err
	}
	wt := &worktree.Worktree{Root: id.Root}
	roots, err := wt.Roots()
	if err != nil {
		return nil, err
	}
	state, err := wt.LoadState()
	if err != nil {
		return nil, err
	}
	if state.Folder != repo.Folder {
		state = &worktree.State{Folder: repo.Folder} // never synced with this folder
	}
	ok = true
	return &Session{store: s, wt: wt, repo: repo, codec: pathtok.New(id.Root, roots...), state: state, unlock: unlock}, nil
}

// Close releases the store lock. It is safe to call more than once.
func (x *Session) Close() {
	if x.unlock != nil {
		x.unlock()
		x.unlock = nil
	}
}

// Folder is the store folder of the session's project.
func (x *Session) Folder() string { return x.repo.Folder }

func same(a []byte, aok bool, b []byte, bok bool) bool {
	return aok == bok && (!aok || bytes.Equal(a, b))
}

// baseFiles returns the store content as of the last sync.
func (x *Session) baseFiles() (map[string][]byte, error) {
	if x.state.StoreCommit == "" {
		return map[string][]byte{}, nil
	}
	if !x.store.HasCommit(x.state.StoreCommit) {
		return nil, fmt.Errorf("the store commit this worktree last synced with (%s) is gone from the store's history", x.state.StoreCommit)
	}
	return x.store.ReadFolderAt(x.state.StoreCommit, x.repo.Folder)
}

// readLocal returns a worktree file in store form (path tokens encoded).
func (x *Session) readLocal(rel string) ([]byte, bool, error) {
	raw, ok, err := x.wt.Read(rel)
	if !ok || err != nil {
		return nil, ok, err
	}
	return x.codec.Encode(raw), true, nil
}

// Pull brings the worktree up to date with the store, merging with local edits.
func (x *Session) Pull() (*Report, error) {
	if len(x.state.Conflicts) > 0 {
		return nil, ErrConflicts
	}
	head, err := x.store.Head()
	if err != nil {
		return nil, err
	}
	base, err := x.baseFiles()
	if err != nil {
		return nil, err
	}
	theirs, err := x.store.ReadFolder(x.repo.Folder)
	if err != nil {
		return nil, err
	}
	paths := union(base, theirs)
	tracked, err := x.wt.Tracked(paths)
	if err != nil {
		return nil, err
	}

	rep := &Report{Folder: x.repo.Folder, StoreCommit: head}
	var conflicts []string
	for _, p := range paths {
		if err := store.ValidateRelPath(p); err != nil {
			return nil, err
		}
		b, bok := base[p]
		t, tok := theirs[p]
		if tracked[p] {
			if tok {
				rep.add(p, Skipped, "tracked by the project's git")
			}
			continue
		}
		o, ook, err := x.readLocal(p)
		if err != nil {
			return nil, err
		}

		switch {
		case same(o, ook, t, tok):
			// Already identical.
		case same(b, bok, o, ook):
			// No local edits: take the store version.
			if !tok {
				if err := x.wt.Remove(p); err != nil {
					return nil, err
				}
				rep.add(p, Deleted, "")
			} else {
				kind := Updated
				if !ook {
					kind = Created
				}
				if err := x.wt.Write(p, x.codec.Decode(t)); err != nil {
					return nil, err
				}
				rep.add(p, kind, "")
			}
		case same(b, bok, t, tok):
			// Only local edits: keep them.
		case ook && tok:
			var bb []byte
			if bok {
				bb = b
			}
			res, conflict, err := textmerge.Merge(bb, o, t, textmerge.Labels{Ours: "local", Base: "last sync", Theirs: "store"})
			if err != nil {
				conflicts = append(conflicts, p)
				rep.add(p, Conflict, fmt.Sprintf("%v; local version kept", err))
				continue
			}
			if err := x.wt.Write(p, x.codec.Decode(res)); err != nil {
				return nil, err
			}
			if conflict {
				conflicts = append(conflicts, p)
				rep.add(p, Conflict, "conflict markers written")
			} else {
				rep.add(p, Merged, "")
			}
		case tok: // deleted locally, changed in the store
			if err := x.wt.Write(p, x.codec.Decode(t)); err != nil {
				return nil, err
			}
			conflicts = append(conflicts, p)
			rep.add(p, Conflict, "deleted locally but changed in the store; store version restored")
		default: // changed locally, deleted in the store
			conflicts = append(conflicts, p)
			rep.add(p, Conflict, "changed locally but deleted in the store; local version kept")
		}
	}

	x.state.StoreCommit = head
	x.state.Conflicts = conflicts
	return rep, x.wt.SaveState(x.state)
}

// localFiles returns the worktree's managed files in store form.
func (x *Session) localFiles() (files map[string][]byte, skipped []string, err error) {
	rels, err := x.wt.Find(x.repo.Matches)
	if err != nil {
		return nil, nil, err
	}
	tracked, err := x.wt.Tracked(rels)
	if err != nil {
		return nil, nil, err
	}
	files = map[string][]byte{}
	for _, rel := range rels {
		if tracked[rel] {
			skipped = append(skipped, rel)
			continue
		}
		data, ok, err := x.readLocal(rel)
		if err != nil {
			return nil, nil, err
		}
		if ok {
			files[rel] = data
		}
	}
	return files, skipped, nil
}

// upToDate reports whether the store's folder is unchanged since the last
// sync (changes to other projects in the store do not matter).
func (x *Session) upToDate(head string) (bool, error) {
	headTree, err := x.store.FolderTree(head, x.repo.Folder)
	if err != nil {
		return false, err
	}
	if x.state.StoreCommit == "" {
		return headTree == "", nil
	}
	if !x.store.HasCommit(x.state.StoreCommit) {
		return false, nil
	}
	syncedTree, err := x.store.FolderTree(x.state.StoreCommit, x.repo.Folder)
	return syncedTree == headTree, err
}

// Push makes the store's folder match the worktree's managed files.
func (x *Session) Push(message string) (*Report, error) {
	if len(x.state.Conflicts) > 0 {
		return nil, ErrConflicts
	}
	head, err := x.store.Head()
	if err != nil {
		return nil, err
	}
	ok, err := x.upToDate(head)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrBehind
	}

	files, skipped, err := x.localFiles()
	if err != nil {
		return nil, err
	}
	for rel, data := range files {
		if worktree.HasConflictMarkers(data) {
			return nil, fmt.Errorf("%s contains conflict markers; fix it before pushing", rel)
		}
	}
	current, err := x.store.ReadFolder(x.repo.Folder)
	if err != nil {
		return nil, err
	}

	rep := &Report{Folder: x.repo.Folder}
	for _, p := range skipped {
		rep.add(p, Skipped, "tracked by the project's git")
	}
	for _, p := range union(files, current) {
		n, nok := files[p]
		c, cok := current[p]
		switch {
		case same(n, nok, c, cok):
		case !cok:
			rep.add(p, Added, "")
		case !nok:
			rep.add(p, Deleted, "")
		default:
			rep.add(p, Changed, "")
		}
	}

	if err := x.store.WriteFolder(x.repo.Folder, files); err != nil {
		return nil, err
	}
	commit, err := x.store.CommitAndPush(message)
	if errors.Is(err, store.ErrRemoteAhead) {
		return nil, ErrBehind
	}
	if err != nil {
		return nil, err
	}
	if commit == "" {
		commit = head
	}
	rep.StoreCommit = commit
	x.state.StoreCommit = commit
	return rep, x.wt.SaveState(x.state)
}

// PushMessage is the default store commit message for a push.
func (x *Session) PushMessage() string {
	host, _ := os.Hostname()
	if host == "" {
		host = "unknown host"
	}
	return fmt.Sprintf("Push %s from %s (%s)", x.repo.Folder, x.wt.Branch(), host)
}

// FileStatus compares a file with the last sync on each side.
type FileStatus struct {
	Path     string
	Local    Change // worktree vs last sync
	Store    Change // store vs last sync
	Conflict bool
	Tracked  bool
}

// Change is a file's change relative to the last sync.
type Change byte

const (
	Same     Change = ' '
	Modified Change = 'M'
	New      Change = 'A'
	Removed  Change = 'D'
)

// Status is the result of Session.Status.
type Status struct {
	Folder      string
	StoreCommit string // last sync, "" if never
	Behind      bool   // the store has changes to pull
	Files       []FileStatus
}

// Status compares the worktree, the last sync and the store.
func (x *Session) Status() (*Status, error) {
	head, err := x.store.Head()
	if err != nil {
		return nil, err
	}
	upToDate, err := x.upToDate(head)
	if err != nil {
		return nil, err
	}
	base, err := x.baseFiles()
	if err != nil {
		return nil, err
	}
	theirs, err := x.store.ReadFolder(x.repo.Folder)
	if err != nil {
		return nil, err
	}
	local, skipped, err := x.localFiles()
	if err != nil {
		return nil, err
	}
	// Files that were synced but no longer match the globs still count.
	for p := range base {
		if _, ok := local[p]; !ok {
			if data, ok, err := x.readLocal(p); err != nil {
				return nil, err
			} else if ok {
				local[p] = data
			}
		}
	}
	conflicted := map[string]bool{}
	for _, p := range x.state.Conflicts {
		conflicted[p] = true
	}
	st := &Status{Folder: x.repo.Folder, StoreCommit: x.state.StoreCommit, Behind: !upToDate}
	for _, p := range skipped {
		st.Files = append(st.Files, FileStatus{Path: p, Tracked: true})
	}
	for _, p := range union(base, theirs, local) {
		b, bok := base[p]
		l, lok := local[p]
		t, tok := theirs[p]
		fs := FileStatus{Path: p, Local: change(b, bok, l, lok), Store: change(b, bok, t, tok), Conflict: conflicted[p]}
		if fs.Local != Same || fs.Store != Same || fs.Conflict {
			st.Files = append(st.Files, fs)
		}
	}
	sort.Slice(st.Files, func(i, j int) bool { return st.Files[i].Path < st.Files[j].Path })
	return st, nil
}

func change(base []byte, bok bool, now []byte, nok bool) Change {
	switch {
	case same(base, bok, now, nok):
		return Same
	case !bok:
		return New
	case !nok:
		return Removed
	}
	return Modified
}

// Resolve marks conflicted files as resolved. paths are relative to cwd (or
// absolute); none means all conflicted files.
func (x *Session) Resolve(cwd string, paths []string) ([]string, error) {
	if len(x.state.Conflicts) == 0 {
		return nil, errors.New("there are no conflicts to resolve")
	}
	want := map[string]bool{}
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			p = filepath.Join(cwd, p)
		}
		rel, err := filepath.Rel(x.wt.Root, p)
		if err != nil || strings.HasPrefix(rel, "..") {
			return nil, fmt.Errorf("%s is outside the worktree", p)
		}
		want[filepath.ToSlash(rel)] = true
	}
	var resolved, remaining []string
	for _, c := range x.state.Conflicts {
		if len(want) > 0 && !want[c] {
			remaining = append(remaining, c)
			continue
		}
		data, ok, err := x.wt.Read(c)
		if err != nil {
			return nil, err
		}
		if ok && worktree.HasConflictMarkers(data) {
			return nil, fmt.Errorf("%s still contains conflict markers", c)
		}
		resolved = append(resolved, c)
		delete(want, c)
	}
	for p := range want {
		return nil, fmt.Errorf("%s is not in conflict", p)
	}
	x.state.Conflicts = remaining
	return resolved, x.wt.SaveState(x.state)
}

func (r *Report) add(path string, kind Kind, note string) {
	r.Actions = append(r.Actions, Action{Path: path, Kind: kind, Note: note})
}

// Conflicts returns the paths left in conflict.
func (r *Report) Conflicts() []string {
	var c []string
	for _, a := range r.Actions {
		if a.Kind == Conflict {
			c = append(c, a.Path)
		}
	}
	return c
}

func union(maps ...map[string][]byte) []string {
	seen := map[string]bool{}
	var keys []string
	for _, m := range maps {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	sort.Strings(keys)
	return keys
}
