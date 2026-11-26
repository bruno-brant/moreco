// Package repoid works out which project a git checkout belongs to.
//
// A project is identified by its remotes (normalized, so SSH and HTTPS forms
// of the same URL compare equal) and by its root commit, which is the same in
// every clone and worktree of the project and does not depend on remote names.
package repoid

import (
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bruno-brant/moreco/internal/gitx"
)

// Identity describes a checkout.
type Identity struct {
	// Root is the top-level directory of the worktree.
	Root string
	// Remotes are the normalized URLs of all configured remotes, sorted.
	Remotes []string
	// RootCommits are the commits without parents reachable from HEAD,
	// sorted. Empty for a repository without commits or a shallow clone
	// (whose history is cut, so its "roots" are not the real ones).
	RootCommits []string
}

// RootCommit returns the canonical root commit (the smallest hash), or "".
func (id Identity) RootCommit() string {
	if len(id.RootCommits) == 0 {
		return ""
	}
	return id.RootCommits[0]
}

// HasRemote reports whether the checkout has the given normalized remote.
func (id Identity) HasRemote(r string) bool {
	for _, x := range id.Remotes {
		if x == r {
			return true
		}
	}
	return false
}

// HasRootCommit reports whether c is one of the checkout's root commits.
func (id Identity) HasRootCommit(c string) bool {
	for _, x := range id.RootCommits {
		if x == c {
			return true
		}
	}
	return false
}

// Identify inspects the git checkout containing dir.
func Identify(dir string) (Identity, error) {
	root, err := gitx.Run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return Identity{}, fmt.Errorf("not inside a git worktree: %w", err)
	}
	id := Identity{Root: filepath.Clean(root)}

	out, err := gitx.Run(root, "config", "--get-regexp", `^remote\..*\.url$`)
	if err != nil && gitx.ExitCode(err) != 1 { // 1 = no remotes
		return Identity{}, err
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		_, u, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		if n := NormalizeRemote(u); n != "" && !seen[n] {
			seen[n] = true
			id.Remotes = append(id.Remotes, n)
		}
	}
	sort.Strings(id.Remotes)

	shallow, err := gitx.Run(root, "rev-parse", "--is-shallow-repository")
	if err != nil {
		return Identity{}, err
	}
	if shallow != "true" {
		out, err := gitx.Run(root, "rev-list", "--max-parents=0", "HEAD")
		if err == nil { // fails when there are no commits yet
			for _, c := range strings.Fields(out) {
				id.RootCommits = append(id.RootCommits, c)
			}
			sort.Strings(id.RootCommits)
		}
	}
	return id, nil
}

// NormalizeRemote reduces a git remote URL to "host/path" so that the usual
// spellings of the same repository compare equal:
//
//	git@github.com:acme/app.git     -> github.com/acme/app
//	https://github.com/acme/app     -> github.com/acme/app
//	ssh://git@github.com:22/acme/app -> github.com/acme/app
//
// User info, ports, a trailing ".git" and trailing slashes are dropped and the
// result is lower-cased. Local paths become "file:" plus the cleaned path
// (case preserved).
func NormalizeRemote(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}

	var host, p string
	switch {
	case strings.Contains(s, "://"):
		u, err := url.Parse(s)
		if err != nil {
			return strings.ToLower(s)
		}
		if u.Scheme == "file" {
			return "file:" + filepath.ToSlash(filepath.Clean(u.Path))
		}
		host, p = u.Hostname(), u.Path
	case isSCPLike(s):
		// [user@]host:path
		hp := s
		if at := strings.LastIndex(hp, "@"); at >= 0 {
			hp = hp[at+1:]
		}
		host, p, _ = strings.Cut(hp, ":")
	default:
		return "file:" + filepath.ToSlash(filepath.Clean(s))
	}

	p = strings.Trim(p, "/")
	p = strings.TrimSuffix(p, ".git")
	p = strings.TrimPrefix(p, "~") // host:~user/repo
	p = strings.Trim(p, "/")
	return strings.ToLower(host + "/" + p)
}

// isSCPLike reports whether s looks like git's "[user@]host:path" syntax:
// a colon before any slash, and not a Windows drive letter ("C:\...").
func isSCPLike(s string) bool {
	colon := strings.Index(s, ":")
	if colon <= 0 {
		return false
	}
	if slash := strings.IndexAny(s, `/\`); slash >= 0 && slash < colon {
		return false
	}
	return colon > 1 // "C:" is a drive, not a host
}
