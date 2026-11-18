package destination

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sync"
)

const hashChunk = 1 << 20

// hasher computes SHA-256 sums with bounded concurrency and a small cache
// keyed by (path, size, modification time), so unchanged files are hashed once.
type hasher struct {
	sem chan struct{}
	mu  sync.Mutex
	m   map[string]*hashEntry
}

type hashEntry struct {
	mu   sync.Mutex
	done bool
	sum  string
	err  error
}

func newHasher(concurrency int) *hasher {
	return &hasher{sem: make(chan struct{}, concurrency), m: map[string]*hashEntry{}}
}

// Sum returns the hex SHA-256 of the file. Cancellation is honoured between
// chunks and is never cached.
func (h *hasher) Sum(ctx context.Context, path string) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	key := fmt.Sprintf("%s|%d|%d", path, st.Size(), st.ModTime().UnixNano())
	h.mu.Lock()
	if len(h.m) > 2048 {
		h.m = map[string]*hashEntry{}
	}
	e := h.m[key]
	if e == nil {
		e = &hashEntry{}
		h.m[key] = e
	}
	h.mu.Unlock()

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.done {
		return e.sum, e.err
	}
	sum, err := h.compute(ctx, path)
	if ctx.Err() == nil {
		e.done, e.sum, e.err = true, sum, err
	}
	return sum, err
}

func (h *hasher) compute(ctx context.Context, path string) (string, error) {
	select {
	case h.sem <- struct{}{}:
		defer func() { <-h.sem }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sh := sha256.New()
	buf := make([]byte, hashChunk)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := f.Read(buf)
		sh.Write(buf[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(sh.Sum(nil)), nil
}
