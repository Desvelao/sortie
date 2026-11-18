package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sortie/internal/app"
	"sortie/internal/config"
	"sortie/internal/database"
	"sortie/internal/profile"
)

func startup(t *testing.T, dir string) (*app.Application, config.Config, string) {
	t.Helper()
	cfgPath := filepath.Join(dir, "config.json")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ProfilesDir = filepath.Join(dir, "profiles")
	db, err := database.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	core := app.NewWithDB(db)
	pd := loadProfile(core, &cfg, true)
	return core, cfg, pd
}

func TestStartupFreshInstallGetsStarterProfile(t *testing.T) {
	dir := t.TempDir()
	core, _, pd := startup(t, dir)
	if len(core.Tags()) != 9 || len(core.VarNames()) != 1 {
		t.Fatalf("%d tags, %v", len(core.Tags()), core.VarNames())
	}
	if _, err := os.Stat(profile.PathFor(pd, "default")); err != nil {
		t.Fatal(err)
	}
}

// A broken profile file must neither stop the app nor be modified.
func TestStartupWithBrokenProfile(t *testing.T) {
	dir := t.TempDir()
	pd := filepath.Join(dir, "profiles")
	os.MkdirAll(pd, 0o755)
	bad := filepath.Join(pd, "default.yaml")
	os.WriteFile(bad, []byte("version: 1\nname: X\nbogus: 1\ntags: [{name: A}]\n"), 0o644)
	core, _, _ := startup(t, dir)
	if core.ProfileID() != profile.RecoveryID || len(core.Tags()) == 0 {
		t.Fatalf("%s %d", core.ProfileID(), len(core.Tags()))
	}
	if b, _ := os.ReadFile(bad); !strings.Contains(string(b), "bogus") {
		t.Fatal("broken profile was modified")
	}
}

// runSelfTest is what the release scripts run on clean machines; without this
// test a regression in it would only show up at release time.
func TestSelfTestPasses(t *testing.T) {
	tmp := t.TempDir()
	for _, v := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME"} {
		t.Setenv(v, filepath.Join(tmp, v))
	}
	t.Setenv("HOME", tmp)
	prev := slog.Default()
	defer slog.SetDefault(prev)
	if code := runSelfTest(); code != 0 {
		t.Fatalf("runSelfTest returned %d", code)
	}
}
