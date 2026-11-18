package logging

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	for in, want := range map[string]slog.Level{"": slog.LevelInfo, "INFO": slog.LevelInfo, "debug": slog.LevelDebug, "Warning": slog.LevelWarn, "error": slog.LevelError} {
		if got, err := ParseLevel(in); err != nil || got != want {
			t.Errorf("%q: %v %v", in, got, err)
		}
	}
	if _, err := ParseLevel("loud"); err == nil {
		t.Error("expected error")
	}
}

func TestSetupWritesAndFiltersByLevel(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "x.log")
	c, err := Setup(p, "warn")
	if err != nil {
		t.Fatal(err)
	}
	slog.Info("not logged")
	slog.Warn("logged line", "path", "/tmp/ü.txt")
	c.Close()
	b, _ := os.ReadFile(p)
	if strings.Contains(string(b), "not logged") || !strings.Contains(string(b), "logged line") || !strings.Contains(string(b), "ü.txt") {
		t.Fatalf("%s", b)
	}
	if _, err := Setup(p, "nonsense"); err == nil {
		t.Fatal("bad level accepted")
	}
}

func TestRotation(t *testing.T) {
	p := filepath.Join(t.TempDir(), "r.log")
	r, err := openRotating(p, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	line := strings.Repeat("x", 60) + "\n"
	for i := 0; i < 10; i++ {
		if _, err := r.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"r.log", "r.log.1", "r.log.2"} {
		st, err := os.Stat(filepath.Join(filepath.Dir(p), name))
		if err != nil || st.Size() > 200 {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := os.Stat(p + ".3"); err == nil {
		t.Fatal("more backups than configured")
	}
}

func TestUnwritableLogFileFallsBack(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file")
	os.WriteFile(f, nil, 0o644)
	c, err := Setup(filepath.Join(f, "sub", "x.log"), "info") // parent is a file
	if err == nil {
		t.Fatal("expected a warning error")
	}
	c.Close()
	slog.Info("still works") // must not panic
}

func TestSetupInvalidLevelFallsBackToInfo(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.log")
	c, err := Setup(p, "nonsense")
	if err == nil || !strings.Contains(err.Error(), "nonsense") {
		t.Fatalf("expected an error naming the level, got %v", err)
	}
	if c == nil {
		t.Fatal("closer must not be nil, callers defer Close")
	}
	slog.Info("still logged")
	c.Close()
	if b, _ := os.ReadFile(p); !strings.Contains(string(b), "still logged") {
		t.Fatalf("no logging after invalid level: %s", b)
	}
}

func TestLogPermissionsArePrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	os.MkdirAll(dir, 0o755)
	p := filepath.Join(dir, "x.log")
	os.WriteFile(p, []byte("old\n"), 0o644) // left by an older version
	r, err := openRotating(p, 10)
	if err != nil {
		t.Fatal(err)
	}
	r.Write([]byte("0123456789abcdef")) // pushes past max
	r.Write([]byte("more"))             // triggers a rotation
	r.Close()
	check := func(path string, want os.FileMode) {
		t.Helper()
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := st.Mode().Perm(); got != want {
			t.Errorf("%s: mode %o, want %o", filepath.Base(path), got, want)
		}
	}
	check(dir, 0o700)
	check(p, 0o600)
	check(p+".1", 0o600)
}

func TestWriterRecoversAfterFailedReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	r, err := openRotating(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	r.path = filepath.Join(dir, "missing", "app.log") // rotation cannot reopen the file
	r.Write([]byte("0123456789ab"))                   // rotates, reopen fails
	if r.f != nil {
		t.Fatal("expected the reopen to fail")
	}
	r.path = path
	if _, err := r.Write([]byte("back")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "back") {
		t.Errorf("logging did not resume, file has %q", b)
	}
	r.Close()
	r.Write([]byte("after close"))
	if r.f != nil {
		t.Error("a closed writer must not reopen")
	}
}
