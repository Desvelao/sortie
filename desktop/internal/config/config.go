// Package config manages the application-wide JSON configuration: logging,
// view preferences, global shortcuts and which profile is active. Everything
// that belongs to a use case (input and destination, tags, variable
// extraction) lives in a profile file, see package profile.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"sortie/internal/logging"
	"sortie/internal/profile"
	"sortie/internal/shortcuts"
)

// Config holds application-wide settings.
type Config struct {
	// ActiveProfile is the id (file name without extension) of the profile in
	// ProfilesDir that is in use. Empty means "default".
	ActiveProfile string `json:"active_profile,omitempty"`
	// ProfilesDir overrides the profiles folder (default profile.DefaultDir).
	ProfilesDir string `json:"profiles_dir,omitempty"`
	// Editor overrides $VISUAL/$EDITOR for "open profile in editor".
	Editor string `json:"editor,omitempty"`

	// Shortcuts maps action id → binding text (see package shortcuts);
	// actions missing here use their defaults.
	Shortcuts        map[string]string `json:"shortcuts"`
	LogLevel         string            `json:"log_level"`
	ShowFilmstrip    bool              `json:"show_filmstrip"`
	ThumbnailCacheMB int               `json:"thumbnail_cache_mb"`

	unknown []string // keys in the file that this version does not know (sorted)
}

// UnknownKeys returns the top-level keys of the loaded file that this version
// does not recognise (typos, or keys of another version). Never fatal.
func (c Config) UnknownKeys() []string { return c.unknown }

func knownKeys() map[string]bool {
	m := map[string]bool{}
	t := reflect.TypeOf(Config{})
	for i := 0; i < t.NumField(); i++ {
		if name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ","); name != "" && name != "-" {
			m[name] = true
		}
	}
	return m
}

// Default returns the default configuration.
func Default() Config {
	return Config{Shortcuts: shortcuts.Defaults(), LogLevel: "info", ShowFilmstrip: true, ThumbnailCacheMB: 200}
}

// Validate checks the configuration.
func (c Config) Validate() error {
	if err := shortcuts.Validate(c.Shortcuts, nil); err != nil {
		return fmt.Errorf("shortcut settings: %w", err)
	}
	if c.ActiveProfile != "" {
		if err := profile.ValidID(c.ActiveProfile); err != nil {
			return fmt.Errorf("active profile: %w", err)
		}
	}
	if strings.ContainsRune(c.ProfilesDir, 0) || strings.ContainsRune(c.Editor, 0) {
		return fmt.Errorf("profiles_dir/editor contain an invalid character")
	}
	if _, err := logging.ParseLevel(c.LogLevel); err != nil {
		return err
	}
	if c.ThumbnailCacheMB < 10 || c.ThumbnailCacheMB > 10000 {
		return fmt.Errorf("thumbnail cache size must be between 10 and 10000 MB")
	}
	return nil
}

// DefaultPath returns ~/.config/sortie/config.json.
func DefaultPath() (string, error) {
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "sortie", "config.json"), nil
}

// Load reads the config; a missing file yields defaults.
func Load(path string) (Config, error) {
	c := Default()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return Default(), err
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(b, &raw) == nil {
		known := knownKeys()
		for k := range raw {
			if !known[k] {
				c.unknown = append(c.unknown, k)
			}
		}
		sort.Strings(c.unknown)
	}
	if err := c.Validate(); err != nil {
		return Default(), err
	}
	return c, nil
}

// Save writes the config atomically (temp file + rename).
func Save(path string, c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil { // otherwise a crash can leave an empty file after the rename
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// ProfilesPath returns the profiles folder: ProfilesDir or the default.
func (c Config) ProfilesPath() (string, error) {
	if strings.TrimSpace(c.ProfilesDir) != "" {
		return c.ProfilesDir, nil
	}
	return profile.DefaultDir()
}

// ActiveProfileID returns ActiveProfile, defaulting to profile.DefaultID.
func (c Config) ActiveProfileID() string {
	if c.ActiveProfile != "" {
		return c.ActiveProfile
	}
	return profile.DefaultID
}

// BackupInvalid copies an unreadable or invalid config file to
// <path>.invalid.bak before defaults replace it. A backup with identical
// content is reused; different content gets <path>.invalid.bak.1, .2, ... so an
// earlier backup is never overwritten. It returns "" when path does not exist.
func BackupInvalid(path string) (string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	for i := 0; ; i++ {
		dst := path + ".invalid.bak"
		if i > 0 {
			dst = fmt.Sprintf("%s.%d", dst, i)
		}
		old, err := os.ReadFile(dst)
		if errors.Is(err, os.ErrNotExist) {
			return dst, os.WriteFile(dst, b, 0o600)
		}
		if err != nil {
			return "", err
		}
		if string(old) == string(b) {
			return dst, nil
		}
	}
}
