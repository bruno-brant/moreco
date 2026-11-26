// Package manifest reads and writes moreco.yaml, the store's index of which
// projects it holds and which files it manages for each.
package manifest

import (
	"errors"
	"fmt"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/bruno-brant/moreco/internal/repoid"
)

// FileName is the manifest's name at the root of the store.
const FileName = "moreco.yaml"

// CurrentVersion is the manifest format version written by this build.
const CurrentVersion = 1

// DefaultFiles are the globs a newly linked project manages.
var DefaultFiles = []string{
	"**/.vscode/launch.json",
	"**/.vscode/tasks.json",
}

// Manifest is the content of moreco.yaml.
type Manifest struct {
	Version int    `yaml:"version"`
	Repos   []Repo `yaml:"repos"`
}

// Repo is one project held in the store.
type Repo struct {
	// Folder is the project's directory at the root of the store.
	Folder string `yaml:"folder"`
	// Remotes are normalized remote URLs (see repoid.NormalizeRemote).
	Remotes []string `yaml:"remotes,omitempty"`
	// RootCommit is the project's root commit, used to recognize checkouts
	// whose remotes are not listed and to catch a remote URL reused for an
	// unrelated repository.
	RootCommit string `yaml:"rootCommit,omitempty"`
	// Files are glob patterns, relative to the project root, of the files
	// moreco manages. "**" matches any number of directories.
	Files []string `yaml:"files"`
}

// New returns an empty manifest.
func New() *Manifest { return &Manifest{Version: CurrentVersion, Repos: []Repo{}} }

// Load reads a manifest file.
func Load(file string) (*Manifest, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse decodes and validates a manifest.
func Parse(data []byte) (*Manifest, error) {
	m := New()
	if err := yaml.Unmarshal(data, m); err != nil {
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}
	if m.Version > CurrentVersion {
		return nil, fmt.Errorf("%s: version %d is newer than this moreco understands (%d); upgrade moreco",
			FileName, m.Version, CurrentVersion)
	}
	if m.Repos == nil {
		m.Repos = []Repo{}
	}
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}
	return m, nil
}

// Save writes the manifest to file.
func (m *Manifest) Save(file string) error {
	data, err := m.Marshal()
	if err != nil {
		return err
	}
	return os.WriteFile(file, data, 0o644)
}

