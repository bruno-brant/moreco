// Package textmerge does line-based three-way merges of file contents with
// `git merge-file`, the same algorithm git uses for merges.
package textmerge

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/bruno-brant/moreco/internal/gitx"
)

// Labels name the three sides in conflict markers.
type Labels struct{ Ours, Base, Theirs string }

// Merge merges ours and theirs, both descended from base. On conflict, the
// result contains git-style conflict markers and conflict is true. A nil
// base means the file did not exist (merged against an empty file).
func Merge(base, ours, theirs []byte, l Labels) (result []byte, conflict bool, err error) {
	dir, err := os.MkdirTemp("", "moreco-merge-")
	if err != nil {
		return nil, false, err
	}
	defer os.RemoveAll(dir)
	write := func(name string, data []byte) (string, error) {
		p := filepath.Join(dir, name)
		return p, os.WriteFile(p, data, 0o600)
	}
	po, err := write("ours", ours)
	if err != nil {
		return nil, false, err
	}
	pb, err := write("base", base)
	if err != nil {
		return nil, false, err
	}
	pt, err := write("theirs", theirs)
	if err != nil {
		return nil, false, err
	}

	out, err := gitx.RunRaw("", nil, "merge-file", "-p",
		"-L", l.Ours, "-L", l.Base, "-L", l.Theirs, po, pb, pt)
	switch code := gitx.ExitCode(err); {
	case err == nil:
		return out, false, nil
	case code > 0 && code < 128: // number of conflicts
		return out, true, nil
	default:
		return nil, false, fmt.Errorf("cannot merge: %w", err)
	}
}
