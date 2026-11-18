package copier

import "sortie/internal/database"

// History records copy attempts. Implementations must be safe for concurrent
// use. A failing history never blocks a copy; the error is attached to the
// result instead.
type History interface {
	Begin(job Job, destPath string) (id int64, err error)
	Note(id int64, note string) error
	Finish(id int64, r Result) error
}

// DBHistory persists history in SQLite.
type DBHistory struct{ DB *database.DB }

func (h DBHistory) Begin(job Job, dest string) (int64, error) {
	return h.DB.BeginCopy(job.FileID, job.Tag.ID, job.Tag.Name, job.SourcePath, dest)
}

func (h DBHistory) Note(id int64, note string) error { return h.DB.NoteCopy(id, note) }

func (h DBHistory) Finish(id int64, r Result) error {
	return h.DB.FinishCopy(id, string(r.Status), r.SrcHash, r.DstHash, r.Detail)
}
