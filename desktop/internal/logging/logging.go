// Package logging sets up structured logging to a size-limited log file
// (and stderr). The log never contains file contents.
package logging

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const maxLogBytes = 2 << 20 // rotate at 2 MiB
const keepBackups = 2

// DefaultPath returns $XDG_STATE_HOME/sortie/sortie.log
// (~/.local/state/sortie/sortie.log).
func DefaultPath() (string, error) {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "sortie", "sortie.log"), nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".local", "state", "sortie", "sortie.log"), nil
}

// ParseLevel accepts debug, info, warn(ing) and error (case-insensitive).
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, nil
	case "", "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("unknown log level %q (use debug, info, warn or error)", s)
}

// Setup installs a default slog logger writing to path (rotated) and to
// stderr. The returned closer flushes and closes the file.
func Setup(path, level string) (io.Closer, error) {
	// An invalid level must not leave callers with a nil closer or no
	// logging: fall back to info and report the problem.
	lv, levelErr := ParseLevel(level)
	if levelErr != nil {
		lv = slog.LevelInfo
		levelErr = fmt.Errorf("%w; using info", levelErr)
	}
	rf, err := openRotating(path, maxLogBytes)
	if err != nil {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lv})))
		return nopCloser{}, errors.Join(levelErr, fmt.Errorf("log file unavailable, logging to stderr only: %w", err))
	}
	h := slog.NewTextHandler(io.MultiWriter(rf, os.Stderr), &slog.HandlerOptions{Level: lv})
	slog.SetDefault(slog.New(h))
	return rf, levelErr
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

// rotatingFile appends to path and rotates it to path.1, path.2 ... when it
// grows beyond max bytes.
type rotatingFile struct {
	mu   sync.Mutex
	path string
	max  int64
	f    *os.File
	size int64

	closed bool // Close was called: never reopen
	warned bool // the "log file lost" warning was already printed
}

func openRotating(path string, max int64) (*rotatingFile, error) {
	// Logs contain full file paths: keep them private to the user.
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	_ = os.Chmod(dir, 0o700) // keep the directory private
	r := &rotatingFile{path: path, max: max}
	return r, r.open()
}

func (r *rotatingFile) open() error {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	_ = f.Chmod(0o600) // keep the log private
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	r.f, r.size = f, st.Size()
	return nil
}

func (r *rotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		// A failed reopen after rotation (or a transient error) must not end file
		// logging for the session: retry on every write until it works.
		if r.closed || r.open() != nil {
			return len(p), nil // never fail the application because of logging
		}
		r.warned = false
	}
	if r.size+int64(len(p)) > r.max {
		r.rotate()
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	if err != nil {
		return len(p), nil
	}
	return n, nil
}

func (r *rotatingFile) rotate() {
	r.f.Close()
	for i := keepBackups; i >= 1; i-- {
		from := r.path
		if i > 1 {
			from = fmt.Sprintf("%s.%d", r.path, i-1)
		}
		os.Rename(from, fmt.Sprintf("%s.%d", r.path, i))
	}
	if err := r.open(); err != nil {
		r.f = nil
		if !r.warned {
			r.warned = true
			fmt.Fprintf(os.Stderr, "sortie: log file %s unavailable after rotation, will retry: %v\n", r.path, err)
		}
	}
}

func (r *rotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}
