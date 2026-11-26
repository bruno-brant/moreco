package ops

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bruno-brant/moreco/internal/repoid"
	"github.com/bruno-brant/moreco/internal/store"
	"github.com/bruno-brant/moreco/internal/testgit"
)

const launch = ".vscode/launch.json"

type env struct {
	t      *testing.T
	remote string
	store  *store.Store
	main   string // main checkout of the project
}

func setup(t *testing.T) *env {
	t.Helper()
	testgit.Isolate(t)
	remote := testgit.BareRemote(t)
	s, err := store.Init(remote, filepath.Join(t.TempDir(), "store"))
	if err != nil {
		t.Fatal(err)
	}
	main := testgit.Repo(t)
	testgit.Git(t, main, "remote", "add", "origin", "git@github.com:acme/mono.git")
	e := &env{t: t, remote: remote, store: s, main: main}
	e.link(s, main, "mono")
	return e
}

func (e *env) link(s *store.Store, dir, folder string) {
	e.t.Helper()
	id, err := repoid.Identify(dir)
	if err != nil {
		e.t.Fatal(err)
	}
	if err := s.Sync(); err != nil {
		e.t.Fatal(err)
	}
	m, _ := s.Manifest()
	if _, _, err := m.Link(folder, id); err != nil {
		e.t.Fatal(err)
	}
	if err := s.SaveManifest(m); err != nil {
		e.t.Fatal(err)
	}
	if _, err := s.CommitAndPush("link " + folder); err != nil {
		e.t.Fatal(err)
	}
}

// worktree adds a linked worktree of the main checkout.
func (e *env) worktree(name string) string {
	e.t.Helper()
	dir := filepath.Join(e.t.TempDir(), name)
	testgit.Git(e.t, e.main, "worktree", "add", "--quiet", "-b", name, dir)
	return dir
}

// machine returns a second store clone, as on another computer.
func (e *env) machine() *store.Store {
	e.t.Helper()
	s, err := store.Init(e.remote, filepath.Join(e.t.TempDir(), "store2"))
	if err != nil {
		e.t.Fatal(err)
	}
	return s
}

func (e *env) session(dir string) *Session {
	e.t.Helper()
	return e.sessionOn(e.store, dir)
}

func (e *env) sessionOn(s *store.Store, dir string) *Session {
	e.t.Helper()
	x, err := Open(dir, s, Options{})
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(x.Close)
	return x
}

func (e *env) push(dir string) (*Report, error) {
	x := e.session(dir)
	defer x.Close()
	return x.Push("test push")
}

