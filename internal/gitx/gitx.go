// Package gitx runs git commands.
package gitx

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Error is a failed git command.
type Error struct {
	Args     []string
	ExitCode int
	Stderr   string
}

func (e *Error) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = fmt.Sprintf("exit status %d", e.ExitCode)
	}
	return fmt.Sprintf("git %s: %s", strings.Join(e.Args, " "), msg)
}

// ExitCode returns the exit code of a failed git command, or -1.
func ExitCode(err error) int {
	var ge *Error
	if errors.As(err, &ge) {
		return ge.ExitCode
	}
	return -1
}

// Run runs git in dir and returns its stdout with surrounding whitespace
// trimmed.
func Run(dir string, args ...string) (string, error) {
	out, err := RunRaw(dir, nil, args...)
	return strings.TrimSpace(string(out)), err
}

// RunRaw runs git in dir with optional stdin and returns stdout untouched.
func RunRaw(dir string, stdin []byte, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return stdout.Bytes(), &Error{Args: args, ExitCode: ee.ExitCode(), Stderr: stderr.String()}
		}
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return stdout.Bytes(), nil
}
