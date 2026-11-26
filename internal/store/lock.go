package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

// Lock settings; variables so tests can shorten them.
var (
	lockTimeout = 30 * time.Second
	lockStale   = 10 * time.Minute
	lockPoll    = 100 * time.Millisecond
)

// Lock takes an exclusive lock on the store, so that concurrent moreco runs
// (for example post-checkout hooks of two new worktrees) do not interleave
// their sync/commit/push. It uses a lock file next to the clone, which works
// on every platform. A lock older than lockStale is considered abandoned.
func (s *Store) Lock() (unlock func(), err error) {
	file := s.Dir + ".lock"
	deadline := time.Now().Add(lockTimeout)
	for {
		f, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(f, "pid %d at %s\n", os.Getpid(), time.Now().Format(time.RFC3339))
			f.Close()
			return func() { _ = os.Remove(file) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		if fi, err := os.Stat(file); err == nil && time.Since(fi.ModTime()) > lockStale {
			_ = os.Remove(file)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("store is locked by another moreco process (remove %s if that is not the case)", file)
		}
		time.Sleep(lockPoll)
	}
}
