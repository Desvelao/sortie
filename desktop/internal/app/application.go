// Package app holds UI-independent application state: the file list, current
// position, the active profile (tags, variables, destination), variable
// values and tag persistence. It does not import Fyne.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"sortie/internal/copier"
	"sortie/internal/database"
	"sortie/internal/destination"
	"sortie/internal/fileinfo"
	"sortie/internal/media"
	"sortie/internal/profile"
	"sortie/internal/tags"
	"sortie/internal/vars"
)

// Application tracks the file list, current position, tags and variables of
// the active profile.
type Application struct {
	mu sync.RWMutex
	// updateMu serialises UpdateProfile's read-modify-write cycle.
	updateMu sync.Mutex
	db       *database.DB
	files    []fileinfo.FileInfo
	index    int // index into files of the current file
	dir      string

	view        []int // indices into files shown under the current filter
	filter      Filter
	statuses    map[int64]fileStatus // destination status summary per file
	statusKnown bool
	statusGen   int // bumped whenever statuses are invalidated

	assigned map[int64]map[int]bool // file id -> tag ids
	tagList  []tags.Tag

	// active profile: raw (as written in its file), its id (file name without
	// extension; also the database key), the folder relative paths are
	// resolved against, and the compiled variables
	profileID string
	prof      profile.Profile
	baseDir   string
	varSet    *vars.Set
	manual    map[int64]map[string]string // file id -> variable -> manual value

	fieldsFn func(path string) map[string]string // metadata reader (media.Fields)
	fcMu     sync.Mutex
	fcache   map[string]fieldsEntry
	fcflight map[string]*fieldsCall // reads in progress, so a file is read once

	resolver destination.Resolver
	checker  *destination.Checker
	engine   *copier.Engine

	interrupted int // copies found IN_PROGRESS at startup
}

// fieldsCall is a metadata read in progress; waiters block on done.
type fieldsCall struct {
	done   chan struct{}
	fields map[string]string
}

type fieldsEntry struct {
	size   int64
	mtime  int64
	fields map[string]string
}

// New creates an application backed by an in-memory database (tests).
func New() *Application {
	db, err := database.OpenMemory()
	if err != nil {
		panic(err)
	}
	return NewWithDB(db)
}

// NewWithDB creates an application using db for persistence. Until a profile
// is applied it has no tags and no variables.
func NewWithDB(db *database.DB) *Application {
	chk := destination.NewChecker(2)
	n, err := db.MarkInterrupted() // a previous run was killed mid-copy
	if err != nil {
		slog.Warn("could not mark interrupted copies", "error", err)
	}
	set, _ := vars.New(nil)
	return &Application{db: db, assigned: map[int64]map[int]bool{}, manual: map[int64]map[string]string{},
		profileID: database.DefaultProfile, prof: profile.New("Default"), varSet: set,
		fieldsFn: media.Fields, fcache: map[string]fieldsEntry{}, fcflight: map[string]*fieldsCall{},
		resolver: destination.Resolver{Template: profile.DefaultTemplate},
		checker:  chk, engine: copier.New(chk, copier.DBHistory{DB: db}), interrupted: n}
}

// ---- profile ----

