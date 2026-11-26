package manifest

import (
	"errors"
	"strings"
	"testing"

	"github.com/bruno-brant/moreco/internal/repoid"
)

func id(root string, remotes ...string) repoid.Identity {
	var roots []string
	if root != "" {
		roots = []string{root}
	}
	return repoid.Identity{Root: "/src/x", Remotes: remotes, RootCommits: roots}
}

func TestRoundTrip(t *testing.T) {
	m := New()
	if _, _, err := m.Link("web", id("bbb", "github.com/acme/web")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Link("api", id("aaa", "github.com/acme/api")); err != nil {
		t.Fatal(err)
	}
	data, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	want := `# moreco store manifest. Maps projects to folders in this repository.
version: 1
repos:
  - folder: api
    remotes:
      - github.com/acme/api
    rootCommit: aaa
    files:
      - '**/.vscode/launch.json'
      - '**/.vscode/tasks.json'
  - folder: web
    remotes:
      - github.com/acme/web
    rootCommit: bbb
    files:
      - '**/.vscode/launch.json'
      - '**/.vscode/tasks.json'
`
	if string(data) != want {
		t.Fatalf("got:\n%s", data)
	}
	back, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Repos) != 2 || back.Repos[0].Folder != "api" {
		t.Fatalf("parsed: %+v", back)
	}
}

