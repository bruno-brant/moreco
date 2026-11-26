// Package store manages moreco's local clone of the store repository: the
// private git repo that holds every project's configuration files.
//
// The local clone is a cache. Every operation starts by syncing it to the
// remote (Sync), and a push that loses a race is rolled back, so the clone
// never carries state that is not on the remote.
package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bruno-brant/moreco/internal/gitx"
	"github.com/bruno-brant/moreco/internal/manifest"
)

// ErrRemoteAhead means the remote store has commits this operation did not
// start from, so the push was refused (like a non-fast-forward in git).
var ErrRemoteAhead = errors.New("the store has changed since it was last synced")

// Store is the local clone of the store repository.
type Store struct {
	Dir    string
	Branch string
}

// DefaultDir is where the store is cloned: $MORECO_STORE if set, otherwise
// <user cache dir>/moreco/store.
func DefaultDir() (string, error) {
	if d := os.Getenv("MORECO_STORE"); d != "" {
		return filepath.Abs(d)
	}
	c, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(c, "moreco", "store"), nil
}

// Init clones remote into dir and makes sure it is a moreco store, creating
// and pushing an empty manifest if the remote has none. Running it again for
// the same remote is a no-op.
func Init(remote, dir string) (*Store, error) {
	if _, err := os.Stat(dir); err == nil {
		s, err := Open(dir)
		if err != nil {
			return nil, fmt.Errorf("%s already exists and is not a moreco store", dir)
		}
		existing, err := s.RemoteURL()
		if err != nil {
			return nil, err
		}
		if existing != remote {
			return nil, fmt.Errorf("a store is already set up at %s for %s; remove it or set MORECO_STORE to use another location", dir, existing)
		}
		return s, s.ensureManifest()
	}

	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return nil, err
	}
	if _, err := gitx.Run("", "clone", "--quiet", remote, dir); err != nil {
		return nil, err
	}
	s, err := Open(dir)
	if err != nil {
		return nil, err
	}
	return s, s.ensureManifest()
}

func (s *Store) ensureManifest() error {
	unlock, err := s.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	if err := s.Sync(); err != nil {
		return err
	}
	if _, err := os.Stat(s.manifestPath()); err == nil {
		_, err := s.Manifest() // validate
		return err
	}
	if err := manifest.New().Save(s.manifestPath()); err != nil {
		return err
	}
	_, err = s.CommitAndPush("Initialize moreco store")
	return err
}

// Open opens an existing local store clone.
func Open(dir string) (*Store, error) {
	if fi, err := os.Stat(filepath.Join(dir, ".git")); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("no moreco store at %s (run `moreco init <remote>`)", dir)
	}
	branch, err := gitx.Run(dir, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("store at %s is not on a branch: %w", dir, err)
	}
	return &Store{Dir: dir, Branch: branch}, nil
}

// RemoteURL returns the URL of the store's origin remote.
func (s *Store) RemoteURL() (string, error) {
	return gitx.Run(s.Dir, "remote", "get-url", "origin")
}

func (s *Store) remoteRef() string { return "refs/remotes/origin/" + s.Branch }

func (s *Store) remoteHasBranch() (bool, error) {
	_, err := gitx.Run(s.Dir, "rev-parse", "--verify", "--quiet", s.remoteRef())
	if err == nil {
		return true, nil
	}
	if gitx.ExitCode(err) == 1 {
		return false, nil
	}
	return false, err
}

// Sync fetches the remote and makes the local clone match it exactly,
// discarding anything local.
func (s *Store) Sync() error {
	if _, err := gitx.Run(s.Dir, "fetch", "--quiet", "--prune", "origin"); err != nil {
		return err
	}
	ok, err := s.remoteHasBranch()
	if err != nil || !ok {
		return err // remote is still empty
	}
	if _, err := gitx.Run(s.Dir, "reset", "--quiet", "--hard", s.remoteRef()); err != nil {
		return err
	}
	_, err = gitx.Run(s.Dir, "clean", "--quiet", "-fd")
	return err
}

// Head returns the current store commit, or "" if the store has no commits.
func (s *Store) Head() (string, error) {
	out, err := gitx.Run(s.Dir, "rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil && gitx.ExitCode(err) == 1 {
		return "", nil
	}
	return out, err
}

func (s *Store) manifestPath() string { return filepath.Join(s.Dir, manifest.FileName) }