// ApplyProfile validates p and makes it the active profile: its variables,
// tags (synchronised with the database by name), destination and per-profile
// state (tag assignments, manual values) take effect immediately. id is the
// profile's file name without extension; relative paths in p are resolved
// against baseDir. If validation fails nothing changes. Database errors
// leave the in-memory state on the old profile, but the database may already
// hold the synchronised tags, and manual values of variables the new profile
// no longer defines are deleted (PruneFileVars) before the later steps run.
func (a *Application) ApplyProfile(id string, p profile.Profile, baseDir string) error {
	if err := profile.ValidID(id); err != nil {
		return err
	}
	if err := p.Validate(); err != nil {
		return err
	}
	res := p.Resolved(baseDir)
	if err := res.ValidateResolved(); err != nil {
		return err
	}
	set, err := p.VarSet()
	if err != nil {
		return err
	}
	ids, err := a.db.SyncProfileTags(id, p.ToTags(nil))
	if err != nil {
		return err
	}
	if err := a.db.PruneFileVars(id, set.Names()); err != nil {
		return err
	}
	assign, err := a.db.LoadProfileAssignments(id)
	if err != nil {
		return err
	}
	manual, err := a.db.LoadFileVars(id)
	if err != nil {
		return err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.profileID, a.prof, a.baseDir, a.varSet = id, p, baseDir, set
	a.tagList = p.ToTags(ids)
	a.assigned = toAssigned(assign)
	a.manual = manual
	tpl := strings.TrimSpace(p.DestinationTemplate)
	if tpl == "" {
		tpl = profile.DefaultTemplate
	}
	a.resolver = destination.Resolver{Root: res.DestinationRoot, Template: tpl, VarNames: set.Names()}
	a.invalidateStatusesLocked()
	a.rebuildLocked()
	slog.Info("profile applied", "profile", id, "name", p.Name, "tags", len(p.Tags), "variables", len(p.Variables))
	return nil
}

// DeleteProfileData removes the stored tagging and manual values of a profile
// that is being deleted. The active profile cannot be removed.
func (a *Application) DeleteProfileData(id string) error {
	a.mu.RLock()
	active := a.profileID
	a.mu.RUnlock()
	if id == active {
		return fmt.Errorf("profile %q is in use", id)
	}
	return a.db.DeleteProfile(id)
}

// TaggedFileCount returns how many files have tags assigned in profile id;
// deleting that profile's data discards those assignments.
func (a *Application) TaggedFileCount(id string) (int, error) { return a.db.CountTaggedFiles(id) }

// UpdateProfile applies fn to a copy of the active profile and, if the result
// is valid, makes it the active profile. The caller persists it (profile.Save)
// when it should outlive the session.
func (a *Application) UpdateProfile(fn func(*profile.Profile)) error {
	// Serialise read-modify-write cycles so overlapping updates cannot start
	// from the same snapshot and overwrite each other. fn runs under this
	// mutex (not a.mu) and must not call UpdateProfile.
	a.updateMu.Lock()
	defer a.updateMu.Unlock()
	a.mu.RLock()
	id, base := a.profileID, a.baseDir
	p := cloneProfile(a.prof)
	a.mu.RUnlock()
	fn(&p)
	return a.ApplyProfile(id, p, base)
}

func cloneProfile(p profile.Profile) profile.Profile {
	p.Variables = append([]vars.Definition(nil), p.Variables...)
	p.Tags = append([]profile.Tag(nil), p.Tags...)
	return p
}

func toAssigned(m map[int64][]int) map[int64]map[int]bool {
	out := map[int64]map[int]bool{}
	for fid, ids := range m {
		s := map[int]bool{}
		for _, t := range ids {
			s[t] = true
		}
		out[fid] = s
	}
	return out
}

// Profile returns a copy of the active profile as written in its file.
func (a *Application) Profile() profile.Profile {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return cloneProfile(a.prof)
}

// ProfileID returns the active profile's id (file name without extension).
func (a *Application) ProfileID() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.profileID
}

// InputDir returns the profile's input directory with "~" and relative paths
// resolved; "" if none is configured.
func (a *Application) InputDir() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.prof.Resolved(a.baseDir).InputDirectory
}

// ---- variables ----

// VarInfo is the value of one variable for one file.
type VarInfo struct {
	Name string
	vars.Value
	Manual bool // the value comes from a manual override
}

// VarNames returns the profile's variable names in definition order.
func (a *Application) VarNames() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.varSet.Names()
}

// VarsOf resolves every variable for f (manual > the profile's sources).
func (a *Application) VarsOf(f fileinfo.FileInfo) []VarInfo {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.varsOfLocked(f)
}

func (a *Application) varsOfLocked(f fileinfo.FileInfo) []VarInfo {
	vals := a.varSet.ResolveAll(a.fileContextLocked(f))
	var out []VarInfo
	for _, n := range a.varSet.Names() {
		v := vals[n]
		out = append(out, VarInfo{Name: n, Value: v, Manual: v.Source == vars.KindManual})
	}
	return out
}

// valuesLocked returns only the resolved values by name, for templates.
func (a *Application) valuesLocked(f fileinfo.FileInfo) map[string]string {
	out := map[string]string{}
	for n, v := range a.varSet.ResolveAll(a.fileContextLocked(f)) {
		if v.OK() {
			out[n] = v.Value
		}
	}
	// {parent}: name of the folder the file lives in. It is a reserved
	// built-in, so no user variable can have set it above.
	if p := filepath.Base(filepath.Dir(f.Path)); p != "." && p != string(filepath.Separator) {
		out["parent"] = p
	}
	return out
}

func (a *Application) allResolvedLocked(f fileinfo.FileInfo) bool {
	for _, v := range a.varSet.ResolveAll(a.fileContextLocked(f)) {
		if !v.OK() {
			return false
		}
	}
	return true
}

