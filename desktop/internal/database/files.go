package database

import (
	"database/sql"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"sortie/internal/fileinfo"
)

// SyncResult summarises a rescan. Renamed files keep their id, tags and
// manual variable values (matched by size and modification time).
type SyncResult struct{ Added, Modified, Removed, Renamed int }

type fileRow struct {
	path   string
	id     int64
	size   sql.NullInt64
	mtime  sql.NullFloat64
	status string
}

// SyncFiles reconciles scanned files with the inventory in one transaction.
// Known files keep their id, manual variable values and tags; new files are inserted;
// files that disappeared are marked MISSING (never deleted, so classifications
// survive). files[i].ID is filled in.
func (d *DB) SyncFiles(dir string, files []fileinfo.FileInfo, recursive bool) (SyncResult, error) {
	var res SyncResult
	dir = filepath.Clean(dir)
	prefix := strings.TrimSuffix(dir, "/") + "/"
	tx, err := d.sql.Begin()
	if err != nil {
		return res, err
	}
	defer tx.Rollback()

	existing := map[string]fileRow{}
	rows, err := tx.Query(`SELECT id, source_path, file_size, modified_time, status
		FROM files WHERE substr(source_path,1,?)=?`, utf8.RuneCountInString(prefix), prefix)
	if err != nil {
		return res, err
	}
	for rows.Next() {
		var r fileRow
		var p string
		if err := rows.Scan(&r.id, &p, &r.size, &r.mtime, &r.status); err != nil {
			rows.Close()
			return res, err
		}
		r.path = p
		if recursive || filepath.Dir(p) == dir {
			existing[p] = r
		}
	}
	if err := rows.Close(); err != nil {
		return res, err
	}

	ins, err := tx.Prepare(`INSERT INTO files(source_path, filename, extension, mime_type, file_size, modified_time, status)
		VALUES(?,?,?,?,?,?,'PENDING')`)
	if err != nil {
		return res, err
	}
	defer ins.Close()
	upd, err := tx.Prepare(`UPDATE files SET filename=?, extension=?, mime_type=?, file_size=?, modified_time=?,
		status=CASE WHEN status='MISSING' THEN 'PENDING' ELSE status END WHERE id=?`)
	if err != nil {
		return res, err
	}
	defer upd.Close()

	seen := map[int64]bool{}
	var fresh []int // indices of files not known by path
	for i := range files {
		f := &files[i]
		mt := float64(f.ModTime.UnixNano()) / 1e9
		r, ok := existing[f.Path]
		if !ok {
			fresh = append(fresh, i)
			continue
		}
		f.ID = r.id
		seen[r.id] = true
		changed := !r.size.Valid || r.size.Int64 != f.Size || !r.mtime.Valid || math.Abs(r.mtime.Float64-mt) > 1e-3
		if changed || r.status == "MISSING" {
			if changed {
				res.Modified++
			}
			if _, err := upd.Exec(f.Name, f.Ext, f.MIMEType, f.Size, mt, r.id); err != nil {
				return res, err
			}
		}
	}

	// rows whose path is gone: candidates for rename detection
	var unseen []fileRow
	for _, r := range existing {
		if !seen[r.id] {
			unseen = append(unseen, r)
		}
	}
	key := func(size int64, mt float64) string { return fmt.Sprintf("%d|%d", size, int64(math.Round(mt*1000))) }
	goneByKey := map[string][]fileRow{}
	for _, r := range unseen {
		if r.size.Valid && r.size.Int64 > 0 && r.mtime.Valid {
			k := key(r.size.Int64, r.mtime.Float64)
			goneByKey[k] = append(goneByKey[k], r)
		}
	}
	freshByKey := map[string][]int{}
	for _, i := range fresh {
		f := files[i]
		if f.Size > 0 {
			k := key(f.Size, float64(f.ModTime.UnixNano())/1e9)
			freshByKey[k] = append(freshByKey[k], i)
		}
	}
	renamedRows := map[int64]bool{}
	renamedFresh := map[int]bool{}
	for k, gone := range goneByKey {
		if nf := freshByKey[k]; len(gone) == 1 && len(nf) == 1 {
			r, f := gone[0], &files[nf[0]]
			if _, err := tx.Exec(`UPDATE files SET source_path=?, filename=?, extension=?, mime_type=?, file_size=?, modified_time=?,
				status='PENDING' WHERE id=?`, f.Path, f.Name, f.Ext, f.MIMEType, f.Size, float64(f.ModTime.UnixNano())/1e9, r.id); err != nil {
				continue // e.g. path already taken: fall back to add + remove
			}
			f.ID = r.id
			renamedRows[r.id], renamedFresh[nf[0]] = true, true
			seen[r.id] = true
			res.Renamed++
		}
	}

	for _, i := range fresh {
		if renamedFresh[i] {
			continue
		}
		f := &files[i]
		r, err := ins.Exec(f.Path, f.Name, f.Ext, f.MIMEType, f.Size, float64(f.ModTime.UnixNano())/1e9)
		if err != nil {
			return res, fmt.Errorf("inserting %s: %w", f.Path, err)
		}
		if f.ID, err = r.LastInsertId(); err != nil {
			return res, err
		}
		res.Added++
	}
	for _, r := range unseen {
		if renamedRows[r.id] || r.status == "MISSING" {
			continue
		}
		if _, err := tx.Exec(`UPDATE files SET status='MISSING' WHERE id=?`, r.id); err != nil {
			return res, err
		}
		res.Removed++
	}
	return res, tx.Commit()
}

