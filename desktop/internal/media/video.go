package media

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// VideoInfo is container-level metadata.
type VideoInfo struct {
	Container     string
	Duration      time.Duration
	Width, Height int
	Created       time.Time
}

// ReadVideo parses MP4/MOV/M4A (ISO BMFF), Matroska/WebM and AVI containers.
func ReadVideo(path string) (v VideoInfo, err error) {
	defer recoverTo(&err, "video")
	f, err := os.Open(path)
	if err != nil {
		return v, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return v, err
	}
	switch strings.ToLower(extOf(path)) {
	case ".mp4", ".m4v", ".mov", ".m4a", ".m4b", ".3gp":
		return readISOBMFF(f, st.Size())
	case ".mkv", ".webm", ".mka":
		return readEBML(f, st.Size())
	case ".avi":
		return readAVI(f)
	}
	return v, fmt.Errorf("no metadata reader for this container")
}
