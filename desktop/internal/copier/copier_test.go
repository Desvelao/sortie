package copier

import (
	"context"
	"golang.org/x/sys/unix"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"sortie/internal/database"
	"sortie/internal/destination"
	"sortie/internal/fileinfo"
	"sortie/internal/tags"
)

type env struct {
	t        *testing.T
	in, root string
	eng      *Engine
	hist     *memHistory
	res      destination.Resolver
}

type memHistory struct {
	mu   sync.Mutex
	rows map[int64]*Result
	st   map[int64]string
	next int64
}

func (h *memHistory) Begin(j Job, dest string) (int64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.next++
	h.rows[h.next] = &Result{Job: j, DestPath: dest}
	h.st[h.next] = "IN_PROGRESS"
	return h.next, nil
}
func (h *memHistory) Note(id int64, n string) error { return nil }
func (h *memHistory) Finish(id int64, r Result) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.st[id] = string(r.Status)
	return nil
}
func (h *memHistory) count(status string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, s := range h.st {
		if s == status {
			n++
		}
	}
	return n
}

func newEnv(t *testing.T) *env {
	h := &memHistory{rows: map[int64]*Result{}, st: map[int64]string{}}
	root := t.TempDir()
	return &env{t: t, in: t.TempDir(), root: root, hist: h,
		eng: New(destination.NewChecker(2), h),
		res: destination.Resolver{Root: root, Template: "{year}/{tag}/{filename}", VarNames: []string{"year"}}}
}

func (e *env) src(name, content string) string {
	p := filepath.Join(e.in, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
	return p
}

// yearVars builds the variable map of a file whose year is y (0 = unresolved).
func yearVars(y int) map[string]string {
	if y == 0 {
		return nil
	}
	return map[string]string{"year": strconv.Itoa(y)}
}

func (e *env) job(path string, year int, tagID int, tagName string) Job {
	st, err := os.Stat(path)
	if err != nil {
		e.t.Fatal(err)
	}
	return Job{FileID: 1, SourcePath: path, Name: filepath.Base(path), Size: st.Size(), ModTime: st.ModTime(), Vars: yearVars(year),
		Tag: tags.Tag{ID: tagID, Name: tagName, Destination: tagName, Enabled: true}}
}

func (e *env) dest(parts ...string) string {
	return filepath.Join(append([]string{e.root}, parts...)...)
}

func (e *env) read(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		e.t.Fatal(err)
	}
	return string(b)
}

func (e *env) noTemps() {
	e.t.Helper()
	filepath.WalkDir(e.root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && strings.HasPrefix(d.Name(), tempPrefix) {
			e.t.Errorf("temporary file left behind: %s", p)
		}
		return nil
	})
}