func (a *Application) fileContextLocked(f fileinfo.FileInfo) vars.FileContext {
	rel := ""
	if a.dir != "" {
		if r, err := filepath.Rel(a.dir, filepath.Dir(f.Path)); err == nil && r != "." && !strings.HasPrefix(r, "..") {
			rel = filepath.ToSlash(r)
		}
	}
	return vars.FileContext{Path: f.Path, Name: f.Name, RelDir: rel, ModTime: f.ModTime,
		Manual: a.manual[f.ID], Fields: func() map[string]string { return a.fieldsFor(f) }}
}

// fieldsFor reads metadata once per file version (path, size, mtime).
func (a *Application) fieldsFor(f fileinfo.FileInfo) map[string]string {
	a.fcMu.Lock()
	e, ok := a.fcache[f.Path]
	a.fcMu.Unlock()
	if ok && e.size == f.Size && e.mtime == f.ModTime.UnixNano() {
		return e.fields
	}
	// Concurrent misses for the same file version share one read.
	key := fmt.Sprintf("%s|%d|%d", f.Path, f.Size, f.ModTime.UnixNano())
	a.fcMu.Lock()
	if c, ok := a.fcflight[key]; ok {
		a.fcMu.Unlock()
		<-c.done
		return c.fields
	}
	c := &fieldsCall{done: make(chan struct{})}
	a.fcflight[key] = c
	a.fcMu.Unlock()

	c.fields = a.fieldsFn(f.Path)
	a.fcMu.Lock()
	a.fcache[f.Path] = fieldsEntry{f.Size, f.ModTime.UnixNano(), c.fields}
	delete(a.fcflight, key)
	a.fcMu.Unlock()
	close(c.done)
	return c.fields
}

// pruneFieldsCache drops cached metadata of files that are not in files (any
// more): removed or renamed files and files of a previously scanned directory.
func (a *Application) pruneFieldsCache(files []fileinfo.FileInfo) {
	keep := make(map[string]struct{}, len(files))
	for _, f := range files {
		keep[f.Path] = struct{}{}
	}
	a.fcMu.Lock()
	defer a.fcMu.Unlock()
	for p := range a.fcache {
		if _, ok := keep[p]; !ok {
			delete(a.fcache, p)
		}
	}
}

// SetManualVar stores a manual value for variable name of f. The value must
// satisfy the variable's validation rules.
func (a *Application) SetManualVar(f fileinfo.FileInfo, name, value string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	value = strings.TrimSpace(value)
	if value == "" {
		return a.setManualLocked(f, name, "")
	}
	if err := a.varSet.Check(name, value); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return a.setManualLocked(f, name, value)
}

// ClearManualVar removes the manual value of variable name for f.
func (a *Application) ClearManualVar(f fileinfo.FileInfo, name string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.setManualLocked(f, name, "")
}

func (a *Application) setManualLocked(f fileinfo.FileInfo, name, value string) error {
	if f.ID == 0 {
		return fmt.Errorf("%s is not tracked in the database", f.Name)
	}
	if !a.varSet.Has(name) {
		return fmt.Errorf("unknown variable %q", name)
	}
	if err := a.db.SetFileVar(f.ID, a.profileID, name, value); err != nil {
		return err
	}
	if value == "" {
		delete(a.manual[f.ID], name)
	} else {
		if a.manual[f.ID] == nil {
			a.manual[f.ID] = map[string]string{}
		}
		a.manual[f.ID][name] = value
	}
	delete(a.statuses, f.ID)
	a.rebuildLocked()
	return nil
}

// ---- destinations ----

// SetDestination changes the destination root and default template of the
// active profile (in memory; see UpdateProfile).
func (a *Application) SetDestination(root, template string) error {
	return a.UpdateProfile(func(p *profile.Profile) {
		p.DestinationRoot, p.DestinationTemplate = root, template
	})
}

// Resolver returns the current destination settings.
func (a *Application) Resolver() destination.Resolver {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.resolver
}

