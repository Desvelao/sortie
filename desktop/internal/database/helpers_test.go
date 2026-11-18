package database

import "sortie/internal/tags"

// Test-only conveniences over the per-profile API, acting on DefaultProfile.

func (d *DB) SyncTags(ts []tags.Tag) error {
	_, err := d.SyncProfileTags(DefaultProfile, ts)
	return err
}

func (d *DB) LoadAssignments() (map[int64][]int, error) {
	return d.LoadProfileAssignments(DefaultProfile)
}