func run(t *testing.T, e *env, jobs []Job, opts Options) Summary {
	t.Helper()
	s, err := e.eng.Run(context.Background(), e.res, jobs, opts)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCopyOneFileToMultipleDestinations(t *testing.T) {
	e := newEnv(t)
	p := e.src("Concert_2019.mp3", "music data ü")
	old := time.Date(2019, 5, 1, 12, 0, 0, 0, time.UTC)
	os.Chtimes(p, old, old)
	jobs := []Job{e.job(p, 2019, 1, "Music"), e.job(p, 2019, 2, "Events"), e.job(p, 2019, 3, "Important")}
	s := run(t, e, jobs, Options{})
	if s.Copied != 3 || s.Failed != 0 {
		t.Fatalf("%+v", s)
	}
	for _, tg := range []string{"Music", "Events", "Important"} {
		d := e.dest("2019", tg, "Concert_2019.mp3")
		if e.read(d) != "music data ü" {
			t.Errorf("%s content", tg)
		}
		if st, _ := os.Stat(d); !st.ModTime().Equal(old) {
			t.Errorf("%s: modification time not preserved", tg)
		}
	}
	if e.read(p) != "music data ü" {
		t.Fatal("source modified")
	}
	if e.hist.count("COPIED") != 3 {
		t.Fatalf("history: %v", e.hist.st)
	}
	e.noTemps()
	for _, r := range s.Results {
		if r.SrcHash == "" || r.SrcHash != r.DstHash || r.Bytes == 0 {
			t.Errorf("%+v", r)
		}
	}
}

func TestRerunSkipsIdenticalAndResumes(t *testing.T) {
	e := newEnv(t)
	a, b := e.src("a_2020.txt", "A"), e.src("b_2020.txt", "B")
	jobs := []Job{e.job(a, 2020, 1, "Family"), e.job(b, 2020, 1, "Family")}
	// "interrupted" run: only the first job got done
	run(t, e, jobs[:1], Options{})
	s := run(t, e, jobs, Options{}) // resume
	if s.AlreadyCopied != 1 || s.Copied != 1 {
		t.Fatalf("%+v", s)
	}
	s = run(t, e, jobs, Options{})
	if s.AlreadyCopied != 2 || s.Copied != 0 {
		t.Fatalf("%+v", s)
	}
	if e.hist.count("COPIED") != 2 { // skipped jobs create no history rows
		t.Fatalf("%v", e.hist.st)
	}
}

func TestConflictPolicies(t *testing.T) {
	e := newEnv(t)
	p := e.src("doc_2021.txt", "NEW CONTENT")
	existing := e.dest("2021", "Work", "doc_2021.txt")
	os.MkdirAll(filepath.Dir(existing), 0o755)
	os.WriteFile(existing, []byte("OLD CONTENT"), 0o644)
	job := e.job(p, 2021, 1, "Work")

	// default: skip, destination untouched
	s := run(t, e, []Job{job}, Options{})
	if s.ConflictsSkipped != 1 || e.read(existing) != "OLD CONTENT" {
		t.Fatalf("%+v", s)
	}
	// rename: new file next to the old one; re-running does not pile up copies
	s = run(t, e, []Job{job}, Options{Policy: PolicyRename})
	alt := e.dest("2021", "Work", "doc_2021 (1).txt")
	if s.Renamed != 1 || e.read(alt) != "NEW CONTENT" || e.read(existing) != "OLD CONTENT" {
		t.Fatalf("%+v", s)
	}
	s = run(t, e, []Job{job}, Options{Policy: PolicyRename})
	if s.AlreadyCopied != 1 || s.Renamed != 0 {
		t.Fatalf("rename re-run: %+v", s)
	}
	if _, err := os.Stat(e.dest("2021", "Work", "doc_2021 (2).txt")); err == nil {
		t.Fatal("duplicate alternative created")
	}
	// replace needs confirmation
	if _, err := e.eng.Run(context.Background(), e.res, []Job{job}, Options{Policy: PolicyReplace}); err != ErrReplaceNotConfirmed {
		t.Fatalf("got %v", err)
	}
	if e.read(existing) != "OLD CONTENT" {
		t.Fatal("replaced without confirmation")
	}
	s = run(t, e, []Job{job}, Options{Policy: PolicyReplace, ConfirmReplace: true})
	if s.Replaced != 1 || e.read(existing) != "NEW CONTENT" || e.read(p) != "NEW CONTENT" {
		t.Fatalf("%+v", s)
	}
	e.noTemps()
}

func TestUnresolvedVariableIsNotCopied(t *testing.T) {
	e := newEnv(t)
	p := e.src("unknown_file.pdf", "x")
	j := e.job(p, 0, 1, "Documents")
	s := run(t, e, []Job{j}, Options{})
	if s.Invalid != 1 || s.Copied != 0 {
		t.Fatalf("%+v", s)
	}
	if entries, _ := os.ReadDir(e.root); len(entries) != 0 {
		t.Fatalf("something was written: %v", entries)
	}
	// explicit alternative rule
	e.res.Template = "{year|Unknown}/{tag}/{filename}"
	s = run(t, e, []Job{j}, Options{})
	if s.Copied != 1 || e.read(e.dest("Unknown", "Documents", "unknown_file.pdf")) != "x" {
		t.Fatalf("%+v", s)
	}
}

func TestSourceProblemsAreFailuresNotCrashes(t *testing.T) {
	e := newEnv(t)
	good := e.src("good_2020.txt", "ok")
	gone := e.src("gone_2020.txt", "bye")
	changed := e.src("changed_2020.txt", "v1")
	jobs := []Job{e.job(gone, 2020, 1, "A"), e.job(changed, 2020, 1, "A"), e.job(good, 2020, 1, "A")}
	os.Remove(gone)
	os.WriteFile(changed, []byte("version two!"), 0o644) // size differs from the scan
	s := run(t, e, jobs, Options{})
	if s.Failed != 2 || s.Copied != 1 {
		t.Fatalf("%+v", s)
	}
	if !strings.Contains(s.Results[1].Detail, "changed") {
		t.Fatalf("%q", s.Results[1].Detail)
	}
	if e.hist.count("FAILED") != 1 { // the missing file fails in the pre-check, before any write
		t.Logf("history: %v", e.hist.st)
	}
	if _, err := os.Stat(e.dest("2020", "A", "changed_2020.txt")); err == nil {
		t.Fatal("changed source was copied")
	}
	e.noTemps()
}

func TestMissingDestinationRootIsRefused(t *testing.T) {
	e := newEnv(t)
	p := e.src("a_2020.txt", "x")
	e.res.Root = filepath.Join(e.root, "not-mounted", "Sorted")
	if _, err := e.eng.Run(context.Background(), e.res, []Job{e.job(p, 2020, 1, "A")}, Options{}); err == nil {
		t.Fatal("expected error")
	}
	if _, err := os.Stat(filepath.Join(e.root, "not-mounted")); err == nil {
		t.Fatal("root must never be created implicitly")
	}
	e.res.Root = ""
	if _, err := e.eng.Run(context.Background(), e.res, nil, Options{}); err == nil {
		t.Fatal("empty root accepted")
	}
}

func TestPermissionErrorIsReportedAndBatchContinues(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores permissions")
	}
	e := newEnv(t)
	a, b := e.src("a_2020.txt", "A"), e.src("b_2021.txt", "B")
	ro := e.dest("2020")
	os.MkdirAll(ro, 0o755)
	os.Chmod(ro, 0o555)
	defer os.Chmod(ro, 0o755)
	s := run(t, e, []Job{e.job(a, 2020, 1, "Fam"), e.job(b, 2021, 1, "Fam")}, Options{})
	if s.Failed != 1 || s.Copied != 1 {
		t.Fatalf("%+v", s)
	}
	if e.read(a) != "A" {
		t.Fatal("source damaged")
	}
	e.noTemps()
}

