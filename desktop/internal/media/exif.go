// Package media reads format-specific metadata (EXIF, PDF, audio, video).
// It is independent of the sorting engine and the GUI; every reader is
// best-effort and returns an error instead of failing the application.
package media

import (
	"fmt"
	"os"
	"time"

	"github.com/rwcarlsen/goexif/exif"
)

// EXIF holds the few EXIF values the application uses.
type EXIF struct {
	Taken       time.Time
	Orientation int // 1..8, 0 if unknown
	Camera      string
}

// ReadEXIF reads EXIF data from a JPEG or TIFF file.
func ReadEXIF(path string) (e EXIF, err error) {
	defer recoverTo(&err, "EXIF")
	f, err := os.Open(path)
	if err != nil {
		return e, err
	}
	defer f.Close()
	x, err := exif.Decode(f)
	if err != nil {
		return e, err
	}
	if t, err := x.DateTime(); err == nil {
		e.Taken = t
	}
	if tag, err := x.Get(exif.Orientation); err == nil {
		if o, err := tag.Int(0); err == nil && o >= 1 && o <= 8 {
			e.Orientation = o
		}
	}
	var mk, md string
	if tag, err := x.Get(exif.Make); err == nil {
		mk, _ = tag.StringVal()
	}
	if tag, err := x.Get(exif.Model); err == nil {
		md, _ = tag.StringVal()
	}
	e.Camera = trimJoin(mk, md)
	return e, nil
}

func trimJoin(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "" || a == b:
		return a
	case len(b) >= len(a) && b[:len(a)] == a:
		return b
	}
	return a + " " + b
}

// recoverTo converts a panic from a third-party parser into an error.
func recoverTo(err *error, what string) {
	if r := recover(); r != nil {
		*err = fmt.Errorf("%s parser failed: %v", what, r)
	}
}
