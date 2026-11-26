// Package testgit provides helpers for tests that run real git commands in
// temporary directories, isolated from the user's git configuration.
package testgit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bruno-brant/moreco/internal/gitx"
)

// Isolate points git at a fresh global config with a test identity and no
// signing, hooks or system config, for the duration of the test.
func Isolate(t *testing.T) {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	content := "[user]\n\tname = moreco test\n\temail = test@example.invalid\n" +
		"[init]\n\tdefaultBranch = main\n" +
		"[commit]\n\tgpgsign = false\n" +
		"[protocol \"file\"]\n\tallow = always\n"
	if err := os.WriteFile(cfg, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
}

// Git runs git in dir and fails the test on error.
func Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitx.Run(dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// BareRemote creates an empty bare repository and returns its path.
func BareRemote(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "remote.git")
	Git(t, "", "init", "--quiet", "--bare", dir)
	return dir
}

// Repo creates a repository with one commit and returns its path.
func Repo(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "project")
	Git(t, "", "init", "--quiet", dir)
	WriteFile(t, filepath.Join(dir, "README"), "hello\n")
	Git(t, dir, "add", ".")
	Git(t, dir, "commit", "--quiet", "-m", "initial")
	return dir
}

// WriteFile writes content to p, creating parent directories.
func WriteFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
