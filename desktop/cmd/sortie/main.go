package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"fyne.io/fyne/v2"
	fyneapp "fyne.io/fyne/v2/app"

	"sortie/internal/app"
	"sortie/internal/cache"
	"sortie/internal/config"
	"sortie/internal/database"
	"sortie/internal/logging"
	"sortie/internal/profile"
	"sortie/internal/ui"
)

// version is set at release time (-ldflags "-X main.version=…"); see ../VERSION.
var version = "dev"

func main() { os.Exit(run()) }

func run() int {
	showVersion := flag.Bool("version", false, "print version and exit")
	smoke := flag.Bool("smoke-test", false, "start the GUI, then exit after a short delay")
	selfTest := flag.Bool("self-test", false, "run a headless end-to-end check (SQLite, scan, tag, copy) in a temporary directory and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("sortie", version)
		return 0
	}

	if *selfTest {
		return runSelfTest()
	}

	cfgPath, err := config.DefaultPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config path:", err)
		return 1
	}
	cfg, cfgErr := config.Load(cfgPath) // returns defaults on error

	if logPath, err := logging.DefaultPath(); err == nil {
		closer, lerr := logging.Setup(logPath, cfg.LogLevel)
		defer closer.Close()
		if lerr != nil {
			slog.Warn("logging", "error", lerr)
		}
	}
	slog.Info("sortie starting", "version", version, "config", cfgPath)
	if cfgErr != nil {
		slog.Warn("ignoring unreadable or invalid config, using defaults", "error", cfgErr)
		// Defaults replace the file on the next settings save: keep a copy.
		if bak, berr := config.BackupInvalid(cfgPath); berr != nil {
			slog.Warn("could not back up the invalid config", "error", berr)
		} else if bak != "" {
			slog.Warn("the invalid config was saved as a backup", "backup", bak)
		}
	}

	dbPath, err := database.DefaultPath()
	if err != nil {
		slog.Error("database path", "error", err)
		return 1
	}
	db, err := database.Open(dbPath)
	if err != nil {
		slog.Error("cannot open database", "path", dbPath, "error", err)
		fmt.Fprintf(os.Stderr, "cannot open database %s: %v\n", dbPath, err)
		return 1
	}
	defer db.Close()

	core := app.NewWithDB(db)
	profDir := loadProfile(core, &cfg, cfgErr == nil)
	if n := core.InterruptedAtStart(); n > 0 {
		slog.Warn("copies were interrupted in a previous run", "count", n)
	}
	for _, p := range core.LeftoverTempFiles() {
		slog.Warn("leftover temporary file from an interrupted copy (safe to delete)", "path", p)
	}

	a := fyneapp.NewWithID("io.github.sortie")
	w := ui.NewMainWindow(a, core, cfg, cfgPath)
	w.SetProfileDir(profDir)
	if dir, err := cache.DefaultDir(); err == nil {
		thumbs := cache.NewThumbnails(dir, int64(cfg.ThumbnailCacheMB)<<20)
		w.SetThumbnailCache(thumbs)
		go func() { // keep the cache within its size limit
			if n, err := thumbs.Prune(); err != nil {
				slog.Debug("thumbnail cache prune failed", "error", err)
			} else if n > 0 {
				slog.Debug("thumbnail cache pruned", "removed", n)
			}
		}()
	}
	if *smoke {
		go func() {
			time.Sleep(1500 * time.Millisecond)
			fyne.Do(a.Quit)
		}()
	}
	w.Run()
	slog.Info("sortie stopped")
	return 0
}

// loadProfile makes the active profile effective and returns the profiles
// folder ("" if it cannot be determined, in which case profile edits are not
// saved). A first run writes the starter profiles/default.yaml. Nothing is
// overwritten and a broken profile never stops the app.
func loadProfile(core *app.Application, cfg *config.Config, cfgOK bool) string {
	dir, err := cfg.ProfilesPath()
	if err != nil {
		slog.Error("profiles folder", "error", err)
		return ""
	}
	if keys := cfg.UnknownKeys(); cfgOK && len(keys) > 0 {
		slog.Warn("config.json contains unknown keys (typo?); they are ignored and dropped on the next save", "keys", keys)
	}
	if _, err := profile.EnsureDefault(dir); err != nil {
		slog.Error("cannot create the default profile", "dir", dir, "error", err)
	}

	id, p, warn := profile.Activate(dir, cfg.ActiveProfileID())
	if warn != nil {
		slog.Warn("profile", "error", warn)
	}
	if err := core.ApplyProfile(id, p, dir); err != nil {
		slog.Error("profile cannot be applied, using the starter profile", "profile", id, "error", err)
		if err := core.ApplyProfile(profile.RecoveryID, profile.Example("Recovery"), dir); err != nil {
			slog.Error("starter profile cannot be applied", "error", err)
		}
	}
	return dir
}
