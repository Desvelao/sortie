package profile

import (
	"errors"
	"fmt"
	"os"
)

// DefaultID is the id of the profile written on first run. It is also the
// database profile that holds state before any profile was chosen.
const DefaultID = "default"

// EnsureDefault makes sure dir holds at least one profile. When it holds none
// it writes default.yaml with the starter Example. It never touches a folder
// that already has profile files (valid or not), and reports whether it wrote
// one.
func EnsureDefault(dir string) (bool, error) {
	es, err := List(dir)
	if err != nil {
		return false, err
	}
	if len(es) > 0 {
		return false, nil
	}
	path := PathFor(dir, DefaultID)
	if _, err := os.Stat(path); err == nil {
		return false, nil // exists but is not listed (should not happen); leave it
	}
	if err := Save(path, Example("Default")); err != nil {
		return false, err
	}
	return true, nil
}

// RecoveryID is the id of the in-memory starter profile used when no profile
// file can be loaded.
const RecoveryID = "recovery"

// Activate picks the profile to use: preferred if it loads, otherwise the
// first valid one in dir, otherwise an unsaved starter profile under
// RecoveryID. The returned warning says why the preferred one was not used
// and is nil when it was.
func Activate(dir, preferred string) (id string, p Profile, warn error) {
	es, err := List(dir)
	if err != nil {
		return RecoveryID, Example("Recovery"), err
	}
	for _, e := range es {
		if e.ID == preferred && e.Err == nil {
			return e.ID, e.Profile, nil
		}
	}
	for _, e := range es {
		if e.Err == nil {
			if preferred != "" {
				warn = fmt.Errorf("profile %q could not be used (%v); using %q", preferred, why(es, preferred), e.ID)
			}
			return e.ID, e.Profile, warn
		}
	}
	return RecoveryID, Example("Recovery"), fmt.Errorf("no valid profile in %s; using an unsaved starter profile (%v)", dir, why(es, preferred))
}

func why(es []Entry, id string) error {
	for _, e := range es {
		if e.ID == id {
			if e.Err != nil {
				return e.Err
			}
			return errors.New("unknown error")
		}
	}
	return errors.New("not found")
}
