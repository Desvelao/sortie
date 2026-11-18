package profile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Slug turns a display name into a file-name-safe id: lower-case letters and
// digits, everything else a single dash; "profile" if nothing is left.
func Slug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	if s := strings.Trim(b.String(), "-"); s != "" {
		return s
	}
	return "profile"
}

func exists(dir, id string) bool {
	for _, ext := range []string{".yaml", ".yml"} {
		if _, err := os.Stat(filepath.Join(dir, id+ext)); err == nil {
			return true
		}
	}
	return false
}

// UniqueID returns base, or base-2, base-3 ... the first id that has no file
// in dir yet.
func UniqueID(dir, base string) string {
	id := base
	for n := 2; exists(dir, id); n++ {
		id = fmt.Sprintf("%s-%d", base, n)
	}
	return id
}

// Create writes a new profile (the starter Example) named name and returns
// its id.
func Create(dir, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("the profile name is empty")
	}
	id := UniqueID(dir, Slug(name))
	return id, Save(PathFor(dir, id), Example(name))
}

// Duplicate copies profile fromID under a new name and returns the new id.
// The copy is byte-for-byte except for its name, so comments are kept.
func Duplicate(dir, fromID, newName string) (string, error) {
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return "", errors.New("the profile name is empty")
	}
	src, err := fileFor(dir, fromID)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	id := UniqueID(dir, Slug(newName))
	dst := PathFor(dir, id)
	if err := writeAtomic(dst, b); err != nil {
		return "", err
	}
	if err := Rename(dir, id, newName); err != nil {
		os.Remove(dst)
		return "", err
	}
	return id, nil
}

// fileFor finds the file of an id (.yaml or .yml).
func fileFor(dir, id string) (string, error) {
	if err := ValidID(id); err != nil {
		return "", err
	}
	for _, ext := range []string{".yaml", ".yml"} {
		p := filepath.Join(dir, id+ext)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("profile %q not found in %s", id, dir)
}

// Rename changes the display name of a profile. The id (file name) stays, as
// it keys the profile's tagging in the database; comments in the file are
// kept.
func Rename(dir, id, newName string) error {
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return errors.New("the profile name is empty")
	}
	path, err := fileFor(dir, id)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(path), cleanYAMLError(err))
	}
	root := &doc
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = root.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("%s is not a profile", filepath.Base(path))
	}
	found := false
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "name" {
			root.Content[i+1].Kind, root.Content[i+1].Tag, root.Content[i+1].Value = yaml.ScalarNode, "!!str", newName
			root.Content[i+1].Style = 0
			found = true
		}
	}
	if !found {
		return errors.New("the profile has no name field")
	}
	out, err := encodeNode(&doc)
	if err != nil {
		return err
	}
	if _, err := Parse(out); err != nil {
		return fmt.Errorf("renaming would produce an invalid profile: %w", err)
	}
	return writeAtomic(path, out)
}

// Delete moves a profile file into the hidden folder <dir>/.trash so it can
// be restored by hand; it returns the new location.
func Delete(dir, id string) (string, error) {
	path, err := fileFor(dir, id)
	if err != nil {
		return "", err
	}
	trash := filepath.Join(dir, ".trash")
	if err := os.MkdirAll(trash, 0o755); err != nil {
		return "", err
	}
	stamp := time.Now().Format("20060102-150405")
	dst := filepath.Join(trash, fmt.Sprintf("%s-%s%s", id, stamp, filepath.Ext(path)))
	// The timestamp has one-second resolution and os.Rename replaces an
	// existing file: never overwrite an earlier trashed profile.
	for n := 2; ; n++ {
		if _, err := os.Lstat(dst); errors.Is(err, os.ErrNotExist) {
			break
		}
		dst = filepath.Join(trash, fmt.Sprintf("%s-%s-%d%s", id, stamp, n, filepath.Ext(path)))
	}
	return dst, os.Rename(path, dst)
}

// Import validates the profile file at src and copies it into dir under a
// free id derived from its file name; existing profiles are never replaced.
func Import(dir, src string) (string, error) {
	b, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	if _, err := Parse(b); err != nil {
		return "", fmt.Errorf("%s: %w", filepath.Base(src), err)
	}
	stem := strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
	id := UniqueID(dir, Slug(stem))
	return id, writeAtomic(PathFor(dir, id), b)
}

// Export copies the profile file to dst (overwriting dst).
func Export(dir, id, dst string) error {
	src, err := fileFor(dir, id)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return writeAtomic(dst, b)
}

// encodeNode renders a node tree with the program's 2-space indentation.
func encodeNode(n *yaml.Node) ([]byte, error) { return marshalAny(n) }