func TestParseEmptyAndErrors(t *testing.T) {
	m, err := Parse([]byte("version: 1\n"))
	if err != nil || m.Repos == nil {
		t.Fatalf("empty manifest: %v %+v", err, m)
	}
	bad := map[string]string{
		"future version": "version: 99\n",
		"bad folder":     "repos: [{folder: ../x, files: [a]}]\n",
		"dup folder":     "repos: [{folder: a, files: [x]}, {folder: a, files: [x]}]\n",
		"shared remote":  "repos: [{folder: a, remotes: [h/r], files: [x]}, {folder: b, remotes: [h/r], files: [x]}]\n",
		"no files":       "repos: [{folder: a}]\n",
		"abs pattern":    "repos: [{folder: a, files: [/etc/passwd]}]\n",
		"dotdot pattern": "repos: [{folder: a, files: [../x]}]\n",
		"git pattern":    "repos: [{folder: a, files: [.git/config]}]\n",
		"bad glob":       "repos: [{folder: a, files: ['[']}]\n",
		"not yaml":       "repos: {\n",
	}
	for name, src := range bad {
		if _, err := Parse([]byte(src)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestFind(t *testing.T) {
	m := New()
	m.Repos = []Repo{
		{Folder: "mono", Remotes: []string{"github.com/acme/mono", "gitlab.com/acme/mono"}, RootCommit: "r1", Files: DefaultFiles},
		{Folder: "other", Remotes: []string{"github.com/acme/other"}, RootCommit: "r2", Files: DefaultFiles},
		{Folder: "fork-a", Remotes: []string{"github.com/a/lib"}, RootCommit: "r3", Files: DefaultFiles},
		{Folder: "fork-b", Remotes: []string{"github.com/b/lib"}, RootCommit: "r3", Files: DefaultFiles},
	}

	r, kind, err := m.Find(id("r1", "gitlab.com/acme/mono"))
	if err != nil || r.Folder != "mono" || kind != ByRemote {
		t.Errorf("by remote: %v %v %v", r, kind, err)
	}
	// Shallow clone (no root commit) still matches by remote.
	if r, _, err := m.Find(id("", "github.com/acme/mono")); err != nil || r.Folder != "mono" {
		t.Errorf("shallow: %v %v", r, err)
	}
	// Unknown remote, known history: matched by root commit.
	r, kind, err = m.Find(id("r2", "example.com/mirror/other"))
	if err != nil || r.Folder != "other" || kind != ByRootCommit {
		t.Errorf("by root: %v %v %v", r, kind, err)
	}
	// Remote reused for an unrelated repository.
	if _, _, err := m.Find(id("zzz", "github.com/acme/mono")); !errors.Is(err, ErrMismatch) {
		t.Errorf("mismatch: %v", err)
	}
	// Remotes of two folders.
	if _, _, err := m.Find(id("r1", "github.com/acme/mono", "github.com/acme/other")); !errors.Is(err, ErrAmbiguous) {
		t.Errorf("ambiguous remote: %v", err)
	}
	// Two forks share history; an unlisted remote cannot pick one.
	if _, _, err := m.Find(id("r3", "github.com/c/lib")); !errors.Is(err, ErrAmbiguous) {
		t.Errorf("ambiguous root: %v", err)
	}
	// But a listed remote can.
	if r, _, err := m.Find(id("r3", "github.com/b/lib")); err != nil || r.Folder != "fork-b" {
		t.Errorf("fork by remote: %v %v", r, err)
	}
	if _, _, err := m.Find(id("nope", "example.com/x")); !errors.Is(err, ErrNotLinked) {
		t.Errorf("not linked: %v", err)
	}
	if _, _, err := m.Find(id("")); !errors.Is(err, ErrNotLinked) {
		t.Errorf("nothing to go on: %v", err)
	}
}

func TestLink(t *testing.T) {
	m := New()
	r, changed, err := m.Link("mono", id("r1", "github.com/acme/mono"))
	if err != nil || !changed || r.RootCommit != "r1" {
		t.Fatalf("first link: %+v %v %v", r, changed, err)
	}
	// Same checkout again: nothing to do.
	if _, changed, err := m.Link("mono", id("r1", "github.com/acme/mono")); err != nil || changed {
		t.Errorf("relink: %v %v", changed, err)
	}
	// A second remote for the same project joins the folder.
	r, changed, err = m.Link("mono", id("r1", "gitlab.com/acme/mono"))
	if err != nil || !changed || strings.Join(r.Remotes, ",") != "github.com/acme/mono,gitlab.com/acme/mono" {
		t.Errorf("add remote: %+v %v %v", r, changed, err)
	}
	// A different project cannot be linked into the folder.
	if _, _, err := m.Link("mono", id("other", "github.com/x/y")); !errors.Is(err, ErrMismatch) {
		t.Errorf("mismatch: %v", err)
	}
	// A remote cannot belong to two folders.
	if _, _, err := m.Link("copy", id("r1", "github.com/acme/mono")); err == nil {
		t.Error("expected error linking a remote twice")
	}
	if _, _, err := m.Link(".hidden", id("r9", "h/r")); err == nil {
		t.Error("expected invalid folder error")
	}
	if _, _, err := m.Link("bare", id("")); err == nil {
		t.Error("expected error for repo with no remotes and no commits")
	}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSuggestFolder(t *testing.T) {
	if got := SuggestFolder(id("", "github.com/acme/my-mono")); got != "my-mono" {
		t.Error(got)
	}
	if got := SuggestFolder(repoid.Identity{Root: "/home/me/.Weird Name!"}); got != "Weird-Name-" {
		t.Error(got)
	}
}

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		pat, name string
		want      bool
	}{
		{"**/.vscode/launch.json", ".vscode/launch.json", true},
		{"**/.vscode/launch.json", "apps/api/.vscode/launch.json", true},
		{"**/.vscode/launch.json", "apps/api/.vscode/tasks.json", false},
		{"**/.vscode/*.json", "a/.vscode/settings.json", true},
		{"**/.vscode/*.json", "a/.vscode/sub/x.json", false},
		{".vscode/**", ".vscode/a/b/c", true},
		{"apps/*/.env.local", "apps/web/.env.local", true},
		{"apps/*/.env.local", "apps/web/x/.env.local", false},
		{"**", "anything/at/all", true},
		{".idea/**/*.xml", ".idea/workspace.xml", true},
	}
	for _, c := range cases {
		if got := MatchGlob(c.pat, c.name); got != c.want {
			t.Errorf("MatchGlob(%q, %q) = %v", c.pat, c.name, got)
		}
	}
	r := Repo{Files: DefaultFiles}
	if !r.Matches("apps/api/.vscode/tasks.json") || r.Matches("README.md") {
		t.Error("Repo.Matches")
	}
}