// Checks evaluates the destination of every tag assigned to f. It reads the
// filesystem and may hash files, so call it from a goroutine; ctx cancels it.
// Results follow the display order of the tags.
func (a *Application) Checks(ctx context.Context, f fileinfo.FileInfo) []destination.Check {
	a.mu.RLock()
	res := a.resolver
	var assigned []tags.Tag
	for _, t := range a.tagList {
		if a.assigned[f.ID][t.ID] {
			assigned = append(assigned, t)
		}
	}
	values := a.valuesLocked(f)
	gen := a.statusGen
	a.mu.RUnlock()
	if len(assigned) == 0 {
		return nil
	}
	out := a.checker.Check(ctx, res, destination.Source{Path: f.Path, Name: f.Name, Vars: values}, assigned)
	if ctx.Err() == nil {
		a.recordChecks(gen, f.ID, assigned, out)
	}
	return out
}

// ---- copying ----

// LeftoverTempFiles lists temporary files that interrupted copies left in
// destination folders and that still exist.
func (a *Application) LeftoverTempFiles() []string {
	paths, err := a.db.InterruptedTempFiles()
	if err != nil {
		slog.Warn("could not list leftover temporary files", "error", err)
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, p := range paths {
		if seen[p] || !copier.IsTempFile(p) {
			continue
		}
		seen[p] = true
		if st, err := os.Lstat(p); err == nil && st.Mode().IsRegular() {
			out = append(out, p)
		}
	}
	return out
}

// RemoveLeftoverTempFiles deletes the files LeftoverTempFiles reports and
// returns how many were removed. Only call it after the user agreed.
func (a *Application) RemoveLeftoverTempFiles() (int, error) {
	n := 0
	for _, p := range a.LeftoverTempFiles() {
		removed, err := copier.RemoveTempFile(p)
		if err != nil {
			return n, err
		}
		if removed {
			n++
		}
	}
	return n, nil
}

// InterruptedAtStart returns how many copies were left IN_PROGRESS by a
// previous run that was killed. Copying again resumes them.
func (a *Application) InterruptedAtStart() int { return a.interrupted }

// CopyJobs builds one job per (file, enabled assigned tag): for the current
// file only, or for every listed file.
func (a *Application) CopyJobs(all bool) []copier.Job {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var files []fileinfo.FileInfo
	if all {
		files = a.files
	} else if len(a.view) > 0 && a.index >= 0 && a.index < len(a.files) {
		files = a.files[a.index : a.index+1]
	}
	var jobs []copier.Job
	for _, f := range files {
		values := a.valuesLocked(f)
		for _, t := range a.tagList {
			if t.Enabled && a.assigned[f.ID][t.ID] {
				jobs = append(jobs, copier.Job{FileID: f.ID, SourcePath: f.Path, Name: f.Name,
					Size: f.Size, ModTime: f.ModTime, Vars: values, Tag: t})
			}
		}
	}
	return jobs
}

// PlanCopy evaluates jobs without writing anything.
func (a *Application) PlanCopy(ctx context.Context, jobs []copier.Job, onDone func(done, total int)) []destination.Check {
	return a.engine.Plan(ctx, a.Resolver(), jobs, 2, onDone)
}

// RunCopy copies the jobs. Classification (tags) is never modified, so
// failed or cancelled jobs can simply be run again.
func (a *Application) RunCopy(ctx context.Context, jobs []copier.Job, opts copier.Options) (copier.Summary, error) {
	slog.Info("copy started", "jobs", len(jobs), "policy", opts.Policy.String(), "root", a.Resolver().Root)
	sum, err := a.engine.Run(ctx, a.Resolver(), jobs, opts)
	if err != nil {
		slog.Error("copy not started", "error", err)
		return sum, err
	}
	for _, r := range sum.Results {
		switch r.Status {
		case copier.StatusCopied, copier.StatusReplaced, copier.StatusRenamed:
			slog.Info("copied", "status", string(r.Status), "src", r.Job.SourcePath, "dst", r.DestPath, "bytes", r.Bytes, "sha256", r.DstHash)
		case copier.StatusFailed:
			slog.Warn("copy failed", "src", r.Job.SourcePath, "dst", r.DestPath, "error", r.Detail)
		case copier.StatusConflictSkip, copier.StatusInvalid:
			slog.Info("copy skipped", "status", string(r.Status), "src", r.Job.SourcePath, "dst", r.DestPath, "detail", r.Detail)
		}
	}
	slog.Info("copy finished", "copied", sum.Copied, "replaced", sum.Replaced, "renamed", sum.Renamed,
		"already", sum.AlreadyCopied, "conflicts", sum.ConflictsSkipped, "invalid", sum.Invalid,
		"failed", sum.Failed, "cancelled", sum.Cancelled, "bytes", sum.Bytes)
	a.applyCopyResults(sum)
	return sum, nil
}

// History returns the copy attempts recorded for f, newest first.
func (a *Application) History(f fileinfo.FileInfo) ([]database.HistoryEntry, error) {
	return a.db.History(f.ID)
}

// ---- tags ----

// SetTags replaces the tags of the active profile (in memory; see
// UpdateProfile). Tags are matched to existing ones by name: a removed tag
// loses its assignments, a renamed tag starts over. Ids in ts are ignored.
func (a *Application) SetTags(ts []tags.Tag) error {
	if err := tags.Validate(ts); err != nil {
		return err
	}
	pts := profile.FromTags(tags.Normalize(ts))
	return a.UpdateProfile(func(p *profile.Profile) { p.Tags = pts })
}

// Tags returns the tags in display order.
func (a *Application) Tags() []tags.Tag {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return append([]tags.Tag(nil), a.tagList...)
}

// HasTag reports whether f carries the tag.
func (a *Application) HasTag(f fileinfo.FileInfo, tagID int) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.assigned[f.ID][tagID]
}