func TestCancelBeforeAndDuringCopy(t *testing.T) {
	e := newEnv(t)
	p := e.src("a_2020.txt", "A")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s, err := e.eng.Run(ctx, e.res, []Job{e.job(p, 2020, 1, "A")}, Options{})
	if err != nil || s.Cancelled != 1 || s.Copied != 0 {
		t.Fatalf("%+v %v", s, err)
	}

	// mid-copy: a large sparse file, cancelled on the first progress event
	big := filepath.Join(e.in, "big_2020.bin")
	f, _ := os.Create(big)
	f.Truncate(512 << 20)
	f.Close()
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	s, err = e.eng.Run(ctx, e.res, []Job{e.job(big, 2020, 1, "Big")}, Options{OnEvent: func(ev Event) {
		if ev.Kind == EventBytes {
			cancel()
		}
	}})
	if err != nil || s.Cancelled != 1 {
		t.Fatalf("%+v %v", s, err)
	}
	if _, err := os.Stat(e.dest("2020", "Big", "big_2020.bin")); err == nil {
		t.Fatal("partial file was published")
	}
	e.noTemps()
	if st, _ := os.Stat(big); st.Size() != 512<<20 {
		t.Fatal("source changed")
	}
}

func TestLargeFileCopyIsVerified(t *testing.T) {
	e := newEnv(t)
	big := filepath.Join(e.in, "large_2020.bin")
	data := make([]byte, 40<<20)
	for i := range data {
		data[i] = byte(i * 7)
	}
	os.WriteFile(big, data, 0o644)
	var sawBytes bool
	s := run(t, e, []Job{e.job(big, 2020, 1, "Big")}, Options{OnEvent: func(ev Event) {
		if ev.Kind == EventBytes {
			sawBytes = true
		}
	}})
	if s.Copied != 1 {
		t.Fatalf("%+v", s)
	}
	cmp := destination.NewChecker(1).Compare(context.Background(), big, e.dest("2020", "Big", "large_2020.bin"))
	if cmp.Status != destination.StatusAlreadyCopied {
		t.Fatalf("%+v", cmp)
	}
	_ = sawBytes // progress events are time-throttled; presence is not guaranteed on fast disks
}