// PurgeMissing permanently removes inventory rows of files that are MISSING
// (no longer on disk) together with their tag assignments and variable
// values, and returns how many it removed. dir limits it to files below that
// directory; "" means everywhere. Rows with copy history are kept, because
// the history is a record of what was copied and must stay attached to its
// file. Files on disk are never touched.
func (d *DB) PurgeMissing(dir string) (int, error) {
	where := `status='MISSING' AND id NOT IN (SELECT file_id FROM copy_history)`
	var args []any
	if dir != "" {
		prefix := strings.TrimSuffix(filepath.Clean(dir), "/") + "/"
		where += ` AND substr(source_path,1,?)=?`
		args = append(args, utf8.RuneCountInString(prefix), prefix)
	}
	tx, err := d.sql.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	for _, tbl := range []string{"file_tags", "file_vars"} {
		if _, err := tx.Exec(`DELETE FROM `+tbl+` WHERE file_id IN (SELECT id FROM files WHERE `+where+`)`, args...); err != nil {
			return 0, err
		}
	}
	r, err := tx.Exec(`DELETE FROM files WHERE `+where, args...)
	if err != nil {
		return 0, err
	}
	n, _ := r.RowsAffected()
	return int(n), tx.Commit()
}

// SaveSession records the input directory and current file.
func (d *DB) SaveSession(dir string, fileID int64) error {
	var cur any
	if fileID > 0 {
		cur = fileID
	}
	_, err := d.sql.Exec(`INSERT INTO session(id, input_directory, current_file_id, last_opened)
		VALUES(1,?,?,datetime('now'))
		ON CONFLICT(id) DO UPDATE SET input_directory=excluded.input_directory,
		current_file_id=excluded.current_file_id, last_opened=excluded.last_opened`, dir, cur)
	return err
}

// LoadSession returns the stored session; ok=false if none.
func (d *DB) LoadSession() (dir string, fileID int64, ok bool, err error) {
	var cur sql.NullInt64
	err = d.sql.QueryRow(`SELECT input_directory, current_file_id FROM session WHERE id=1`).Scan(&dir, &cur)
	if err == sql.ErrNoRows {
		return "", 0, false, nil
	}
	return dir, cur.Int64, err == nil, err
}
