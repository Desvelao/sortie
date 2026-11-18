package database

import (
	"time"

	"sortie/internal/tags"
)

// ToggleTag assigns the tag if absent, removes it if present; returns the new state.
func (d *DB) ToggleTag(fileID int64, tagID int) (bool, error) {
	tx, err := d.sql.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	r, err := tx.Exec(`DELETE FROM file_tags WHERE file_id=? AND tag_id=?`, fileID, tagID)
	if err != nil {
		return false, err
	}
	n, _ := r.RowsAffected()
	assigned := n == 0
	if assigned {
		if _, err := tx.Exec(`INSERT INTO file_tags(file_id, tag_id, assigned_at) VALUES(?,?,?)`,
			fileID, tagID, time.Now().UTC().Format(time.RFC3339)); err != nil {
			return false, err
		}
	}
	return assigned, tx.Commit()
}

// DefaultProfile is the id of the profile created on first run.
const DefaultProfile = "default"

// SyncProfileTags makes the tags of one profile mirror ts, matching by name.
// Tags no longer listed are deleted with their assignments (files on disk are
// never touched; copy history keeps the tag name). It returns tag name → id;
// ids are database-assigned and stable while the name is unchanged, so
// renaming a tag in a profile file starts it over as a new tag.
func (d *DB) SyncProfileTags(profile string, ts []tags.Tag) (map[string]int, error) {
	tx, err := d.sql.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	existing := map[string]int{}
	rows, err := tx.Query(`SELECT id, name FROM tags WHERE profile=?`, profile)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int
		var n string
		if err := rows.Scan(&id, &n); err != nil {
			rows.Close()
			return nil, err
		}
		existing[n] = id
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	want := map[string]bool{}
	for _, t := range ts {
		want[t.Name] = true
	}
	for n, id := range existing {
		if want[n] {
			continue
		}
		if _, err := tx.Exec(`DELETE FROM file_tags WHERE tag_id=?`, id); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`DELETE FROM tags WHERE id=?`, id); err != nil {
			return nil, err
		}
		delete(existing, n)
	}
	// free UNIQUE shortcuts so tags can swap them
	if _, err := tx.Exec(`UPDATE tags SET shortcut=NULL WHERE profile=?`, profile); err != nil {
		return nil, err
	}
	ids := make(map[string]int, len(ts))
	for _, t := range ts {
		var sc any
		if t.Shortcut != "" {
			sc = t.Shortcut
		}
		en := 0
		if t.Enabled {
			en = 1
		}
		if id, ok := existing[t.Name]; ok {
			if _, err := tx.Exec(`UPDATE tags SET shortcut=?, destination=?, enabled=?, sort_order=? WHERE id=?`,
				sc, t.Destination, en, t.SortOrder, id); err != nil {
				return nil, err
			}
			ids[t.Name] = id
			continue
		}
		r, err := tx.Exec(`INSERT INTO tags(profile,name,shortcut,destination,enabled,sort_order) VALUES(?,?,?,?,?,?)`,
			profile, t.Name, sc, t.Destination, en, t.SortOrder)
		if err != nil {
			return nil, err
		}
		id, err := r.LastInsertId()
		if err != nil {
			return nil, err
		}
		ids[t.Name] = int(id)
	}
	return ids, tx.Commit()
}

// LoadProfileAssignments returns tag ids per file id for one profile.
func (d *DB) LoadProfileAssignments(profile string) (map[int64][]int, error) {
	rows, err := d.sql.Query(`SELECT ft.file_id, ft.tag_id FROM file_tags ft
		JOIN tags t ON t.id = ft.tag_id WHERE t.profile=?`, profile)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]int{}
	for rows.Next() {
		var f int64
		var t int
		if err := rows.Scan(&f, &t); err != nil {
			return nil, err
		}
		out[f] = append(out[f], t)
	}
	return out, rows.Err()
}

// CountTaggedFiles returns how many files have at least one tag of the
// profile assigned.
func (d *DB) CountTaggedFiles(profile string) (int, error) {
	var n int
	err := d.sql.QueryRow(`SELECT COUNT(DISTINCT ft.file_id) FROM file_tags ft
		JOIN tags t ON t.id = ft.tag_id WHERE t.profile=?`, profile).Scan(&n)
	return n, err
}

// DeleteProfile removes everything stored for a profile: its tags with their
// assignments and its manual variable values. Copy history is kept (it only
// loses the link to the removed tags); files on disk are never touched.
func (d *DB) DeleteProfile(profile string) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM file_tags WHERE tag_id IN (SELECT id FROM tags WHERE profile=?)`,
		`DELETE FROM tags WHERE profile=?`,
		`DELETE FROM file_vars WHERE profile=?`,
	} {
		if _, err := tx.Exec(q, profile); err != nil {
			return err
		}
	}
	return tx.Commit()
}