// TagsOf returns the ids of tags assigned to f.
func (a *Application) TagsOf(f fileinfo.FileInfo) []int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var out []int
	for _, t := range a.tagList {
		if a.assigned[f.ID][t.ID] {
			out = append(out, t.ID)
		}
	}
	return out
}

// FilesByTags returns the listed files (scan order) that carry the given tags:
// at least one of them, or all of them when matchAll is set. It does not
// depend on or change the active filter. No tag ids yields no files.
func (a *Application) FilesByTags(tagIDs []int, matchAll bool) []fileinfo.FileInfo {
	if len(tagIDs) == 0 {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	var out []fileinfo.FileInfo
	for _, f := range a.files {
		have := a.assigned[f.ID]
		n := 0
		for _, id := range tagIDs {
			if have[id] {
				n++
			}
		}
		if (matchAll && n == len(tagIDs)) || (!matchAll && n > 0) {
			out = append(out, f)
		}
	}
	return out
}

// ToggleTag toggles an enabled tag on the current file; other tags are kept.
func (a *Application) ToggleTag(tagID int) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.toggleLocked(tagID)
}

func (a *Application) toggleLocked(tagID int) (bool, error) {
	if len(a.view) == 0 || a.index < 0 || a.index >= len(a.files) {
		return false, fmt.Errorf("no current file")
	}
	var tag *tags.Tag
	for i := range a.tagList {
		if a.tagList[i].ID == tagID {
			tag = &a.tagList[i]
		}
	}
	if tag == nil || !tag.Enabled {
		return false, fmt.Errorf("tag %d is not available", tagID)
	}
	id := a.files[a.index].ID
	if id == 0 {
		return false, fmt.Errorf("file is not tracked in the database")
	}
	on, err := a.db.ToggleTag(id, tagID)
	if err != nil {
		return false, err
	}
	if a.assigned[id] == nil {
		a.assigned[id] = map[int]bool{}
	}
	if on {
		a.assigned[id][tagID] = true
	} else {
		delete(a.assigned[id], tagID)
	}
	delete(a.statuses, id)
	a.rebuildLocked() // the current file stays visible even if it no longer matches
	return on, nil
}

// ToggleByShortcut toggles the enabled tag bound to key on the current file.
// handled is false when no tag uses the key (or there is no current file).
func (a *Application) ToggleByShortcut(key string) (handled bool, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t, ok := tags.ByShortcut(a.tagList, strings.ToUpper(key))
	if !ok || len(a.view) == 0 || a.index >= len(a.files) {
		return false, nil
	}
	_, err = a.toggleLocked(t.ID)
	return true, err
}

// TaggedCount returns how many listed files have at least one tag.
func (a *Application) TaggedCount() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	n := 0
	for _, f := range a.files {
		if len(a.assigned[f.ID]) > 0 {
			n++
		}
	}
	return n
}

// ---- files / navigation ----

