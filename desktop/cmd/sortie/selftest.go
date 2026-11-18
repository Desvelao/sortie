package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"sortie/internal/app"
	"sortie/internal/cache"
	"sortie/internal/config"
	"sortie/internal/copier"
	"sortie/internal/database"
	"sortie/internal/logging"
	"sortie/internal/preview"
	"sortie/internal/profile"
	"sortie/internal/scanner"
)

// runSelfTest exercises the whole non-GUI stack on a throw-away directory:
// bundled SQLite, scanning, tagging, destination checks, copying with
// verification, persistence across a "restart", previews and the thumbnail
// cache. It is used by the release scripts on clean machines, where it proves
// that nothing besides the executable is needed (no SQLite, Go or Python).
// It never touches the user's real configuration or data.
func runSelfTest() int {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil))) // keep the report readable
	var failures []string
	step := func(name string, err error) {
		if err != nil {
			failures = append(failures, name+": "+err.Error())
			fmt.Printf("FAIL  %s: %v\n", name, err)
			return
		}
		fmt.Printf("ok    %s\n", name)
	}

	// where real data would live, and that it is not next to the executable
	exe, _ := os.Executable()
	exeDir := filepath.Dir(exe)
	for name, f := range map[string]func() (string, error){
		"config": config.DefaultPath, "database": database.DefaultPath,
		"log": logging.DefaultPath, "cache": cache.DefaultDir,
	} {
		p, err := f()
		if err == nil && (p == exeDir || strings.HasPrefix(p, exeDir+string(filepath.Separator))) {
			err = fmt.Errorf("%s path %s is inside the executable directory", name, p)
		}
		step("data path outside executable ("+name+")", err)
		if err == nil {
			fmt.Printf("      %s: %s\n", name, p)
		}
	}

	tmp, err := os.MkdirTemp("", "sortie-selftest-*")
	if err != nil {
		fmt.Println("FAIL  cannot create temporary directory:", err)
		return 1
	}
	defer os.RemoveAll(tmp)
	in, out := filepath.Join(tmp, "in"), filepath.Join(tmp, "Sorted")
	for _, d := range []string{in, out} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			fmt.Println("FAIL", err)
			return 1
		}
	}
	var pngBuf strings.Builder
	_ = png.Encode(&pngBuilder{&pngBuf}, image.NewRGBA(image.Rect(0, 0, 32, 32)))
	files := map[string]string{
		"Concert_2019.mp3":  "not really audio",
		"ñandú_2018 日本.txt": "unicode name\n",
		"no_year_file.bin":  "\x00\x01\x02",
		"Photo_2020.png":    pngBuf.String(),
	}
	for n, c := range files {
		if err := os.WriteFile(filepath.Join(in, n), []byte(c), 0o644); err != nil {
			fmt.Println("FAIL", err)
			return 1
		}
	}

	dbPath := filepath.Join(tmp, "data", "database.sqlite")
	openCore := func() (*app.Application, *database.DB, error) {
		db, err := database.Open(dbPath)
		if err != nil {
			return nil, nil, err
		}
		core := app.NewWithDB(db)
		// the starter profile goes through its YAML file, like a real one
		prof := profile.Example("Self test")
		prof.DestinationRoot = out
		path := profile.PathFor(filepath.Join(tmp, "profiles"), "selftest")
		if err := profile.Save(path, prof); err != nil {
			return nil, nil, err
		}
		loaded, err := profile.Load(path)
		if err != nil {
			return nil, nil, err
		}
		return core, db, core.ApplyProfile("selftest", loaded, tmp)
	}

	core, db, err := openCore()
	step("open bundled SQLite database", err)
	if err != nil {
		return finish(failures)
	}
	res, err := scanner.Scan(context.Background(), in, scanner.Options{})
	step("scan directory (4 files incl. unicode and unknown type)", orErr(err, len(res.Files) == 4, "expected 4 files"))
	_, err = core.ApplyScan(in, res.Files, false)
	step("store inventory", err)

	// tag every file with Music (key 5) and the first also with Events (key 3)
	for i := 0; i < core.Total(); i++ {
		core.First()
		for j := 0; j < i; j++ {
			core.Next()
		}
		_, e := core.ToggleByShortcut("5")
		if e != nil {
			err = e
		}
	}
	core.First()
	_, e := core.ToggleByShortcut("3")
	step("assign tags with keyboard shortcuts", orErr(e, err == nil, "toggle failed"))

	jobs := core.CopyJobs(true)
	sum, err := core.RunCopy(context.Background(), jobs, copier.Options{})
	// 5 jobs; the file without a year must be skipped, never copied
	step("copy to year/tag folders (variables and templates)", orErr(err, sum.Copied == 4 && sum.Invalid == 1 && sum.Failed == 0,
		fmt.Sprintf("unexpected summary %+v", sum)))
	step("copied content verified", verifyCopy(filepath.Join(in, "ñandú_2018 日本.txt"), filepath.Join(out, "2018", "Music", "ñandú_2018 日本.txt")))
	if _, serr := os.Stat(filepath.Join(out, "0000")); serr == nil {
		step("files without year are not copied", fmt.Errorf("found files for an unresolved year"))
	}
	sum, err = core.RunCopy(context.Background(), jobs, copier.Options{})
	step("second run skips identical files", orErr(err, sum.AlreadyCopied == 4 && sum.Copied == 0, fmt.Sprintf("unexpected summary %+v", sum)))
	step("sources untouched", verifyCopy(filepath.Join(in, "Concert_2019.mp3"), filepath.Join(out, "2019", "Music", "Concert_2019.mp3")))

	// "restart": reopen the database, classification must still be there
	db.Close()
	core2, db2, err := openCore()
	if err == nil {
		defer db2.Close()
		res2, _ := scanner.Scan(context.Background(), in, scanner.Options{})
		_, err = core2.ApplyScan(in, res2.Files, false)
		err = orErr(err, core2.TaggedCount() == 4, fmt.Sprintf("tagged files after restart: %d", core2.TaggedCount()))
	}
	step("assignments survive a restart", err)

	// optional components must work too
	pv := preview.Default().Create(filepath.Join(in, "Photo_2020.png"), "image/png", ".png")
	step("image preview", orErr(nil, pv.Kind == preview.KindImage && pv.Image != nil, "no image preview"))
	pv = preview.Default().Create(filepath.Join(in, "ñandú_2018 日本.txt"), "text/plain", ".txt")
	step("text preview", orErr(nil, pv.Kind == preview.KindText && strings.HasPrefix(pv.Text, "unicode"), "no text preview"))
	th := cache.NewThumbnails(filepath.Join(tmp, "thumbs"), 10<<20)
	st, _ := os.Stat(filepath.Join(in, "Photo_2020.png"))
	_, _, err = th.Load(filepath.Join(in, "Photo_2020.png"), st.Size(), st.ModTime(), func() (image.Image, error) {
		return preview.Thumbnail(filepath.Join(in, "Photo_2020.png"), 64)
	})
	step("thumbnail generation and cache", err)

	return finish(failures)
}

type pngBuilder struct{ b *strings.Builder }

func (p *pngBuilder) Write(d []byte) (int, error) { return p.b.Write(d) }

func orErr(err error, ok bool, msg string) error {
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s", msg)
	}
	return nil
}

func verifyCopy(src, dst string) error {
	h := func(p string) (string, error) {
		b, err := os.ReadFile(p)
		if err != nil {
			return "", err
		}
		s := sha256.Sum256(b)
		return hex.EncodeToString(s[:]), nil
	}
	a, err := h(src)
	if err != nil {
		return err
	}
	b, err := h(dst)
	if err != nil {
		return err
	}
	if a != b {
		return fmt.Errorf("content differs")
	}
	return nil
}

func finish(failures []string) int {
	if len(failures) > 0 {
		fmt.Printf("SELF-TEST FAILED (%d problem(s))\n", len(failures))
		return 1
	}
	fmt.Println("SELF-TEST OK")
	return 0
}
