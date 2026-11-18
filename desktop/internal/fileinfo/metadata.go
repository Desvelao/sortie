// Package fileinfo describes files independently of their format.
package fileinfo

import "time"

// FileInfo is the format-independent representation of a discovered file.
type FileInfo struct {
	ID       int64  // database identifier (0 = not persisted)
	Path     string // absolute source path
	Name     string // original filename
	Ext      string // lower-case extension including dot, may be empty
	MIMEType string // detected or inferred MIME type
	Size     int64
	ModTime  time.Time
}