// Marshal encodes the manifest, with repos sorted by folder.
func (m *Manifest) Marshal() ([]byte, error) {
	sort.Slice(m.Repos, func(i, j int) bool { return m.Repos[i].Folder < m.Repos[j].Folder })
	var b strings.Builder
	b.WriteString("# moreco store manifest. Maps projects to folders in this repository.\n")
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

var folderRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ValidateFolder checks that name can be used as a project folder: a single
// path segment of letters, digits, '.', '_' and '-', not starting with a
// punctuation character (so it cannot collide with .git or reserved names).
func ValidateFolder(name string) error {
	if !folderRE.MatchString(name) {
		return fmt.Errorf("invalid folder name %q: use letters, digits, '.', '_' and '-', starting with a letter or digit", name)
	}
	return nil
}

// ValidatePattern checks a file glob.
func ValidatePattern(p string) error {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, `\`) {
		return fmt.Errorf("invalid file pattern %q: must be a relative path using '/'", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("invalid file pattern %q: empty, '.' or '..' segment", p)
		}
		if seg == ".git" {
			return fmt.Errorf("invalid file pattern %q: cannot manage files inside .git", p)
		}
		if seg != "**" {
			if _, err := path.Match(seg, ""); err != nil {
				return fmt.Errorf("invalid file pattern %q: %w", p, err)
			}
		}
	}
	return nil
}

// Validate checks the whole manifest for consistency.
func (m *Manifest) Validate() error {
	folders := map[string]bool{}
	remotes := map[string]string{}
	for _, r := range m.Repos {
		if err := ValidateFolder(r.Folder); err != nil {
			return err
		}
		if folders[r.Folder] {
			return fmt.Errorf("folder %q listed twice", r.Folder)
		}
		folders[r.Folder] = true
		for _, rem := range r.Remotes {
			if other, ok := remotes[rem]; ok {
				return fmt.Errorf("remote %q belongs to both %q and %q", rem, other, r.Folder)
			}
			remotes[rem] = r.Folder
		}
		if len(r.Files) == 0 {
			return fmt.Errorf("folder %q: no files to manage", r.Folder)
		}
		for _, p := range r.Files {
			if err := ValidatePattern(p); err != nil {
				return fmt.Errorf("folder %q: %w", r.Folder, err)
			}
		}
	}
	return nil
}

// Errors returned by Find.
var (
	ErrNotLinked = errors.New("this repository is not linked to the store (run `moreco link`)")
	ErrAmbiguous = errors.New("this repository matches more than one store folder")
	ErrMismatch  = errors.New("remote matches a store folder, but the repository history does not")
)

// MatchKind says how Find recognized a checkout.
type MatchKind int

const (
	// ByRemote: one of the checkout's remotes is listed for the folder.
	ByRemote MatchKind = iota + 1
	// ByRootCommit: no listed remote, but the root commit matches. The caller
	// may want to add the checkout's remotes to the folder.
	ByRootCommit
)

// Find returns the repo entry for a checkout.
func (m *Manifest) Find(id repoid.Identity) (*Repo, MatchKind, error) {
	var byRemote []*Repo
	for i := range m.Repos {
		r := &m.Repos[i]
		for _, rem := range r.Remotes {
			if id.HasRemote(rem) {
				byRemote = append(byRemote, r)
				break
			}
		}
	}
	switch len(byRemote) {
	case 1:
		r := byRemote[0]
		if r.RootCommit != "" && id.RootCommit() != "" && !id.HasRootCommit(r.RootCommit) {
			return nil, 0, fmt.Errorf("%w: folder %q has root commit %s", ErrMismatch, r.Folder, short(r.RootCommit))
		}
		return r, ByRemote, nil
	case 0:
	default:
		return nil, 0, fmt.Errorf("%w by remote: %s", ErrAmbiguous, folderNames(byRemote))
	}

	if id.RootCommit() == "" {
		return nil, 0, ErrNotLinked
	}
	var byRoot []*Repo
	for i := range m.Repos {
		if r := &m.Repos[i]; r.RootCommit != "" && id.HasRootCommit(r.RootCommit) {
			byRoot = append(byRoot, r)
		}
	}
	switch len(byRoot) {
	case 0:
		return nil, 0, ErrNotLinked
	case 1:
		return byRoot[0], ByRootCommit, nil
	default:
		return nil, 0, fmt.Errorf("%w by history (forks?): %s; run `moreco link --name <folder>` to choose",
			ErrAmbiguous, folderNames(byRoot))
	}
}

// Link registers a checkout under folder. If the folder exists, the
// checkout's remotes are added to it (several remotes may share a folder);
// otherwise a new entry managing DefaultFiles is created. It returns the
// entry and whether the manifest changed.
func (m *Manifest) Link(folder string, id repoid.Identity) (*Repo, bool, error) {
	if err := ValidateFolder(folder); err != nil {
		return nil, false, err
	}
	for _, rem := range id.Remotes {
		for _, r := range m.Repos {
			if r.Folder != folder && contains(r.Remotes, rem) {
				return nil, false, fmt.Errorf("remote %s is already linked to folder %q", rem, r.Folder)
			}
		}
	}

	var r *Repo
	for i := range m.Repos {
		if m.Repos[i].Folder == folder {
			r = &m.Repos[i]
		}
	}
	changed := false
	if r == nil {
		m.Repos = append(m.Repos, Repo{Folder: folder, Files: append([]string(nil), DefaultFiles...)})
		r = &m.Repos[len(m.Repos)-1]
		changed = true
	}

	switch {
	case r.RootCommit == "" && id.RootCommit() != "":
		r.RootCommit = id.RootCommit()
		changed = true
	case r.RootCommit != "" && id.RootCommit() != "" && !id.HasRootCommit(r.RootCommit):
		return nil, false, fmt.Errorf("%w: folder %q holds a project with root commit %s",
			ErrMismatch, folder, short(r.RootCommit))
	}
	for _, rem := range id.Remotes {
		if !contains(r.Remotes, rem) {
			r.Remotes = append(r.Remotes, rem)
			changed = true
		}
	}
	sort.Strings(r.Remotes)
	if r.RootCommit == "" && len(r.Remotes) == 0 {
		return nil, false, errors.New("cannot link a repository with no remotes and no commits")
	}
	return r, changed, nil
}

// SuggestFolder proposes a folder name for a checkout: the last path segment
// of its first remote, or of its root directory.
func SuggestFolder(id repoid.Identity) string {
	name := path.Base(filepathToSlash(id.Root))
	if len(id.Remotes) > 0 {
		name = path.Base(id.Remotes[0])
	}
	name = regexp.MustCompile(`[^A-Za-z0-9._-]+`).ReplaceAllString(name, "-")
	name = strings.TrimLeft(name, "._-")
	if name == "" {
		name = "repo"
	}
	return name
}

// Matches reports whether rel (a slash-separated path relative to the project
// root) is managed by this entry.
func (r *Repo) Matches(rel string) bool {
	for _, p := range r.Files {
		if MatchGlob(p, rel) {
			return true
		}
	}
	return false
}

// MatchGlob matches a slash-separated path against a pattern where "**" is a
// whole segment matching zero or more segments and other segments follow
// path.Match.
func MatchGlob(pattern, name string) bool {
	return matchSegs(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func matchSegs(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			for i := 0; i <= len(name); i++ {
				if matchSegs(pat[1:], name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		if ok, _ := path.Match(pat[0], name[0]); !ok {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func folderNames(rs []*Repo) string {
	var names []string
	for _, r := range rs {
		names = append(names, r.Folder)
	}
	return strings.Join(names, ", ")
}

func short(c string) string {
	if len(c) > 12 {
		return c[:12]
	}
	return c
}

func filepathToSlash(p string) string { return strings.ReplaceAll(p, `\`, "/") }
