package database

import (
	"fmt"
	"strings"
)

// SetFileVar stores a manual value for variable name of one file in a
// profile. An empty (or blank) value clears it.
func (d *DB) SetFileVar(fileID int64, profile, name, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		_, err := d.sql.Exec(`DELETE FROM file_vars WHERE file_id=? AND profile=? AND name=?`, fileID, profile, name)
		return err
	}
	_, err := d.sql.Exec(`INSERT INTO file_vars(file_id, profile, name, value) VALUES(?,?,?,?)
		ON CONFLICT(file_id, profile, name) DO UPDATE SET value=excluded.value`, fileID, profile, name, value)
	if err != nil {
		return fmt.Errorf("file %d: %w", fileID, err) // e.g. foreign key: file not in the database
	}
	return nil
}

// LoadFileVars returns the manual values of a profile: file id → name → value.
func (d *DB) LoadFileVars(profile string) (map[int64]map[string]string, error) {
	rows, err := d.sql.Query(`SELECT file_id, name, value FROM file_vars WHERE profile=?`, profile)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]map[string]string{}
	for rows.Next() {
		var id int64
		var n, v string
		if err := rows.Scan(&id, &n, &v); err != nil {
			return nil, err
		}
		if out[id] == nil {
			out[id] = map[string]string{}
		}
		out[id][n] = v
	}
	return out, rows.Err()
}

// PruneFileVars removes manual values of a profile for variables that are no
// longer defined (names not in keep).
func (d *DB) PruneFileVars(profile string, keep []string) error {
	q := `DELETE FROM file_vars WHERE profile=?`
	args := []any{profile}
	if len(keep) > 0 {
		ph := strings.TrimSuffix(strings.Repeat("?,", len(keep)), ",")
		q += ` AND name NOT IN (` + ph + `)`
		for _, k := range keep {
			args = append(args, k)
		}
	}
	_, err := d.sql.Exec(q, args...)
	return err
}