// Manifest loads moreco.yaml from the store.
func (s *Store) Manifest() (*manifest.Manifest, error) {
	m, err := manifest.Load(s.manifestPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s has no %s; is it a moreco store?", s.Dir, manifest.FileName)
	}
	return m, err
}

// SaveManifest writes moreco.yaml (not committed until CommitAndPush).
func (s *Store) SaveManifest(m *manifest.Manifest) error {
	if err := m.Validate(); err != nil {
		return err
	}
	return m.Save(s.manifestPath())
}

// ReadFolder returns every file in a project folder, keyed by slash-separated
// path relative to the folder.
func (s *Store) ReadFolder(folder string) (map[string][]byte, error) {
	if err := manifest.ValidateFolder(folder); err != nil {
		return nil, err
	}
	root := filepath.Join(s.Dir, folder)
	files := map[string][]byte{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && p == root {
				return fs.SkipAll
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s: not a regular file", p)
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = data
		return nil
	})
	return files, err
}

// WriteFolder makes a project folder contain exactly the given files (keyed
// by slash-separated relative path), deleting any others. Changes are not
// committed until CommitAndPush.
func (s *Store) WriteFolder(folder string, files map[string][]byte) error {
	if err := manifest.ValidateFolder(folder); err != nil {
		return err
	}
	for rel := range files {
		if err := ValidateRelPath(rel); err != nil {
			return err
		}
	}
	existing, err := s.ReadFolder(folder)
	if err != nil {
		return err
	}
	root := filepath.Join(s.Dir, folder)
	for rel := range existing {
		if _, keep := files[rel]; !keep {
			if err := os.Remove(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
				return err
			}
		}
	}
	rels := make([]string, 0, len(files))
	for rel := range files {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, files[rel], 0o644); err != nil {
			return err
		}
	}
	return removeEmptyDirs(root)
}

// ValidateRelPath checks that rel is a clean, relative, slash-separated path
// that stays inside its folder and avoids .git.
func ValidateRelPath(rel string) error {
	if rel == "" || strings.Contains(rel, `\`) || path.IsAbs(rel) || path.Clean(rel) != rel ||
		rel == ".." || strings.HasPrefix(rel, "../") {
		return fmt.Errorf("invalid path %q", rel)
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".git" {
			return fmt.Errorf("invalid path %q: inside .git", rel)
		}
	}
	return nil
}

func removeEmptyDirs(root string) error {
	var dirs []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return fs.SkipAll
			}
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, p)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- { // deepest first
		if entries, err := os.ReadDir(dirs[i]); err == nil && len(entries) == 0 {
			_ = os.Remove(dirs[i])
		}
	}
	return nil
}

// CommitAndPush commits every change in the store and pushes it. It returns
// the new commit, or "" if there was nothing to commit. If the remote moved
// on in the meantime, the local commit is discarded, the clone is synced to
// the remote, and ErrRemoteAhead is returned.
func (s *Store) CommitAndPush(message string) (string, error) {
	if _, err := gitx.Run(s.Dir, "add", "--all"); err != nil {
		return "", err
	}
	if _, err := gitx.Run(s.Dir, "diff", "--cached", "--quiet"); err == nil {
		return "", nil
	} else if gitx.ExitCode(err) != 1 {
		return "", err
	}
	if _, err := gitx.Run(s.Dir, "commit", "--quiet", "-m", message); err != nil {
		return "", err
	}
	if _, pushErr := gitx.Run(s.Dir, "push", "--quiet", "origin", "HEAD:refs/heads/"+s.Branch); pushErr != nil {
		ahead, err := s.remoteMovedOn()
		if err != nil {
			return "", fmt.Errorf("%v (and checking the remote failed: %v)", pushErr, err)
		}
		if ahead {
			if err := s.Sync(); err != nil {
				return "", err
			}
			return "", ErrRemoteAhead
		}
		return "", pushErr
	}
	return s.Head()
}

// remoteMovedOn fetches and reports whether the remote branch has commits
// that HEAD does not contain.
func (s *Store) remoteMovedOn() (bool, error) {
	if _, err := gitx.Run(s.Dir, "fetch", "--quiet", "origin"); err != nil {
		return false, err
	}
	ok, err := s.remoteHasBranch()
	if err != nil || !ok {
		return false, err
	}
	_, err = gitx.Run(s.Dir, "merge-base", "--is-ancestor", s.remoteRef(), "HEAD")
	if err == nil {
		return false, nil
	}
	if gitx.ExitCode(err) == 1 {
		return true, nil
	}
	return false, err
}
