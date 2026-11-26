// Command moreco keeps per-repository editor configuration in a private git
// repository and restores it into any clone or worktree.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/bruno-brant/moreco/internal/manifest"
	"github.com/bruno-brant/moreco/internal/repoid"
	"github.com/bruno-brant/moreco/internal/store"
)

const usage = `usage: moreco <command> [arguments]

Commands:
  init <remote>       set up the store from a git remote you control
  link [--name NAME]  register the current repository in the store
  info                show the store and how the current repository maps to it

The store is cloned to $MORECO_STORE, or to the user cache directory by default.
`

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "moreco:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(out, usage)
		return errors.New("no command given")
	}
	switch args[0] {
	case "init":
		return cmdInit(args[1:], out)
	case "link":
		return cmdLink(args[1:], out)
	case "info":
		return cmdInfo(args[1:], out)
	case "help", "-h", "--help":
		fmt.Fprint(out, usage)
		return nil
	}
	return fmt.Errorf("unknown command %q (see `moreco help`)", args[0])
}

func cmdInit(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: moreco init <remote>")
	}
	dir, err := store.DefaultDir()
	if err != nil {
		return err
	}
	s, err := store.Init(fs.Arg(0), dir)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "store ready at %s (branch %s)\n", s.Dir, s.Branch)
	return nil
}

func openStore() (*store.Store, error) {
	dir, err := store.DefaultDir()
	if err != nil {
		return nil, err
	}
	return store.Open(dir)
}

func cmdLink(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("link", flag.ContinueOnError)
	name := fs.String("name", "", "store folder for this repository (default: derived from the remote)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: moreco link [--name NAME]")
	}
	id, err := repoid.Identify(".")
	if err != nil {
		return err
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	unlock, err := s.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	if err := s.Sync(); err != nil {
		return err
	}
	m, err := s.Manifest()
	if err != nil {
		return err
	}

	folder := *name
	if folder == "" {
		existing, _, err := m.Find(id)
		switch {
		case err == nil:
			folder = existing.Folder
		case errors.Is(err, manifest.ErrNotLinked):
			folder = manifest.SuggestFolder(id)
		default:
			return err
		}
	}

	r, changed, err := m.Link(folder, id)
	if err != nil {
		return err
	}
	if !changed {
		fmt.Fprintf(out, "already linked to %q\n", r.Folder)
		return nil
	}
	if err := s.SaveManifest(m); err != nil {
		return err
	}
	if _, err := s.CommitAndPush(fmt.Sprintf("Link %s (%s)", r.Folder, strings.Join(id.Remotes, ", "))); err != nil {
		if errors.Is(err, store.ErrRemoteAhead) {
			return fmt.Errorf("%w; run the command again", err)
		}
		return err
	}
	fmt.Fprintf(out, "linked to %q\n", r.Folder)
	return nil
}

func cmdInfo(args []string, out io.Writer) error {
	if len(args) != 0 {
		return errors.New("usage: moreco info")
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	remote, _ := s.RemoteURL()
	head, _ := s.Head()
	fmt.Fprintf(out, "store:   %s\nremote:  %s\nbranch:  %s\ncommit:  %s\n", s.Dir, remote, s.Branch, head)

	id, err := repoid.Identify(".")
	if err != nil {
		fmt.Fprintln(out, "repo:    (not inside a git repository)")
		return nil
	}
	fmt.Fprintf(out, "repo:    %s\nremotes: %s\nroot:    %s\n", id.Root, strings.Join(id.Remotes, ", "), orNone(id.RootCommit()))

	m, err := s.Manifest()
	if err != nil {
		return err
	}
	r, kind, err := m.Find(id)
	if err != nil {
		fmt.Fprintf(out, "folder:  (%v)\n", err)
		return nil
	}
	how := "remote"
	if kind == manifest.ByRootCommit {
		how = "history; run `moreco link` to add this remote"
	}
	fmt.Fprintf(out, "folder:  %s (matched by %s)\nfiles:   %s\n", r.Folder, how, strings.Join(r.Files, ", "))
	return nil
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
