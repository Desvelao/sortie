// Package profile defines a profile: everything needed to apply the sorter to
// one use case (input and destination, tags, variable extraction rules,
// destination templates), stored as a self-contained, portable YAML file.
// Application-wide settings live in package config instead.
package profile

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"sortie/internal/destination"
	"sortie/internal/pathtpl"
	"sortie/internal/tags"
	"sortie/internal/vars"
)

// CurrentVersion is the profile file format version written by this program.
const CurrentVersion = 1

// DefaultTemplate is used when neither the tag nor the profile sets one.
const DefaultTemplate = "{tag}/{filename}"

// Conflict policies; they mirror the copy engine's names.
const (
	ConflictSkip    = "skip"
	ConflictRename  = "rename"
	ConflictReplace = "replace"
)

// Tag is a tag as written in a profile file. Ids and ordering are not stored:
// the order of the list is the order in the app and ids are assigned by the
// database (tags are identified by name within a profile).
type Tag struct {
	Name     string `yaml:"name"`
	Shortcut string `yaml:"shortcut,omitempty"`
	// Destination is the folder {tag} expands to; defaults to Name.
	Destination string `yaml:"destination,omitempty"`
	// DestinationTemplate overrides the profile's template for this tag.
	DestinationTemplate string `yaml:"destination_template,omitempty"`
	// Enabled defaults to true when omitted.
	Enabled *bool `yaml:"enabled,omitempty"`
	// Color is an optional display color, "#RRGGBB" or "#RGB".
	Color string `yaml:"color,omitempty"`
}

