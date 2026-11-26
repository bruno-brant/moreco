// Package pathtok replaces a checkout's absolute root path with a token when
// files go into the store, and back when they come out, so configs that
// hard-code the checkout location work in every clone and worktree.
//
// The roots of all worktrees of a repository are encoded, so a file copied
// from the main checkout into a worktree (still pointing at the main
// checkout) is fixed too. Decoding always produces the current worktree root.
package pathtok

import (
	"bytes"
	"sort"
	"strings"
)

// Tokens written into stored files.
const (
	Token     = "{{moreco:root}}"      // root with forward slashes
	TokenWin  = "{{moreco:root:win}}"  // root with backslashes (Windows)
	TokenJSON = "{{moreco:root:json}}" // root with backslashes, JSON-escaped
)

// Codec encodes and decodes paths for one worktree.
type Codec struct {
	current string
	pairs   []pair // encoding replacements, longest first
}

type pair struct {
	from, token string
	fold        bool // case-insensitive (Windows paths)
}

// New returns a codec whose current root is current; others are additional
// roots (e.g. the repository's other worktrees) that are also encoded.
func New(current string, others ...string) *Codec {
	c := &Codec{current: current}
	seen := map[string]bool{}
	for _, r := range append([]string{current}, others...) {
		r = strings.TrimRight(r, `/\`)
		if r == "" || seen[r] {
			continue
		}
		seen[r] = true
		slash := strings.ReplaceAll(r, `\`, "/")
		if isWindows(r) {
			win := strings.ReplaceAll(slash, "/", `\`)
			c.pairs = append(c.pairs,
				pair{strings.ReplaceAll(win, `\`, `\\`), TokenJSON, true},
				pair{win, TokenWin, true},
				pair{slash, Token, true})
		} else {
			c.pairs = append(c.pairs, pair{slash, Token, false})
		}
	}
	sort.SliceStable(c.pairs, func(i, j int) bool { return len(c.pairs[i].from) > len(c.pairs[j].from) })
	return c
}

func isWindows(p string) bool {
	return (len(p) >= 2 && p[1] == ':') || strings.HasPrefix(p, `\\`)
}

// Encode replaces worktree roots in content with tokens.
func (c *Codec) Encode(content []byte) []byte {
	for _, p := range c.pairs {
		content = replaceBounded(content, []byte(p.from), []byte(p.token), p.fold)
	}
	return content
}

// Decode replaces tokens in content with the current worktree root.
func (c *Codec) Decode(content []byte) []byte {
	slash := strings.ReplaceAll(strings.TrimRight(c.current, `/\`), `\`, "/")
	win, json := slash, slash
	if isWindows(c.current) {
		win = strings.ReplaceAll(slash, "/", `\`)
		json = strings.ReplaceAll(win, `\`, `\\`)
	}
	content = bytes.ReplaceAll(content, []byte(TokenJSON), []byte(json))
	content = bytes.ReplaceAll(content, []byte(TokenWin), []byte(win))
	return bytes.ReplaceAll(content, []byte(Token), []byte(slash))
}

// replaceBounded replaces occurrences of from that are whole path prefixes:
// not preceded or followed by a file-name character, so "/src/app" does not
// match inside "/src/app2" or "x/src/app".
func replaceBounded(s, from, to []byte, fold bool) []byte {
	if len(from) == 0 {
		return s
	}
	var out []byte
	last := 0
	for i := 0; i+len(from) <= len(s); {
		if matchAt(s, i, from, fold) &&
			(i == 0 || !isNameChar(s[i-1])) &&
			(i+len(from) == len(s) || !isNameChar(s[i+len(from)])) {
			out = append(out, s[last:i]...)
			out = append(out, to...)
			i += len(from)
			last = i
			continue
		}
		i++
	}
	if out == nil {
		return s
	}
	return append(out, s[last:]...)
}

func matchAt(s []byte, i int, from []byte, fold bool) bool {
	w := s[i : i+len(from)]
	if fold {
		return bytes.EqualFold(w, from)
	}
	return bytes.Equal(w, from)
}

func isNameChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		c == '.' || c == '_' || c == '-'
}