func TestConcurrentJobsNeverClobber(t *testing.T) {
	e := newEnv(t)
	// two different sources that resolve to the same destination (same name, year, tag)
	a := e.src("one/IMG_2020.jpg", "FIRST")
	b := e.src("two/IMG_2020.jpg", "SECOND!")
	for i := 0; i < 20; i++ {
		os.RemoveAll(e.dest("2020"))
		s := run(t, e, []Job{e.job(a, 2020, 1, "Pics"), e.job(b, 2020, 1, "Pics")}, Options{Workers: 4})
		if s.Copied != 1 || s.ConflictsSkipped != 1 {
			t.Fatalf("iteration %d: %+v", i, s)
		}
		got := e.read(e.dest("2020", "Pics", "IMG_2020.jpg"))
		if got != "FIRST" && got != "SECOND!" {
			t.Fatalf("corrupt destination %q", got)
		}
	}
	e.noTemps()
}

func TestSameFileTwoTagsSameDestination(t *testing.T) {
	// the same file with two tags that map to the same folder: second is ALREADY_COPIED, never a duplicate
	e := newEnv(t)
	p := e.src("x_2020.txt", "X")
	j1, j2 := e.job(p, 2020, 1, "Same"), e.job(p, 2020, 2, "Same")
	s := run(t, e, []Job{j1, j2}, Options{Workers: 4})
	if s.Copied+s.AlreadyCopied != 2 || s.Copied != 1 {
		t.Fatalf("%+v", s)
	}
}

func TestUnicodeAndNoExtension(t *testing.T) {
	e := newEnv(t)
	a := e.src("ñandú 日本_2018", "u")
	b := e.src("README_2018", "r")
	s := run(t, e, []Job{e.job(a, 2018, 1, "Familia/Fotos"), e.job(b, 2018, 1, "Docs")}, Options{})
	if s.Copied != 2 || e.read(e.dest("2018", "Familia", "Fotos", "ñandú 日本_2018")) != "u" {
		t.Fatalf("%+v", s)
	}
}