// Profile is one use case.
type Profile struct {
	Version     int    `yaml:"version"`
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`

	InputDirectory string `yaml:"input_directory,omitempty"`
	Recursive      bool   `yaml:"recursive"`

	DestinationRoot     string `yaml:"destination_root,omitempty"`
	DestinationTemplate string `yaml:"destination_template,omitempty"`
	ConflictPolicy      string `yaml:"conflict_policy,omitempty"`

	Variables []vars.Definition `yaml:"variables,omitempty"`
	Tags      []Tag             `yaml:"tags"`
}

// New returns a minimal valid profile.
func New(name string) Profile {
	return Profile{Version: CurrentVersion, Name: name, Recursive: true, DestinationTemplate: DefaultTemplate, ConflictPolicy: ConflictSkip}
}

// Example returns a starter profile that sorts by year and tag; a template for
// "New profile".
func Example(name string) Profile {
	min, max := 1970, 2100
	p := New(name)
	p.DestinationTemplate = "{year}/{tag}/{filename}"
	p.Variables = []vars.Definition{{
		Name: "year",
		Sources: []vars.Source{
			{Kind: vars.KindManual},
			{Kind: vars.KindFilename, Pattern: `(^|[^0-9])(20[0-9]{2})([^0-9]|$)`, Group: 2},
			{Kind: vars.KindMetadata, Field: "year"},
		},
		Validate: vars.Validation{Min: &min, Max: &max},
	}}
	p.Tags = nil
	for i, t := range tags.Defaults() {
		p.Tags = append(p.Tags, Tag{Name: t.Name, Shortcut: fmt.Sprint(i + 1)})
	}
	return p
}

// ValidationError locates a problem inside a profile. Path is like
// "tags[2]" or "destination_template"; Load turns it into a line number.
type ValidationError struct {
	Path string
	Err  error
	Line int // 0 = unknown
}

func (e *ValidationError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("line %d: %s: %v", e.Line, e.Path, e.Err)
	}
	return fmt.Sprintf("%s: %v", e.Path, e.Err)
}
func (e *ValidationError) Unwrap() error { return e.Err }

func verr(path string, format string, a ...any) *ValidationError {
	return &ValidationError{Path: path, Err: fmt.Errorf(format, a...)}
}

// VarSet compiles the profile's variables.
func (p Profile) VarSet() (*vars.Set, error) { return vars.New(p.Variables) }

// TemplateFor returns the template that applies to a tag.
func (p Profile) TemplateFor(t Tag) string {
	if s := strings.TrimSpace(t.DestinationTemplate); s != "" {
		return s
	}
	if s := strings.TrimSpace(p.DestinationTemplate); s != "" {
		return s
	}
	return DefaultTemplate
}

// Validate checks the whole profile. The error is a *ValidationError.
func (p Profile) Validate() error {
	if p.Version < 1 || p.Version > CurrentVersion {
		if p.Version > CurrentVersion {
			return verr("version", "profile format version %d is newer than this program supports (%d)", p.Version, CurrentVersion)
		}
		return verr("version", "must be %d", CurrentVersion)
	}
	if strings.TrimSpace(p.Name) == "" {
		return verr("name", "is empty")
	}
	for _, f := range []struct{ path, v string }{{"input_directory", p.InputDirectory}, {"destination_root", p.DestinationRoot}} {
		if strings.ContainsRune(f.v, 0) {
			return verr(f.path, "contains an invalid character")
		}
	}
	switch p.ConflictPolicy {
	case "", ConflictSkip, ConflictRename, ConflictReplace:
	default:
		return verr("conflict_policy", "%q is not one of skip, rename, replace", p.ConflictPolicy)
	}

	set, err := vars.New(p.Variables)
	if err != nil {
		path := "variables"
		for i, d := range p.Variables { // point at the offending definition
			if _, e := vars.New([]vars.Definition{d}); e != nil {
				path = fmt.Sprintf("variables[%d]", i)
				break
			}
		}
		return &ValidationError{Path: path, Err: err}
	}
	names := set.Names()

	if t := strings.TrimSpace(p.DestinationTemplate); t != "" {
		if err := pathtpl.Validate(t, names); err != nil {
			return &ValidationError{Path: "destination_template", Err: err}
		}
	}
	if len(p.Tags) == 0 {
		return verr("tags", "at least one tag is required")
	}
	for i, t := range p.Tags {
		path := fmt.Sprintf("tags[%d]", i)
		if tpl := strings.TrimSpace(t.DestinationTemplate); tpl != "" {
			if err := pathtpl.Validate(tpl, names); err != nil {
				return &ValidationError{Path: path, Err: fmt.Errorf("tag %q destination_template: %w", t.Name, err)}
			}
		}
	}
	for i, t := range p.Tags {
		if strings.TrimSpace(t.Color) != "" {
			if _, err := tags.ParseColor(t.Color); err != nil {
				return &ValidationError{Path: fmt.Sprintf("tags[%d]", i), Err: err}
			}
		}
	}
	// names, shortcuts and destination folders: reuse the tag rules
	ts := p.ToTags(nil)
	for i := range ts {
		ts[i].ID = i + 1
	}
	if err := tags.Validate(ts); err != nil {
		path := "tags"
		for i := range ts { // find the first tag that fails on its own
			if e := tags.Validate([]tags.Tag{{ID: 1, Name: ts[i].Name, Shortcut: ts[i].Shortcut, Destination: ts[i].Destination, Enabled: true}}); e != nil {
				path = fmt.Sprintf("tags[%d]", i)
				break
			}
		}
		return &ValidationError{Path: path, Err: err}
	}
	// a profile whose every tag lacks a usable template would copy nowhere
	for i, t := range p.Tags {
		if err := pathtpl.Validate(p.TemplateFor(t), names); err != nil {
			return &ValidationError{Path: fmt.Sprintf("tags[%d]", i), Err: err}
		}
	}
	return nil
}

// ToTags converts to the tag model used by the app. ids maps tag name → id
// (as assigned by the database); tags missing from it get id 0.
func (p Profile) ToTags(ids map[string]int) []tags.Tag {
	out := make([]tags.Tag, len(p.Tags))
	for i, t := range p.Tags {
		name := strings.TrimSpace(t.Name)
		dest := strings.TrimSpace(t.Destination)
		if dest == "" {
			dest = name
		}
		sc, err := tags.NormalizeShortcut(t.Shortcut)
		if err != nil {
			sc = strings.ToUpper(strings.TrimSpace(t.Shortcut)) // Validate reports it
		}
		out[i] = tags.Tag{
			ID: ids[name], Name: name, Shortcut: sc, Destination: dest,
			DestinationTemplate: strings.TrimSpace(t.DestinationTemplate),
			Enabled:             t.Enabled == nil || *t.Enabled, SortOrder: i,
			Color: strings.TrimSpace(t.Color),
		}
	}
	return out
}

// Resolved returns a copy with "~" and relative paths in input_directory and
// destination_root expanded: "~" to the home directory, relative paths
// against baseDir (normally the folder holding the profile file).
func (p Profile) Resolved(baseDir string) Profile {
	p.InputDirectory = expandPath(p.InputDirectory, baseDir)
	p.DestinationRoot = expandPath(p.DestinationRoot, baseDir)
	return p
}

func expandPath(s, base string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if s == "~" || strings.HasPrefix(s, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			s = filepath.Join(h, strings.TrimPrefix(s, "~"))
		}
	}
	if !filepath.IsAbs(s) && base != "" {
		s = filepath.Join(base, s)
	}
	return filepath.Clean(s)
}

// ValidateResolved additionally checks the paths after Resolved.
func (p Profile) ValidateResolved() error {
	if err := destination.ValidateRoot(p.DestinationRoot); err != nil {
		return &ValidationError{Path: "destination_root", Err: err}
	}
	return nil
}

// Parse decodes and validates YAML. Unknown keys, wrong types and invalid
// content are errors that carry a line number where one is known.
func Parse(data []byte) (Profile, error) {
	var p Profile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil {
		if errors.Is(err, errEOF) {
			return Profile{}, errors.New("the profile file is empty")
		}
		return Profile{}, fmt.Errorf("invalid profile: %w", cleanYAMLError(err))
	}
	if err := p.Validate(); err != nil {
		var ve *ValidationError
		if errors.As(err, &ve) {
			var root yaml.Node
			if yaml.Unmarshal(data, &root) == nil {
				ve.Line = lineOf(&root, ve.Path)
			}
		}
		return Profile{}, err
	}
	return p, nil
}

// Load reads and parses a profile file.
func Load(path string) (Profile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Profile{}, err
	}
	p, err := Parse(b)
	if err != nil {
		return Profile{}, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return p, nil
}

// Marshal renders a profile as YAML (2-space indent).
func Marshal(p Profile) ([]byte, error) {
	if p.Version == 0 {
		p.Version = CurrentVersion
	}
	return marshalAny(p)
}

func marshalAny(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Save validates and writes a profile atomically (temp file + rename). When
// the file already exists, comments written in it are carried over to the new
// content (matched by key, and tags/variables by name), so changes made in
// the app do not wipe hand-written notes. Formatting is normalised.
func Save(path string, p Profile) error {
	if p.Version == 0 {
		p.Version = CurrentVersion
	}
	if err := p.Validate(); err != nil {
		return err
	}
	b, err := Marshal(p)
	if err != nil {
		return err
	}
	if old, err := os.ReadFile(path); err == nil {
		if merged, ok := carryComments(old, b); ok {
			b = merged
		}
	}
	return writeAtomic(path, b)
}

func writeAtomic(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".profile-*.tmp")
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
	// profiles are meant to be copied and shared: keep the mode of the file
	// being replaced, otherwise use the usual 0644 (CreateTemp makes 0600)
	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// DefaultDir returns ~/.config/sortie/profiles.
func DefaultDir() (string, error) {
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "sortie", "profiles"), nil
}

// Entry is one profile file found in a profiles directory.
type Entry struct {
	ID      string // file name without extension; also the database key
	Path    string
	Name    string // display name from the file; ID if the file is invalid
	Profile Profile
	Err     error // non-nil if the file could not be loaded
}

// List returns the *.yaml / *.yml files of dir sorted by display name.
// A missing directory is an empty list. Invalid files are listed with Err set
// so the UI can show and fix them instead of hiding them.
func List(dir string) ([]Entry, error) {
	des, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Entry
	seen := map[string]string{} // id -> file name that owns it
	for _, de := range des {
		ext := strings.ToLower(filepath.Ext(de.Name()))
		if de.IsDir() || strings.HasPrefix(de.Name(), ".") || (ext != ".yaml" && ext != ".yml") {
			continue
		}
		e := Entry{ID: strings.TrimSuffix(de.Name(), filepath.Ext(de.Name())), Path: filepath.Join(dir, de.Name())}
		if owner, dup := seen[e.ID]; dup {
			// fileFor/Activate use the first file (a.yaml before a.yml); this
			// one could never be selected, so show it as broken instead.
			e.Err = fmt.Errorf("another file (%s) already provides the profile id %q; remove or rename one of them", owner, e.ID)
		} else {
			seen[e.ID] = de.Name()
			e.Profile, e.Err = Load(e.Path)
		}
		e.Name = e.ID
		if e.Err == nil {
			e.Name = e.Profile.Name
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

// PathFor returns the .yaml file path for a profile id inside dir.
func PathFor(dir, id string) string { return filepath.Join(dir, id+".yaml") }

// ResolvePath returns the file of an existing profile (.yaml or .yml), or the
// .yaml path where a new one would be created. Use it to save a profile back
// to the file it was loaded from.
func ResolvePath(dir, id string) string {
	if p, err := fileFor(dir, id); err == nil {
		return p
	}
	return PathFor(dir, id)
}

// ValidID reports whether id is usable as a profile file name.
func ValidID(id string) error {
	if strings.TrimSpace(id) == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\\x00") || strings.HasPrefix(id, ".") {
		return fmt.Errorf("%q is not a valid profile name", id)
	}
	return nil
}

// FromTags converts app tags to their file form: the destination folder is
// omitted when it equals the name, enabled only when false.
func FromTags(ts []tags.Tag) []Tag {
	out := make([]Tag, len(ts))
	for i, t := range ts {
		pt := Tag{Name: t.Name, Shortcut: t.Shortcut, DestinationTemplate: t.DestinationTemplate, Color: t.Color}
		if t.Destination != t.Name {
			pt.Destination = t.Destination
		}
		if !t.Enabled {
			f := false
			pt.Enabled = &f
		}
		out[i] = pt
	}
	return out
}
