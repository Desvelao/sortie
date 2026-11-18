package config

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRoundTripAndDefaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "config.json")
	c, err := Load(p)
	if err != nil || c.ActiveProfileID() != "default" {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	c.ActiveProfile, c.Editor, c.LogLevel = "work", "nvim", "debug"
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil || got.ActiveProfile != "work" || got.Editor != "nvim" || got.LogLevel != "debug" {
		t.Fatalf("%+v %v", got, err)
	}
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Fatalf("temp files left: %v", entries)
	}
}

func TestCorruptConfig(t *testing.T) {
	if _, err := Load(write(t, "{nope")); err == nil {
		t.Fatal("expected error")
	}
}

func TestGenericSettings(t *testing.T) {
	c := Default()
	if !c.ShowFilmstrip || c.LogLevel != "info" || c.ThumbnailCacheMB != 200 || c.Shortcuts["next"] != "Right" {
		t.Fatalf("%+v", c)
	}
	// a partial shortcut map keeps defaults for the other actions
	got, err := Load(write(t, `{"shortcuts":{"next":"N","prev":"B"},"log_level":"debug"}`))
	if err != nil || got.Shortcuts["next"] != "N" || got.Shortcuts["first"] != "Home" || got.LogLevel != "debug" || !got.ShowFilmstrip {
		t.Fatalf("%+v %v", got, err)
	}
	p := filepath.Join(t.TempDir(), "c.json")
	for name, mod := range map[string]func(*Config){
		"dup shortcut":    func(c *Config) { c.Shortcuts["next"] = "Left" },
		"bad level":       func(c *Config) { c.LogLevel = "loud" },
		"cache too small": func(c *Config) { c.ThumbnailCacheMB = 1 },
		"unknown action":  func(c *Config) { c.Shortcuts["warp"] = "F1" },
		"bad profile id":  func(c *Config) { c.ActiveProfile = "../x" },
	} {
		c := Default()
		mod(&c)
		if Save(p, c) == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestBackupInvalidKeepsBrokenFile(t *testing.T) {
	p := write(t, "{nope")
	bak, err := BackupInvalid(p)
	if err != nil || bak != p+".invalid.bak" {
		t.Fatalf("bak=%q err=%v", bak, err)
	}
	if b, _ := os.ReadFile(bak); string(b) != "{nope" {
		t.Fatalf("backup content %q", b)
	}
	// Same content again: reuse the backup. Different content: new backup.
	if again, _ := BackupInvalid(p); again != bak {
		t.Fatalf("identical content should reuse %q, got %q", bak, again)
	}
	os.WriteFile(p, []byte("{other"), 0o644)
	second, err := BackupInvalid(p)
	if err != nil || second != p+".invalid.bak.1" {
		t.Fatalf("second=%q err=%v", second, err)
	}
	if b, _ := os.ReadFile(bak); string(b) != "{nope" {
		t.Fatal("first backup was overwritten")
	}
	if none, err := BackupInvalid(p + ".missing"); none != "" || err != nil {
		t.Fatalf("missing file: %q %v", none, err)
	}
}

func TestSaveIsAtomicAndPrivate(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	c := Default()
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(p); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("%v %v", st, err)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(p), ".config-*.tmp")); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
	if got, err := Load(p); err != nil {
		t.Fatalf("%+v %v", got, err)
	}
	bad := Default()
	bad.LogLevel = "nonsense"
	if err := Save(p, bad); err == nil {
		t.Fatal("invalid config saved")
	}
	if got, err := Load(p); err != nil || got.LogLevel != "info" {
		t.Fatal("a failed Save must leave the old file intact")
	}
}

func TestUnknownKeysAreReportedNotFatal(t *testing.T) {
	c, err := Load(write(t, `{"log_levl":"debug","log_level":"warn","active_profile":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.UnknownKeys(); len(got) != 1 || got[0] != "log_levl" {
		t.Errorf("unknown keys = %v, want [log_levl]", got)
	}
	if c.LogLevel != "warn" {
		t.Errorf("known keys must still load: %+v", c)
	}
	if c, _ := Load(write(t, `{"log_level":"info"}`)); len(c.UnknownKeys()) != 0 {
		t.Errorf("unexpected unknown keys %v", c.UnknownKeys())
	}
}