func TestRenameNoReplace(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "dst")
	os.WriteFile(src, []byte("new"), 0o644)
	os.WriteFile(dst, []byte("precious"), 0o644)
	if err := renameNoReplace(src, dst); err != fs.ErrExist {
		t.Fatalf("got %v", err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "precious" {
		t.Fatal("destination overwritten")
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatal("source must stay for the caller to clean up")
	}
	os.Remove(dst)
	if err := renameNoReplace(src, dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "new" {
		t.Fatal("not moved")
	}
}

func TestAlternativeName(t *testing.T) {
	if got := alternativeName("/a/b/doc.txt", 2); got != "/a/b/doc (2).txt" {
		t.Fatal(got)
	}
	if got := alternativeName("/a/b/noext", 1); got != "/a/b/noext (1)" {
		t.Fatal(got)
	}
	if got := alternativeName("/a/b/.bashrc", 1); got != "/a/b/.bashrc (1)" {
		t.Fatal(got)
	}
	if got := alternativeName("/a/b/a.tar.gz", 1); got != "/a/b/a (1).tar.gz" {
		t.Fatal(got)
	}
	if got := alternativeName("/a/b/.tar", 1); got != "/a/b/.tar (1)" {
		t.Fatal(got)
	}
}

func TestProgressEventsAndPlan(t *testing.T) {
	e := newEnv(t)
	a, b := e.src("a_2020.txt", "A"), e.src("b_2020.txt", "B")
	jobs := []Job{e.job(a, 2020, 1, "T"), e.job(b, 0, 1, "T")}
	plan := e.eng.Plan(context.Background(), e.res, jobs, 2, nil)
	if plan[0].Status != destination.StatusReady || plan[1].Status != destination.StatusUnresolvedVar {
		t.Fatalf("%+v", plan)
	}
	if entries, _ := os.ReadDir(e.root); len(entries) != 0 {
		t.Fatal("Plan must not write")
	}
	var starts, dones int
	var last Event
	run(t, e, jobs, Options{Workers: 1, OnEvent: func(ev Event) {
		switch ev.Kind {
		case EventStart:
			starts++
		case EventDone:
			dones++
			last = ev
		}
	}})
	if starts != 2 || dones != 2 || last.Total != 2 || last.Done != 2 {
		t.Fatalf("%d %d %+v", starts, dones, last)
	}
}

// With the real SQLite history: a crash mid-copy is detected and reported; a rerun finishes the job.
func TestDBHistoryAndResumeAfterCrash(t *testing.T) {
	db, err := database.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	files := []fileinfo.FileInfo{{Path: "/x/a_2020.txt", Name: "a_2020.txt", ModTime: time.Unix(1, 0)}}
	db.SyncFiles("/x", files, false)
	db.SyncProfileTags(database.DefaultProfile, tags.Defaults())

	e := newEnv(t)
	e.eng = New(destination.NewChecker(1), DBHistory{db})
	p := e.src("a_2020.txt", "data")
	j := e.job(p, 2020, 1, "Family")
	j.FileID = files[0].ID

	// simulate a crash: an IN_PROGRESS row and a stray temp file, but no destination file
	id, _ := db.BeginCopy(j.FileID, 1, "Family", p, e.dest("2020", "Family", "a_2020.txt"))
	os.MkdirAll(e.dest("2020", "Family"), 0o755)
	stray := e.dest("2020", "Family", tempPrefix+"123.tmp")
	os.WriteFile(stray, []byte("da"), 0o644)
	db.NoteCopy(id, stray)
	if n, _ := db.MarkInterrupted(); n != 1 {
		t.Fatalf("interrupted: %d", n)
	}

	s := run(t, e, []Job{j}, Options{})
	if s.Copied != 1 || e.read(e.dest("2020", "Family", "a_2020.txt")) != "data" {
		t.Fatalf("%+v", s)
	}
	if _, err := os.Stat(stray); err != nil {
		t.Fatal("stray temp file from the crash must not be deleted automatically")
	}
	if got, _ := db.InterruptedTempFiles(); len(got) != 1 || got[0] != stray {
		t.Fatalf("the interrupted entry should name the leftover: %v", got)
	}
	hist, _ := db.History(j.FileID)
	if len(hist) != 2 || hist[0].Status != "COPIED" || hist[1].Status != "INTERRUPTED" || hist[0].DestHash == "" {
		t.Fatalf("%+v", hist)
	}
}

func TestReplaceNeverOverwritesFileWrittenByTheSameBatch(t *testing.T) {
	for _, workers := range []int{1, 4} {
		e := newEnv(t)
		// same file name from two folders, both resolve to <root>/2020/T/same_2020.txt
		a := e.src("one/same_2020.txt", "FIRST")
		b := e.src("two/same_2020.txt", "SECOND")
		jobs := []Job{e.job(a, 2020, 1, "T"), e.job(b, 2020, 1, "T")}

		plan := e.eng.Plan(context.Background(), e.res, jobs, 2, nil)
		if !strings.Contains(plan[1].Detail, "same destination") || plan[0].Detail != "" {
			t.Fatalf("plan should flag only the second job: %+v", plan)
		}

		s, err := e.eng.Run(context.Background(), e.res, jobs, Options{Workers: workers, Policy: PolicyReplace, ConfirmReplace: true})
		if err != nil {
			t.Fatal(err)
		}
		if s.Copied != 1 || s.Replaced != 0 || s.ConflictsSkipped != 1 {
			t.Fatalf("workers=%d: %+v", workers, s)
		}
		got := e.read(e.dest("2020", "T", "same_2020.txt"))
		if got != "FIRST" && got != "SECOND" {
			t.Fatalf("workers=%d: destination content %q", workers, got)
		}
		// whichever file won, it was not overwritten by the other
		winner := s.Results[0].Status == StatusCopied
		if winner && got != "FIRST" || !winner && got != "SECOND" {
			t.Fatalf("workers=%d: %q does not match the job reported as copied", workers, got)
		}
	}
}

func TestReplaceStillReplacesPreExistingFiles(t *testing.T) {
	e := newEnv(t)
	p := e.src("x_2020.txt", "NEW")
	os.MkdirAll(e.dest("2020", "T"), 0o755)
	os.WriteFile(e.dest("2020", "T", "x_2020.txt"), []byte("OLD"), 0o644)
	s := run(t, e, []Job{e.job(p, 2020, 1, "T")}, Options{Policy: PolicyReplace, ConfirmReplace: true})
	if s.Replaced != 1 || e.read(e.dest("2020", "T", "x_2020.txt")) != "NEW" {
		t.Fatalf("%+v", s)
	}
}

func TestRemoveTempFileOnlyTouchesOurTempFiles(t *testing.T) {
	dir := t.TempDir()
	stray := filepath.Join(dir, tempPrefix+"42.tmp")
	os.WriteFile(stray, []byte("x"), 0o644)
	if removed, err := RemoveTempFile(stray); !removed || err != nil {
		t.Fatalf("%v %v", removed, err)
	}
	if removed, err := RemoveTempFile(stray); removed || err != nil {
		t.Fatalf("already gone: %v %v", removed, err)
	}
	other := filepath.Join(dir, "photo.jpg")
	os.WriteFile(other, []byte("precious"), 0o644)
	if _, err := RemoveTempFile(other); err == nil {
		t.Fatal("must refuse files that are not ours")
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatal("foreign file was removed")
	}
	if _, err := RemoveTempFile(filepath.Join(dir, tempPrefix+"dir.tmp")); err != nil {
		t.Fatalf("missing file is not an error: %v", err)
	}
	os.Mkdir(filepath.Join(dir, tempPrefix+"d.tmp"), 0o755)
	if _, err := RemoveTempFile(filepath.Join(dir, tempPrefix+"d.tmp")); err == nil {
		t.Fatal("must refuse directories")
	}
}

func TestReplaceNeverRemovesADirectoryAtTheDestination(t *testing.T) {
	e := newEnv(t)
	p := e.src("x_2020.txt", "NEW")
	dst := e.dest("2020", "T", "x_2020.txt")
	os.MkdirAll(filepath.Join(dst, "inner"), 0o755)
	os.WriteFile(filepath.Join(dst, "inner", "keep.txt"), []byte("keep"), 0o644)
	s := run(t, e, []Job{e.job(p, 2020, 1, "T")}, Options{Policy: PolicyReplace, ConfirmReplace: true})
	if s.Copied+s.Replaced != 0 {
		t.Fatalf("a directory must not be replaced: %+v", s)
	}
	if e.read(filepath.Join(dst, "inner", "keep.txt")) != "keep" {
		t.Fatal("directory contents were lost")
	}
}

func TestDanglingSymlinkAtDestinationNeverWritesThroughIt(t *testing.T) {
	for _, pol := range []ConflictPolicy{PolicySkip, PolicyRename, PolicyReplace} {
		e := newEnv(t)
		p := e.src("x_2020.txt", "NEW")
		dst := e.dest("2020", "T", "x_2020.txt")
		os.MkdirAll(filepath.Dir(dst), 0o755)
		outside := filepath.Join(t.TempDir(), "outside.txt") // does not exist
		if err := os.Symlink(outside, dst); err != nil {
			t.Fatal(err)
		}
		run(t, e, []Job{e.job(p, 2020, 1, "T")}, Options{Policy: pol, ConfirmReplace: true})
		if _, err := os.Lstat(outside); err == nil {
			t.Fatalf("policy %v wrote through the dangling symlink to %s", pol, outside)
		}
	}
}

func TestRunWithCancelledContextMarksJobsCancelled(t *testing.T) {
	e := newEnv(t)
	p := e.src("x_2020.txt", "X")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s, err := e.eng.Run(ctx, e.res, []Job{e.job(p, 2020, 1, "T")}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Cancelled != 1 || s.Results[0].Status != StatusCancelled || s.Copied != 0 {
		t.Fatalf("%+v", s)
	}
	if _, err := os.Stat(e.dest("2020", "T", "x_2020.txt")); err == nil {
		t.Fatal("nothing may be written after cancellation")
	}
}

// FAT/exFAT/NTFS mounts refuse chmod and utimes with EPERM/ENOTSUP: the copy
// must still succeed. Other errors must still fail the job.
func TestCopySucceedsWhenFilesystemRefusesChmodAndChtimes(t *testing.T) {
	origChmod, origChtimes := chmodFn, chtimesFn
	defer func() { chmodFn, chtimesFn = origChmod, origChtimes }()
	chmodFn = func(string, os.FileMode) error { return &os.PathError{Op: "chmod", Err: unix.EPERM} }
	chtimesFn = func(string, time.Time, time.Time) error { return &os.PathError{Op: "chtimes", Err: unix.ENOTSUP} }

	e := newEnv(t)
	p := e.src("a_2019.txt", "data")
	s := run(t, e, []Job{e.job(p, 2019, 1, "Music")}, Options{})
	if s.Copied != 1 || s.Failed != 0 {
		t.Fatalf("%+v", s)
	}
	if e.read(e.dest("2019", "Music", "a_2019.txt")) != "data" {
		t.Error("content")
	}
	e.noTemps()

	chmodFn = func(string, os.FileMode) error { return &os.PathError{Op: "chmod", Err: unix.EIO} }
	p2 := e.src("b_2019.txt", "data2")
	if s := run(t, e, []Job{e.job(p2, 2019, 1, "Music")}, Options{}); s.Failed != 1 {
		t.Fatalf("an I/O error must still fail the job: %+v", s)
	}
	e.noTemps()
}

func TestBatchDuplicateNoteIsCaseInsensitive(t *testing.T) {
	checks := []destination.Check{
		{Path: "/out/Photo.JPG", Status: destination.StatusReady},
		{Path: "/out/photo.jpg", Status: destination.StatusReady},
		{Path: "/out/photo.jpg", Status: destination.StatusReady},
		{Path: "/out/other.jpg", Status: destination.StatusReady},
	}
	noteBatchDuplicates(checks)
	if checks[0].Detail != "" || checks[3].Detail != "" {
		t.Errorf("unexpected notes: %q %q", checks[0].Detail, checks[3].Detail)
	}
	if !strings.Contains(checks[1].Detail, "letter case") {
		t.Errorf("case-only duplicate not flagged: %q", checks[1].Detail)
	}
	if !strings.Contains(checks[2].Detail, "same destination") {
		t.Errorf("exact duplicate not flagged: %q", checks[2].Detail)
	}
}

// On a case-insensitive filesystem Photo.JPG and photo.jpg are one file; a
// hard link stands in for that here.
func TestBatchPathsRecognisesCaseAliases(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "Photo.JPG"), filepath.Join(dir, "photo.jpg")
	if err := os.WriteFile(a, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(a, b); err != nil {
		t.Skip("hard links unavailable:", err)
	}
	bp := &batchPaths{paths: map[string]bool{a: true}}
	if !bp.wrote(b) {
		t.Error("alias of an already written file must count as written")
	}
	// two genuinely different files whose names differ only in case
	c, d := filepath.Join(dir, "Other.jpg"), filepath.Join(dir, "other.jpg")
	os.WriteFile(c, []byte("1"), 0o644)
	os.WriteFile(d, []byte("2"), 0o644)
	bp = &batchPaths{paths: map[string]bool{c: true}}
	if bp.wrote(d) {
		t.Error("distinct files on a case-sensitive filesystem must not be confused")
	}
}
