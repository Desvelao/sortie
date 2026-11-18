package scanner

import (
	"io/fs"
	"os"
)

// osFS lets us stat absolute paths through the fs.FS interface.
type osFS struct{}

func (osFS) Open(name string) (fs.File, error)     { return os.Open(name) }
func (osFS) Stat(name string) (fs.FileInfo, error) { return os.Stat(name) }
