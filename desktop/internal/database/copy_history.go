package database

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// History statuses. IN_PROGRESS rows left behind by a crash are converted to
// INTERRUPTED at the next start.
const (
	CopyInProgress  = "IN_PROGRESS"
	CopyInterrupted = "INTERRUPTED"
)

// HistoryEntry is one copy attempt.
type HistoryEntry struct {
	ID         int64
	FileID     int64
	TagID      int // 0 if the tag was removed
	TagName    string
	SourcePath string
	DestPath   string
	SourceHash string
	DestHash   string
	Status     string
	CopiedAt   time.Time
	ErrorMsg   string
}

// BeginCopy records the start of a copy attempt (status IN_PROGRESS). tagID 0
// (or less) means "no tag" and is stored as NULL, as HistoryEntry.TagID expects.
func (d *DB) BeginCopy(fileID int64, tagID int, tagName, src, dst string) (int64, error) {
	var tag any
	if tagID > 0 {
		tag = tagID
	}
	r, err := d.sql.Exec(`INSERT INTO copy_history(file_id, tag_id, tag_name, source_path, destination_path, status)
		VALUES(?,?,?,?,?,?)`, fileID, tag, tagName, src, dst, CopyInProgress)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

// NoteCopy stores a free-text note (e.g. the temporary file in use) on an entry.
func (d *DB) NoteCopy(id int64, note string) error {
	r, err := d.sql.Exec(`UPDATE copy_history SET error_message=? WHERE id=?`, note, id)
	return oneRow(r, err, "history entry", id)
}

// oneRow turns "no row matched" into an error, so a bad id cannot fail silently.
func oneRow(r sql.Result, err error, what string, id int64) error {
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n != 1 {
		return fmt.Errorf("%s %d not found", what, id)
	}
	return nil
}

// FinishCopy records the final outcome of an attempt.
func (d *DB) FinishCopy(id int64, status, srcHash, dstHash, errMsg string) error {
	r, err := d.sql.Exec(`UPDATE copy_history SET status=?, source_hash=?, destination_hash=?, error_message=?, copied_at=?
		WHERE id=?`, status, nullIfEmpty(srcHash), nullIfEmpty(dstHash), nullIfEmpty(errMsg),
		time.Now().UTC().Format(time.RFC3339), id)
	return oneRow(r, err, "history entry", id)
}

// MarkInterrupted converts IN_PROGRESS rows (a previous run was killed) to
// INTERRUPTED and returns how many there were. Leftover temporary files are
// reported in the entry's message but never deleted automatically.
func (d *DB) MarkInterrupted() (int, error) {
	r, err := d.sql.Exec(`UPDATE copy_history SET status=?,
		error_message='interrupted' || CASE WHEN error_message IS NULL OR error_message='' THEN '' ELSE ' (temporary file: ' || error_message || ')' END
		WHERE status=?`, CopyInterrupted, CopyInProgress)
	if err != nil {
		return 0, err
	}
	n, _ := r.RowsAffected()
	return int(n), nil
}

const tempMsgPrefix, tempMsgSuffix = "(temporary file: ", ")"

// InterruptedTempFiles returns the temporary file paths named by INTERRUPTED
// entries (see MarkInterrupted). The files may no longer exist.
func (d *DB) InterruptedTempFiles() ([]string, error) {
	rows, err := d.sql.Query(`SELECT error_message FROM copy_history WHERE status=? AND error_message LIKE '%(temporary file: %)'`, CopyInterrupted)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var msg string
		if err := rows.Scan(&msg); err != nil {
			return nil, err
		}
		if i := strings.Index(msg, tempMsgPrefix); i >= 0 && strings.HasSuffix(msg, tempMsgSuffix) {
			out = append(out, msg[i+len(tempMsgPrefix):len(msg)-len(tempMsgSuffix)])
		}
	}
	return out, rows.Err()
}

// History returns the copy attempts of a file, newest first.
func (d *DB) History(fileID int64) ([]HistoryEntry, error) {
	rows, err := d.sql.Query(`SELECT id, file_id, tag_id, tag_name, source_path, destination_path,
		source_hash, destination_hash, status, copied_at, error_message
		FROM copy_history WHERE file_id=? ORDER BY id DESC`, fileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HistoryEntry
	for rows.Next() {
		var e HistoryEntry
		var tag sql.NullInt64
		var tn, sh, dh, at, em sql.NullString
		if err := rows.Scan(&e.ID, &e.FileID, &tag, &tn, &e.SourcePath, &e.DestPath, &sh, &dh, &e.Status, &at, &em); err != nil {
			return nil, err
		}
		e.TagID, e.TagName, e.SourceHash, e.DestHash, e.ErrorMsg = int(tag.Int64), tn.String, sh.String, dh.String, em.String
		if at.Valid {
			e.CopiedAt, _ = time.Parse(time.RFC3339, at.String)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CountHistory returns the number of entries with the given status.
func (d *DB) CountHistory(status string) (int, error) {
	var n int
	err := d.sql.QueryRow(`SELECT COUNT(*) FROM copy_history WHERE status=?`, status).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("counting history: %w", err)
	}
	return n, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
