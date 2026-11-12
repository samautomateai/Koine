package mcp

import (
	"os"
	"path/filepath"
	"sync"
)

// LogLimit is the size at which the server log rotates to a ".1" file.
const LogLimit = 5 << 20

// LogFile is the file the servers' stderr goes to: <agent dir>/mcp.log.
func LogFile(home string) string { return filepath.Join(filepath.Dir(UserFile(home)), "mcp.log") }

// Log is an append-only log file that rotates to <path>.1 once it grows past Limit (the older .1 file is replaced).
type Log struct {
	Path  string
	Limit int64

	mu   sync.Mutex
	f    *os.File
	size int64
}

// OpenLog opens the log, rotating first if it is already too big. The file is private to the user.
func OpenLog(path string) (*Log, error) {
	l := &Log{Path: path, Limit: LogLimit}
	if err := l.open(); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *Log) open() error {
	if err := os.MkdirAll(filepath.Dir(l.Path), 0o700); err != nil {
		return err
	}
	if info, err := os.Stat(l.Path); err == nil && info.Size() > l.Limit {
		_ = os.Rename(l.Path, l.Path+".1")
	}
	f, err := os.OpenFile(l.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	l.f, l.size = f, info.Size()
	return nil
}

// Write appends p, rotating when the file is over the limit.
func (l *Log) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return 0, os.ErrClosed
	}
	if l.size+int64(len(p)) > l.Limit && l.size > 0 {
		_ = l.f.Close()
		_ = os.Rename(l.Path, l.Path+".1")
		if err := l.open(); err != nil {
			return 0, err
		}
	}
	n, err := l.f.Write(p)
	l.size += int64(n)
	return n, err
}

// Close closes the file.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}
