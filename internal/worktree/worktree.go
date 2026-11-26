// Package worktree reads and writes managed files in a project checkout and
// keeps moreco's per-worktree sync state.
package worktree

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bruno-brant/moreco/internal/gitx"
)

// Worktree is a checkout of a project.
type Worktree struct {
	Root string
}

// skipDirs are never searched for managed files: they are large and never
// hold editor configuration worth syncing.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, ".venv": true, ".direnv": true, ".terraform": true,
}

// Find returns the slash-separated paths, relative to the root, of files for
// which match returns true. It does not descend into skipDirs, symlinked
// directories, or nested git repositories and worktrees (directories with
// their own .git), which are separate checkouts.
func (w *Worktree) Find(match func(rel string) bool) ([]string, error) {
	var found []string
	err := filepath.WalkDir(w.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrPermission) {
				return nil
			}
			return err
		}
		if p == w.Root {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			if _, err := os.Lstat(filepath.Join(p, ".git")); err == nil {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(w.Root, p)
		if err != nil {
			return err
		}
		if rel = filepath.ToSlash(rel); match(rel) {
			found = append(found, rel)
		}
		return nil
	})
	sort.Strings(found)
	return found, err
}

// Tracked returns the subset of rels that the project's own git tracks.
// moreco leaves those alone: they are already shared through the project.
func (w *Worktree) Tracked(rels []string) (map[string]bool, error) {
	tracked := map[string]bool{}
	if len(rels) == 0 {
		return tracked, nil
	}
	args := append([]string{"ls-files", "-z", "--cached", "--"}, rels...)
	out, err := gitx.RunRaw(w.Root, nil, args...)
	if err != nil {
		return nil, err
	}
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			tracked[p] = true
		}
	}
	return tracked, nil
}

// Read returns the content of a file, or ok=false if it does not exist.
func (w *Worktree) Read(rel string) (data []byte, ok bool, err error) {
	data, err = os.ReadFile(w.path(rel))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	return data, err == nil, err
}

// Write writes a file, creating directories as needed.
func (w *Worktree) Write(rel string, data []byte) error {
	p := w.path(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

// Remove deletes a file and any directories it leaves empty (up to the root).
func (w *Worktree) Remove(rel string) error {
	p := w.path(rel)
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for dir := filepath.Dir(p); dir != w.Root && strings.HasPrefix(dir, w.Root); dir = filepath.Dir(dir) {
		if os.Remove(dir) != nil {
			break // not empty
		}
	}
	return nil
}

func (w *Worktree) path(rel string) string { return filepath.Join(w.Root, filepath.FromSlash(rel)) }

// Roots returns the root directories of all worktrees of the repository,
// including this one.
func (w *Worktree) Roots() ([]string, error) {
	out, err := gitx.Run(w.Root, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var roots []string
	for _, line := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			roots = append(roots, filepath.Clean(p))
		}
	}
	return roots, nil
}

// Branch returns the checked-out branch, or "detached".
func (w *Worktree) Branch() string {
	b, err := gitx.Run(w.Root, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil || b == "" {
		return "detached"
	}
	return b
}

// HasConflictMarkers reports whether data contains git-style conflict
// markers at the start of a line.
func HasConflictMarkers(data []byte) bool {
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "<<<<<<< ") || strings.HasPrefix(line, ">>>>>>> ") {
			return true
		}
	}
	return false
}

// State is moreco's record of a worktree's last sync. It lives inside the
// worktree's private git directory, so every worktree has its own.
type State struct {
	Version int `json:"version"`
	// Folder is the store folder the worktree was synced with.
	Folder string `json:"folder"`
	// StoreCommit is the store commit of the last pull or push. The store
	// content at that commit is the merge base for the next pull.
	StoreCommit string `json:"storeCommit"`
	// Conflicts are files left with unresolved conflicts by a pull.
	Conflicts []string `json:"conflicts,omitempty"`
}

const stateFile = "moreco-state.json"

func (w *Worktree) statePath() (string, error) {
	p, err := gitx.Run(w.Root, "rev-parse", "--git-path", stateFile)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(w.Root, p)
	}
	return p, nil
}

// LoadState returns the worktree's state, or an empty one if it has never
// been synced.
func (w *Worktree) LoadState() (*State, error) {
	p, err := w.statePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return &State{Version: 1}, nil
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	return &s, nil
}

// SaveState writes the worktree's state.
func (w *Worktree) SaveState(s *State) error {
	p, err := w.statePath()
	if err != nil {
		return err
	}
	s.Version = 1
	sort.Strings(s.Conflicts)
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
