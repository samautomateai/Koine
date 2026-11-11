package tools

import (
	"path/filepath"
	"sync"
)

// The per-file mutation queue: tools that read-modify-write a file hold the lock for that file, so parallel edit and write calls on one file
// run one after the other (Pi's withFileMutationQueue). Different files do not wait for each other.
type queueEntry struct {
	mu   sync.Mutex
	refs int
}

var (
	queueMu sync.Mutex
	queues  = map[string]*queueEntry{}
)

// QueueKey canonicalises a path through symlinks, so aliases of one file share a queue. A file that does not exist yet keeps its absolute path.
func QueueKey(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}
	return abs
}

// WithFileMutation runs fn while holding the queue of path. Wrap the whole read-modify-write window, not just the write.
func WithFileMutation[T any](path string, fn func() (T, error)) (T, error) {
	key := QueueKey(path)
	queueMu.Lock()
	e := queues[key]
	if e == nil {
		e = &queueEntry{}
		queues[key] = e
	}
	e.refs++
	queueMu.Unlock()

	e.mu.Lock()
	defer func() {
		e.mu.Unlock()
		queueMu.Lock()
		if e.refs--; e.refs == 0 {
			delete(queues, key)
		}
		queueMu.Unlock()
	}()
	return fn()
}

// queuedFiles reports how many files have a queue entry (tests check that entries are released).
func queuedFiles() int {
	queueMu.Lock()
	defer queueMu.Unlock()
	return len(queues)
}
