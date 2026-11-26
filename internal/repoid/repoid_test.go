package repoid

import (
	"path/filepath"
	"testing"

	"github.com/bruno-brant/moreco/internal/testgit"
)

func TestNormalizeRemote(t *testing.T) {
	cases := map[string]string{
		"git@github.com:Acme/App.git":            "github.com/acme/app",
		"https://github.com/acme/app":            "github.com/acme/app",
		"https://github.com/acme/app.git/":       "github.com/acme/app",
		"https://user:tok@github.com/acme/app":   "github.com/acme/app",
		"ssh://git@github.com:22/acme/app.git":   "github.com/acme/app",
		"git+ssh://git@gitlab.example.com/g/s/r": "gitlab.example.com/g/s/r",
		"git://example.com/r":                    "example.com/r",
		"host:~user/repo":                        "host/user/repo",
		"/srv/git/Project.git":                   "file:/srv/git/Project.git",
		"file:///srv/git/Project.git":            "file:/srv/git/Project.git",
		"../relative/path":                       "file:../relative/path",
		"  ":                                     "",
	}
	for in, want := range cases {
		if got := NormalizeRemote(in); got != want {
			t.Errorf("NormalizeRemote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIdentify(t *testing.T) {
	testgit.Isolate(t)
	dir := testgit.Repo(t)
	testgit.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/app.git")
	testgit.Git(t, dir, "remote", "add", "mirror", "https://github.com/acme/app") // same repo
	testgit.Git(t, dir, "remote", "add", "fork", "https://github.com/me/app")
	root := testgit.Git(t, dir, "rev-list", "--max-parents=0", "HEAD")

	sub := filepath.Join(dir, "apps", "api")
	testgit.WriteFile(t, filepath.Join(sub, "x"), "")
	id, err := Identify(sub)
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := filepath.EvalSymlinks(dir); id.Root != want && id.Root != dir {
		t.Errorf("Root = %s, want %s", id.Root, dir)
	}
	if len(id.Remotes) != 2 || id.Remotes[0] != "github.com/acme/app" || id.Remotes[1] != "github.com/me/app" {
		t.Errorf("Remotes = %v", id.Remotes)
	}
	if id.RootCommit() != root {
		t.Errorf("RootCommit = %s, want %s", id.RootCommit(), root)
	}

	// A linked worktree has the same identity.
	wt := filepath.Join(t.TempDir(), "wt")
	testgit.Git(t, dir, "worktree", "add", "--quiet", "-b", "feature", wt)
	wid, err := Identify(wt)
	if err != nil {
		t.Fatal(err)
	}
	if wid.RootCommit() != root || len(wid.Remotes) != 2 {
		t.Errorf("worktree identity = %+v", wid)
	}
}

func TestIdentifyShallowAndEmpty(t *testing.T) {
	testgit.Isolate(t)
	dir := testgit.Repo(t)
	testgit.WriteFile(t, filepath.Join(dir, "more"), "x")
	testgit.Git(t, dir, "add", ".")
	testgit.Git(t, dir, "commit", "--quiet", "-m", "second")

	shallow := filepath.Join(t.TempDir(), "shallow")
	testgit.Git(t, "", "clone", "--quiet", "--depth", "1", "file://"+dir, shallow)
	id, err := Identify(shallow)
	if err != nil {
		t.Fatal(err)
	}
	if id.RootCommit() != "" {
		t.Errorf("shallow clone should not report a root commit, got %s", id.RootCommit())
	}
	if len(id.Remotes) != 1 {
		t.Errorf("Remotes = %v", id.Remotes)
	}

	empty := filepath.Join(t.TempDir(), "empty")
	testgit.Git(t, "", "init", "--quiet", empty)
	id, err = Identify(empty)
	if err != nil {
		t.Fatal(err)
	}
	if id.RootCommit() != "" || len(id.Remotes) != 0 {
		t.Errorf("empty repo identity = %+v", id)
	}

	if _, err := Identify(t.TempDir()); err == nil {
		t.Error("expected error outside a repository")
	}
}