func (e *env) mustPush(dir string) *Report {
	e.t.Helper()
	r, err := e.push(dir)
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

func (e *env) pull(dir string) *Report {
	e.t.Helper()
	x := e.session(dir)
	defer x.Close()
	r, err := x.Pull()
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

func read(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	testgit.WriteFile(t, filepath.Join(dir, filepath.FromSlash(rel)), content)
}

func kinds(r *Report) string {
	var s []string
	for _, a := range r.Actions {
		s = append(s, string(a.Kind)+":"+a.Path)
	}
	return strings.Join(s, " ")
}

func launchFile(root string) string {
	return `{
	// Comments and formatting survive.
	"version": "0.2.0",
	"configurations": [
		{
			"name": "API",
			"program": "` + root + `/apps/api/main.go",
			"port": 9229,
		},
	],
}
`
}

func TestPushThenPullIntoNewWorktree(t *testing.T) {
	e := setup(t)
	write(t, e.main, launch, launchFile(e.main))
	write(t, e.main, "apps/api/.vscode/tasks.json", "{ /* api tasks */ }\n")
	write(t, e.main, "apps/api/.vscode/settings.json", "{}\n") // not managed
	r := e.mustPush(e.main)
	if got := kinds(r); got != "added:.vscode/launch.json added:apps/api/.vscode/tasks.json" {
		t.Fatalf("push: %s", got)
	}

	stored, _ := e.store.ReadFolder("mono")
	if s := string(stored[launch]); !strings.Contains(s, "{{moreco:root}}/apps/api") || strings.Contains(s, e.main) {
		t.Fatalf("path not tokenized in store:\n%s", s)
	}

	wt := e.worktree("feature")
	r = e.pull(wt)
	if got := kinds(r); got != "created:.vscode/launch.json created:apps/api/.vscode/tasks.json" {
		t.Fatalf("pull: %s", got)
	}
	if got := read(t, wt, launch); got != launchFile(wt) {
		t.Fatalf("worktree launch.json:\n%s", got)
	}
	// Pulling again changes nothing.
	if r := e.pull(wt); len(r.Actions) != 0 {
		t.Fatalf("second pull: %s", kinds(r))
	}
	// Nor does pushing.
	if r := e.mustPush(wt); len(r.Actions) != 0 {
		t.Fatalf("no-op push: %s", kinds(r))
	}
}

func TestFixesCopiedMainCheckoutPaths(t *testing.T) {
	e := setup(t)
	wt := e.worktree("feature")
	// The worktree got a copy (or symlink target) pointing at the main checkout.
	write(t, wt, launch, launchFile(e.main))
	e.mustPush(wt)
	other := e.worktree("other")
	e.pull(other)
	if got := read(t, other, launch); got != launchFile(other) {
		t.Fatalf("expected paths of the new worktree:\n%s", got)
	}
}

func TestPushRequiresPull(t *testing.T) {
	e := setup(t)
	write(t, e.main, launch, "line1\nline2\nline3\nline4\nline5\n")
	e.mustPush(e.main)
	wt := e.worktree("feature")
	e.pull(wt)

	write(t, wt, launch, "line1 (wt)\nline2\nline3\nline4\nline5\n")
	e.mustPush(wt)

	write(t, e.main, launch, "line1\nline2\nline3\nline4\nline5 (main)\n")
	if _, err := e.push(e.main); !errors.Is(err, ErrBehind) {
		t.Fatalf("expected ErrBehind, got %v", err)
	}
	r := e.pull(e.main)
	if got := kinds(r); got != "merged:.vscode/launch.json" {
		t.Fatalf("pull: %s", got)
	}
	if got := read(t, e.main, launch); got != "line1 (wt)\nline2\nline3\nline4\nline5 (main)\n" {
		t.Fatalf("merged:\n%s", got)
	}
	e.mustPush(e.main)
	e.pull(wt)
	if got := read(t, wt, launch); got != "line1 (wt)\nline2\nline3\nline4\nline5 (main)\n" {
		t.Fatalf("wt after pull:\n%s", got)
	}
}

func TestOtherProjectsDoNotBlockPush(t *testing.T) {
	e := setup(t)
	write(t, e.main, launch, "{}\n")
	e.mustPush(e.main)

	other := testgit.Repo(t)
	testgit.Git(t, other, "remote", "add", "origin", "git@github.com:acme/other.git")
	e.link(e.store, other, "other")
	write(t, other, launch, "{}\n")
	e.mustPush(other)

	write(t, e.main, launch, "{ \"x\": 1 }\n")
	if r := e.mustPush(e.main); kinds(r) != "changed:.vscode/launch.json" {
		t.Fatalf("push: %s", kinds(r))
	}
}

func TestConflictFlow(t *testing.T) {
	e := setup(t)
	write(t, e.main, launch, "a\nport: 1\nz\n")
	e.mustPush(e.main)
	wt := e.worktree("feature")
	e.pull(wt)

	write(t, wt, launch, "a\nport: 2\nz\n")
	e.mustPush(wt)
	write(t, e.main, launch, "a\nport: 3\nz\n")

	r := e.pull(e.main)
	if got := kinds(r); got != "conflict:.vscode/launch.json" {
		t.Fatalf("pull: %s", got)
	}
	got := read(t, e.main, launch)
	for _, want := range []string{"<<<<<<< local", "port: 3", "=======", "port: 2", ">>>>>>> store"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}

	if _, err := e.push(e.main); !errors.Is(err, ErrConflicts) {
		t.Fatalf("push with conflicts: %v", err)
	}
	x := e.session(e.main)
	if _, err := x.Pull(); !errors.Is(err, ErrConflicts) {
		t.Fatalf("pull with conflicts: %v", err)
	}
	if _, err := x.Resolve(e.main, nil); err == nil {
		t.Fatal("resolve should refuse while markers remain")
	}
	write(t, e.main, launch, "a\nport: 4\nz\n")
	resolved, err := x.Resolve(filepath.Join(e.main, ".vscode"), []string{"launch.json"})
	if err != nil || len(resolved) != 1 {
		t.Fatalf("resolve: %v %v", resolved, err)
	}
	x.Close()

	e.mustPush(e.main)
	e.pull(wt)
	if got := read(t, wt, launch); got != "a\nport: 4\nz\n" {
		t.Fatalf("wt: %s", got)
	}
}

func TestDeletions(t *testing.T) {
	e := setup(t)
	write(t, e.main, launch, "{}\n")
	write(t, e.main, "apps/api/.vscode/tasks.json", "{}\n")
	e.mustPush(e.main)
	clean := e.worktree("clean")
	edited := e.worktree("edited")
	e.pull(clean)
	e.pull(edited)
	write(t, edited, "apps/api/.vscode/tasks.json", "{ \"edited\": true }\n")

	if err := os.Remove(filepath.Join(e.main, "apps/api/.vscode/tasks.json")); err != nil {
		t.Fatal(err)
	}
	if r := e.mustPush(e.main); kinds(r) != "deleted:apps/api/.vscode/tasks.json" {
		t.Fatalf("push: %s", kinds(r))
	}

	if r := e.pull(clean); kinds(r) != "deleted:apps/api/.vscode/tasks.json" {
		t.Fatalf("clean pull: %s", kinds(r))
	}
	if _, err := os.Stat(filepath.Join(clean, "apps")); !os.IsNotExist(err) {
		t.Fatal("empty directories left behind")
	}
	r := e.pull(edited)
	if kinds(r) != "conflict:apps/api/.vscode/tasks.json" || !strings.Contains(r.Actions[0].Note, "deleted in the store") {
		t.Fatalf("edited pull: %+v", r.Actions)
	}
	if got := read(t, edited, "apps/api/.vscode/tasks.json"); got != "{ \"edited\": true }\n" {
		t.Fatal("local edit lost")
	}
}

func TestFirstPushIntoPopulatedFolderRequiresPull(t *testing.T) {
	e := setup(t)
	write(t, e.main, launch, "{}\n")
	e.mustPush(e.main)

	// Another machine's fresh clone that never pulled must not overwrite.
	clone := filepath.Join(t.TempDir(), "clone")
	testgit.Git(t, "", "clone", "--quiet", e.main, clone)
	testgit.Git(t, clone, "remote", "set-url", "origin", "git@github.com:acme/mono.git")
	s2 := e.machine()
	x := e.sessionOn(s2, clone)
	if _, err := x.Push("x"); !errors.Is(err, ErrBehind) {
		t.Fatalf("expected ErrBehind, got %v", err)
	}
	r, err := x.Pull()
	if err != nil || kinds(r) != "created:.vscode/launch.json" {
		t.Fatalf("pull on second machine: %v %v", kinds(r), err)
	}
}

func TestNewOnBothSidesAndLocalDeletion(t *testing.T) {
	e := setup(t)
	wt := e.worktree("feature")
	write(t, e.main, launch, "same\n")
	e.mustPush(e.main)
	// The worktree already has an identical file: nothing to do.
	write(t, wt, launch, "same\n")
	if r := e.pull(wt); len(r.Actions) != 0 {
		t.Fatalf("identical: %s", kinds(r))
	}

	// Deleted locally while the store changed it: store version is restored
	// and flagged.
	other := e.worktree("other")
	e.pull(other)
	write(t, wt, launch, "changed\n")
	e.mustPush(wt)
	os.Remove(filepath.Join(other, launch))
	r := e.pull(other)
	if kinds(r) != "conflict:.vscode/launch.json" || read(t, other, launch) != "changed\n" {
		t.Fatalf("delete/modify: %s", kinds(r))
	}
}

func TestTrackedFilesAreLeftAlone(t *testing.T) {
	e := setup(t)
	write(t, e.main, ".vscode/tasks.json", "{ \"committed\": true }\n")
	testgit.Git(t, e.main, "add", ".vscode/tasks.json")
	testgit.Git(t, e.main, "commit", "--quiet", "-m", "team tasks")
	write(t, e.main, launch, "{}\n")
	r := e.mustPush(e.main)
	if got := kinds(r); got != "skipped:.vscode/tasks.json added:.vscode/launch.json" {
		t.Fatalf("push: %s", got)
	}
}

func TestNestedWorktreeNotScanned(t *testing.T) {
	e := setup(t)
	nested := filepath.Join(e.main, ".worktrees", "feature")
	testgit.Git(t, e.main, "worktree", "add", "--quiet", "-b", "nested", nested)
	write(t, nested, launch, "{}\n")
	write(t, e.main, "node_modules/pkg/.vscode/launch.json", "{}\n")
	if r := e.mustPush(e.main); len(r.Actions) != 0 {
		t.Fatalf("picked up files from nested checkout or node_modules: %s", kinds(r))
	}
}

func TestStatus(t *testing.T) {
	e := setup(t)
	write(t, e.main, launch, "a\n")
	write(t, e.main, ".vscode/tasks.json", "t\n")
	e.mustPush(e.main)
	wt := e.worktree("feature")
	e.pull(wt)
	write(t, wt, launch, "b\n")
	e.mustPush(wt)

	write(t, e.main, ".vscode/tasks.json", "t2\n")
	write(t, e.main, "apps/x/.vscode/launch.json", "new\n")
	x := e.session(e.main)
	st, err := x.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !st.Behind {
		t.Error("expected Behind")
	}
	var lines []string
	for _, f := range st.Files {
		lines = append(lines, string([]byte{byte(f.Local), byte(f.Store)})+" "+f.Path)
	}
	want := " M .vscode/launch.json|M  .vscode/tasks.json|A  apps/x/.vscode/launch.json"
	if got := strings.Join(lines, "|"); got != want {
		t.Fatalf("status:\n%q\nwant\n%q", got, want)
	}
}
