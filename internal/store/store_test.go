package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bruno-brant/moreco/internal/manifest"
	"github.com/bruno-brant/moreco/internal/repoid"
	"github.com/bruno-brant/moreco/internal/testgit"
)

func newStore(t *testing.T, remote string) *Store {
	t.Helper()
	s, err := Init(remote, filepath.Join(t.TempDir(), "store"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestInitEmptyRemote(t *testing.T) {
	testgit.Isolate(t)
	remote := testgit.BareRemote(t)
	a := newStore(t, remote)

	m, err := a.Manifest()
	if err != nil || len(m.Repos) != 0 {
		t.Fatalf("manifest: %+v %v", m, err)
	}
	// The manifest was pushed: a second machine sees it.
	b := newStore(t, remote)
	if _, err := b.Manifest(); err != nil {
		t.Fatal(err)
	}
	ha, _ := a.Head()
	hb, _ := b.Head()
	if ha == "" || ha != hb {
		t.Fatalf("heads differ: %s vs %s", ha, hb)
	}
	if log := testgit.Git(t, a.Dir, "log", "--oneline"); strings.Count(log, "\n") != 0 {
		t.Fatalf("expected exactly one commit, got:\n%s", log)
	}

	// Init again on the same directory and remote is a no-op.
	if _, err := Init(remote, a.Dir); err != nil {
		t.Fatal(err)
	}
	// Init on that directory for another remote is refused.
	if _, err := Init(testgit.BareRemote(t), a.Dir); err == nil {
		t.Fatal("expected error for a different remote")
	}
}

func TestInitRejectsNonStore(t *testing.T) {
	testgit.Isolate(t)
	// A repository with content but no manifest gets one; a broken manifest is an error.
	remote := testgit.BareRemote(t)
	seed := testgit.Repo(t)
	testgit.WriteFile(t, filepath.Join(seed, manifest.FileName), "version: [\n")
	testgit.Git(t, seed, "add", ".")
	testgit.Git(t, seed, "commit", "--quiet", "-m", "broken")
	testgit.Git(t, seed, "push", "--quiet", remote, "HEAD:refs/heads/main")
	if _, err := Init(remote, filepath.Join(t.TempDir(), "store")); err == nil {
		t.Fatal("expected manifest error")
	}
}

func TestFoldersAndPush(t *testing.T) {
	testgit.Isolate(t)
	s := newStore(t, testgit.BareRemote(t))

	files := map[string][]byte{
		".vscode/launch.json":          []byte("// comment kept\n{ \"version\": \"0.2.0\", }\n"),
		"apps/api/.vscode/tasks.json":  []byte("{}\n"),
		"apps/web/.vscode/launch.json": []byte("{}\n"),
	}
	if err := s.WriteFolder("mono", files); err != nil {
		t.Fatal(err)
	}
	c1, err := s.CommitAndPush("first")
	if err != nil || c1 == "" {
		t.Fatalf("commit: %q %v", c1, err)
	}
	// No changes: no commit.
	if c, err := s.CommitAndPush("nothing"); err != nil || c != "" {
		t.Fatalf("empty commit: %q %v", c, err)
	}

	got, err := s.ReadFolder("mono")
	if err != nil || len(got) != 3 || string(got[".vscode/launch.json"]) != string(files[".vscode/launch.json"]) {
		t.Fatalf("read back: %v %v", got, err)
	}

	// Mirroring removes files that are gone, and their empty directories.
	delete(files, "apps/web/.vscode/launch.json")
	if err := s.WriteFolder("mono", files); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "mono", "apps", "web")); !os.IsNotExist(err) {
		t.Fatalf("empty dir left behind: %v", err)
	}
	if _, err := s.CommitAndPush("remove web"); err != nil {
		t.Fatal(err)
	}

	if got, err := s.ReadFolder("never-written"); err != nil || len(got) != 0 {
		t.Fatalf("missing folder: %v %v", got, err)
	}
	for _, bad := range []string{"../escape", "/abs", "a/../b", ".git/config", `a\b`, "a//b", ""} {
		if err := s.WriteFolder("mono", map[string][]byte{bad: nil}); err == nil {
			t.Errorf("WriteFolder accepted %q", bad)
		}
	}
	if err := s.WriteFolder("../x", nil); err == nil {
		t.Error("WriteFolder accepted a bad folder")
	}
}

func TestPushRace(t *testing.T) {
	testgit.Isolate(t)
	remote := testgit.BareRemote(t)
	a := newStore(t, remote)
	b := newStore(t, remote)

	if err := a.WriteFolder("mono", map[string][]byte{"a.json": []byte("a")}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.CommitAndPush("from a"); err != nil {
		t.Fatal(err)
	}

	// b has not synced since a pushed.
	if err := b.WriteFolder("mono", map[string][]byte{"b.json": []byte("b")}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.CommitAndPush("from b"); !errors.Is(err, ErrRemoteAhead) {
		t.Fatalf("expected ErrRemoteAhead, got %v", err)
	}
	// The losing commit was rolled back and b now matches the remote.
	ha, _ := a.Head()
	hb, _ := b.Head()
	if ha != hb {
		t.Fatalf("b not synced: %s vs %s", hb, ha)
	}
	got, _ := b.ReadFolder("mono")
	if len(got) != 1 || string(got["a.json"]) != "a" {
		t.Fatalf("b content after rollback: %v", got)
	}
	if st := testgit.Git(t, b.Dir, "status", "--porcelain"); st != "" {
		t.Fatalf("b not clean: %s", st)
	}

	// After syncing, b can push.
	if err := b.WriteFolder("mono", map[string][]byte{"a.json": []byte("a"), "b.json": []byte("b")}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.CommitAndPush("from b again"); err != nil {
		t.Fatal(err)
	}
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	if got, _ := a.ReadFolder("mono"); len(got) != 2 {
		t.Fatalf("a after sync: %v", got)
	}
}

func TestSyncDiscardsLocalChanges(t *testing.T) {
	testgit.Isolate(t)
	s := newStore(t, testgit.BareRemote(t))
	testgit.WriteFile(t, filepath.Join(s.Dir, "stray", "file"), "x")
	testgit.WriteFile(t, filepath.Join(s.Dir, manifest.FileName), "garbage")
	if err := s.Sync(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Manifest(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "stray")); !os.IsNotExist(err) {
		t.Fatal("untracked files not removed")
	}
}

func TestManifestInStore(t *testing.T) {
	testgit.Isolate(t)
	s := newStore(t, testgit.BareRemote(t))
	m, _ := s.Manifest()
	if _, _, err := m.Link("mono", repoid.Identity{Remotes: []string{"github.com/acme/mono"}, RootCommits: []string{"abc"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveManifest(m); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CommitAndPush("link"); err != nil {
		t.Fatal(err)
	}
	other := newStore(t, testgit.Git(t, s.Dir, "remote", "get-url", "origin"))
	m2, err := other.Manifest()
	if err != nil || len(m2.Repos) != 1 || m2.Repos[0].Folder != "mono" {
		t.Fatalf("%+v %v", m2, err)
	}
}

func TestLock(t *testing.T) {
	s := &Store{Dir: filepath.Join(t.TempDir(), "store")}
	defer func(a, b time.Duration) { lockTimeout, lockStale = a, b }(lockTimeout, lockStale)
	lockTimeout, lockStale = 300*time.Millisecond, time.Hour

	unlock, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lock(); err == nil {
		t.Fatal("second lock should time out")
	}
	unlock()

	// Mutual exclusion under contention.
	lockTimeout = 10 * time.Second
	var mu sync.Mutex
	inside, maxInside := 0, 0
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock, err := s.Lock()
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			inside++
			if inside > maxInside {
				maxInside = inside
			}
			mu.Unlock()
			time.Sleep(5 * time.Millisecond)
			mu.Lock()
			inside--
			mu.Unlock()
			unlock()
		}()
	}
	wg.Wait()
	if maxInside != 1 {
		t.Fatalf("%d holders at once", maxInside)
	}

	// A stale lock is taken over.
	lockStale = 0
	testgit.WriteFile(t, s.Dir+".lock", "old")
	old := time.Now().Add(-time.Minute)
	_ = os.Chtimes(s.Dir+".lock", old, old)
	unlock, err = s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	unlock()
}