// ApplyScan reconciles scanned files with the database (keeping tags and
// manual years), then makes them the current list. The previously selected
// file stays selected; on first load the saved session position is restored.
// Safe to call from a goroutine.
func (a *Application) ApplyScan(dir string, files []fileinfo.FileInfo, recursive bool) (database.SyncResult, error) {
	res, err := a.db.SyncFiles(dir, files, recursive)
	if err != nil {
		return res, err
	}
	sdir, sid, sok, _ := a.db.LoadSession()

	a.mu.Lock()
	defer a.mu.Unlock()
	assign, err := a.db.LoadProfileAssignments(a.profileID)
	if err != nil {
		return res, err
	}
	manual, err := a.db.LoadFileVars(a.profileID)
	if err != nil {
		return res, err
	}
	a.manual = manual
	a.assigned = toAssigned(assign)
	var curPath string
	if a.index >= 0 && a.index < len(a.files) && a.dir == dir {
		curPath = a.files[a.index].Path
	}
	a.files, a.dir, a.index = files, dir, 0
	a.pruneFieldsCache(files)
	for i, f := range files {
		if (curPath != "" && f.Path == curPath) || (curPath == "" && sok && sdir == dir && f.ID == sid) {
			a.index = i
			break
		}
	}
	a.invalidateStatusesLocked()
	a.rebuildLocked()
	a.saveSessionLocked()
	slog.Info("scan applied", "dir", dir, "recursive", recursive, "files", len(files),
		"added", res.Added, "modified", res.Modified, "renamed", res.Renamed, "removed", res.Removed)
	return res, nil
}

func (a *Application) saveSessionLocked() {
	if a.dir == "" {
		return
	}
	var id int64
	if a.index >= 0 && a.index < len(a.files) {
		id = a.files[a.index].ID
	}
	_ = a.db.SaveSession(a.dir, id)
}

// Count returns the number of files shown under the current filter.
func (a *Application) Count() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.view)
}

// Total returns the number of scanned files, regardless of the filter.
func (a *Application) Total() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.files)
}

// Position returns the 1-based position within the filtered view and the
// size of the view; position is 0 when the view is empty.
func (a *Application) Position() (int, int) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if len(a.view) == 0 {
		return 0, 0
	}
	return a.viewPosLocked() + 1, len(a.view)
}

func (a *Application) viewPosLocked() int {
	for p, i := range a.view {
		if i == a.index {
			return p
		}
	}
	return 0
}

// Current returns the current file, if any.
func (a *Application) Current() (fileinfo.FileInfo, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if len(a.view) == 0 || a.index < 0 || a.index >= len(a.files) {
		return fileinfo.FileInfo{}, false
	}
	return a.files[a.index], true
}

func (a *Application) Next()  { a.move(func(m []int, cur int) int { return firstAfter(m, cur) }) }
func (a *Application) Prev()  { a.move(func(m []int, cur int) int { return lastBefore(m, cur) }) }
func (a *Application) First() { a.move(func(m []int, cur int) int { return m[0] }) }
func (a *Application) Last()  { a.move(func(m []int, cur int) int { return m[len(m)-1] }) }

func firstAfter(m []int, cur int) int {
	for _, i := range m {
		if i > cur {
			return i
		}
	}
	return cur
}

func lastBefore(m []int, cur int) int {
	for k := len(m) - 1; k >= 0; k-- {
		if m[k] < cur {
			return m[k]
		}
	}
	return cur
}

// move navigates among the files that currently match the filter. Files that
// stopped matching since they were opened (e.g. just tagged while viewing
// "Untagged") are left behind by the first move.
func (a *Application) move(pick func(matching []int, cur int) int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.view) == 0 {
		return
	}
	m := a.matchingLocked()
	if len(m) == 0 {
		return
	}
	if i := pick(m, a.index); i != a.index || !a.inViewMatching(m) {
		a.index = i
		a.rebuildFromLocked(m) // m is still valid: only the index changed
		a.saveSessionLocked()
	}
}

func (a *Application) inViewMatching(m []int) bool {
	for _, i := range m {
		if i == a.index {
			return true
		}
	}
	return false
}

// GoTo makes the file at path current if it is in the filtered view.
func (a *Application) GoTo(path string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, i := range a.view {
		if a.files[i].Path == path {
			a.index = i
			a.saveSessionLocked()
			return true
		}
	}
	return false
}

// Neighbors returns up to before/after files around the current one in the
// filtered view (for the filmstrip), in order, plus the index of the current
// file within the returned slice.
func (a *Application) Neighbors(before, after int) ([]fileinfo.FileInfo, int) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if len(a.view) == 0 {
		return nil, 0
	}
	p := a.viewPosLocked()
	lo, hi := p-before, p+after
	if lo < 0 {
		lo = 0
	}
	if hi > len(a.view)-1 {
		hi = len(a.view) - 1
	}
	var out []fileinfo.FileInfo
	for k := lo; k <= hi; k++ {
		out = append(out, a.files[a.view[k]])
	}
	return out, p - lo
}
