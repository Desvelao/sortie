package media

import (
	"strconv"
	"strings"
	"time"
)

// Fields returns normalized, lowercase-keyed metadata for a file so profiles
// can use any of it as a variable source. Keys that do not apply to the file
// type are absent. Common keys:
//
//	date      capture/creation date, RFC 3339 (EXIF, PDF, video); for audio
//	          files only the bare release year ("2020"), as tags carry no more
//	year      4-digit year of date
//	title, author, artist, album, genre, camera, subject, creator, producer
//	pages, duration (seconds), width, height, format
//
// An unreadable or unsupported file yields an empty map, never an error.
func Fields(path string) map[string]string {
	f := map[string]string{}
	set := func(k, v string) {
		if v = strings.TrimSpace(v); v != "" {
			f[k] = v
		}
	}
	setDate := func(t time.Time) {
		if !t.IsZero() && t.Year() > 1 {
			f["date"] = t.Format(time.RFC3339)
			f["year"] = strconv.Itoa(t.Year())
		}
	}
	switch strings.ToLower(extOf(path)) {
	case ".jpg", ".jpeg", ".tif", ".tiff":
		if e, err := ReadEXIF(path); err == nil {
			setDate(e.Taken)
			set("camera", e.Camera)
		}
	case ".pdf":
		if p, err := ReadPDF(path, false); err == nil {
			setDate(p.Created)
			set("title", p.Title)
			set("author", p.Author)
			set("subject", p.Subject)
			set("creator", p.Creator)
			set("producer", p.Producer)
			if p.Pages > 0 {
				f["pages"] = strconv.Itoa(p.Pages)
			}
		}
	case ".mp3", ".flac", ".ogg", ".oga", ".m4a", ".m4b", ".wav":
		if a, err := ReadAudio(path); err == nil {
			if a.Year > 0 {
				f["year"] = strconv.Itoa(a.Year)
				f["date"] = strconv.Itoa(a.Year)
			}
			set("title", a.Title)
			set("artist", a.Artist)
			set("author", a.Artist)
			set("album", a.Album)
			set("genre", a.Genre)
			set("format", a.Format)
			if a.Duration > 0 {
				f["duration"] = strconv.Itoa(int(a.Duration.Seconds()))
			}
		}
	case ".mp4", ".m4v", ".mov", ".mkv", ".webm", ".mka", ".3gp", ".avi":
		if v, err := ReadVideo(path); err == nil {
			if v.Created.Year() > 1970 {
				setDate(v.Created)
			}
			set("format", v.Container)
			if v.Duration > 0 {
				f["duration"] = strconv.Itoa(int(v.Duration.Seconds()))
			}
			if v.Width > 0 {
				f["width"], f["height"] = strconv.Itoa(v.Width), strconv.Itoa(v.Height)
			}
		}
	}
	return f
}

// FieldNames lists the metadata keys Fields may return, for UI hints and
// profile validation.
var FieldNames = []string{
	"date", "year", "title", "author", "artist", "album", "genre", "camera",
	"subject", "creator", "producer", "pages", "duration", "width", "height", "format",
}
