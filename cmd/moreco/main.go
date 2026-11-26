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
	"github.com/bruno-brant/moreco/internal/ops"
	"github.com/bruno-brant/moreco/internal/repoid"
	"github.com/bruno-brant/moreco/internal/store"
)

const usage = `usage: moreco <command> [arguments]

Commands:
  init <remote>       set up the store from a git remote you control
  link [--name NAME]  register the current repository in the store
  info                show the store and how the current repository maps to it
  pull                bring this worktree's files up to date, merging local edits
  push [-m MESSAGE]   save this worktree's files to the store
  status              compare this worktree, its last sync and the store
  resolve [FILE...]   mark conflicted files as resolved (all if none given)

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
	case "pull":
		return cmdPull(args[1:], out)
	case "push":
		return cmdPush(args[1:], out)
	case "status":
		return cmdStatus(args[1:], out)
	case "resolve":
		return cmdResolve(args[1:], out)
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

func session(offline bool) (*ops.Session, error) {
	s, err := openStore()
	if err != nil {
		return nil, err
	}
	return ops.Open(".", s, ops.Options{Offline: offline})
}

func noArgs(name string, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: moreco %s", name)
	}
	return nil
}

func printReport(out io.Writer, r *ops.Report, verb string) {
	if len(r.Actions) == 0 {
		fmt.Fprintf(out, "%s: already up to date\n", r.Folder)
		return
	}
	for _, a := range r.Actions {
		line := fmt.Sprintf("  %-9s %s", a.Kind, a.Path)
		if a.Note != "" {
			line += "  (" + a.Note + ")"
		}
		fmt.Fprintln(out, line)
	}
	fmt.Fprintf(out, "%s %s at store commit %.12s\n", verb, r.Folder, r.StoreCommit)
}

func cmdPull(args []string, out io.Writer) error {
	if err := noArgs("pull", args); err != nil {
		return err
	}
	x, err := session(false)
	if err != nil {
		return err
	}
	defer x.Close()
	r, err := x.Pull()
	if err != nil {
		return err
	}
	printReport(out, r, "pulled")
	if c := r.Conflicts(); len(c) > 0 {
		return fmt.Errorf("%d file(s) in conflict: fix them, then run `moreco resolve` and `moreco push`", len(c))
	}
	return nil
}

func cmdPush(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("push", flag.ContinueOnError)
	msg := fs.String("m", "", "store commit message")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: moreco push [-m MESSAGE]")
	}
	x, err := session(false)
	if err != nil {
		return err
	}
	defer x.Close()
	if *msg == "" {
		*msg = x.PushMessage()
	}
	r, err := x.Push(*msg)
	if err != nil {
		return err
	}
	printReport(out, r, "pushed")
	return nil
}

func cmdStatus(args []string, out io.Writer) error {
	if err := noArgs("status", args); err != nil {
		return err
	}
	x, err := session(false)
	if err != nil {
		return err
	}
	defer x.Close()
	st, err := x.Status()
	if err != nil {
		return err
	}
	synced := "never synced"
	if st.StoreCommit != "" {
		synced = fmt.Sprintf("last synced at %.12s", st.StoreCommit)
	}
	fmt.Fprintf(out, "folder %s, %s\n", st.Folder, synced)
	if st.Behind {
		fmt.Fprintln(out, "the store has changes for this project: run `moreco pull`")
	}
	if len(st.Files) == 0 {
		fmt.Fprintln(out, "no changes")
		return nil
	}
	fmt.Fprintln(out, "(first column: this worktree, second: the store; relative to the last sync)")
	for _, f := range st.Files {
		code := string([]byte{byte(f.Local), byte(f.Store)})
		note := ""
		switch {
		case f.Conflict:
			code, note = "UU", "  (conflict)"
		case f.Tracked:
			code, note = "--", "  (tracked by the project's git; ignored)"
		}
		fmt.Fprintf(out, "  %s %s%s\n", code, f.Path, note)
	}
	return nil
}

func cmdResolve(args []string, out io.Writer) error {
	x, err := session(true)
	if err != nil {
		return err
	}
	defer x.Close()
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	resolved, err := x.Resolve(cwd, args)
	if err != nil {
		return err
	}
	for _, p := range resolved {
		fmt.Fprintf(out, "  resolved  %s\n", p)
	}
	return nil
}
