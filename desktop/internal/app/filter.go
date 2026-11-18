package app

import (
	"context"
	"errors"

	"sortie/internal/copier"
	"sortie/internal/destination"
	"sortie/internal/tags"
)

// Filter selects which files are browsed (PLAN §15).
type Filter int

const (
	FilterAll Filter = iota
	FilterUntagged
	FilterTagged
	FilterUnresolved
	FilterReady
	FilterAlreadyCopied
	FilterConflicts
	FilterErrors
)

// AllFilters lists the filters in display order.
var AllFilters = []Filter{FilterAll, FilterUntagged, FilterTagged, FilterUnresolved,
	FilterReady, FilterAlreadyCopied, FilterConflicts, FilterErrors}

var filterLabels = map[Filter]string{
	FilterAll: "All files", FilterUntagged: "Untagged files", FilterTagged: "Tagged files",
	FilterUnresolved: "Unresolved variables", FilterReady: "Ready to copy",
	FilterAlreadyCopied: "Already copied", FilterConflicts: "Conflicts", FilterErrors: "Files with errors",
}

func (f Filter) String() string { return filterLabels[f] }

// NeedsStatus reports whether the filter depends on destination checks,
// which require looking at (and hashing) destination files.
func (f Filter) NeedsStatus() bool {
	return f == FilterReady || f == FilterAlreadyCopied || f == FilterConflicts || f == FilterErrors
}

// FilterByLabel is the inverse of String (for UI selects).
func FilterByLabel(s string) Filter {
	for f, l := range filterLabels {
		if l == s {
			return f
		}
	}
	return FilterAll
}

// fileStatus summarises the destination checks of one file's enabled tags.
type fileStatus struct{ Ready, Already, Conflict, Errors, Total int }

func summarise(statuses []destination.Status) fileStatus {
	var s fileStatus
	for _, st := range statuses {
		s.Total++
		switch st {
		case destination.StatusReady:
			s.Ready++
		case destination.StatusAlreadyCopied:
			s.Already++
		case destination.StatusConflict:
			s.Conflict++
		case destination.StatusError, destination.StatusInvalidPath:
			s.Errors++
		}
	}
	return s
}

// SetFilter changes the filter. If the current file does not match, the
// nearest matching file becomes current. Status-based filters show nothing
// until StatusesKnown() (see RefreshStatuses).
func (a *Application) SetFilter(f Filter) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.filter = f
	m := a.matchingLocked()
	if len(m) > 0 && !a.inViewMatching(m) {
		if n := firstAfter(m, a.index); n != a.index {
			a.index = n
		} else {
			a.index = lastBefore(m, a.index)
		}
		a.saveSessionLocked()
	}
	a.rebuildFromLocked(m)
}

// CurrentFilter returns the active filter.
func (a *Application) CurrentFilter() Filter {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.filter
}

// StatusesKnown reports whether destination statuses have been computed.
func (a *Application) StatusesKnown() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.statusKnown
}

// ErrStale is returned by RefreshStatuses when the inputs changed while it
// was running; the caller should simply run it again.
var ErrStale = errors.New("destination statuses became stale while computing")

func (a *Application) invalidateStatusesLocked() {
	a.statuses = map[int64]fileStatus{}
	a.statusKnown = false
	a.statusGen++
}

// matchLocked evaluates the active filter for files[i].
func (a *Application) matchLocked(i int) bool {
	f := a.files[i]
	switch a.filter {
	case FilterAll:
		return true
	case FilterUntagged:
		return len(a.assigned[f.ID]) == 0
	case FilterTagged:
		return len(a.assigned[f.ID]) > 0
	case FilterUnresolved:
		return !a.allResolvedLocked(f)
	}
	st, ok := a.statuses[f.ID]
	if !ok || st.Total == 0 {
		return false
	}
	switch a.filter {
	case FilterReady:
		return st.Ready > 0
	case FilterAlreadyCopied:
		return st.Already == st.Total
	case FilterConflicts:
		return st.Conflict > 0
	case FilterErrors:
		return st.Errors > 0
	}
	return false
}

func (a *Application) matchingLocked() []int {
	out := make([]int, 0, len(a.files))
	for i := range a.files {
		if a.matchLocked(i) {
			out = append(out, i)
		}
	}
	return out
}

// rebuildLocked recomputes the view. The current file is pinned into the view
// even if it no longer matches (so tagging a file in the "Untagged" view does
// not make it vanish under the user's hands).
func (a *Application) rebuildLocked() { a.rebuildFromLocked(a.matchingLocked()) }

// rebuildFromLocked is rebuildLocked for callers that already computed
// matchingLocked() and know nothing that affects matching changed since, so
// the O(N) scan (which may resolve variables per file) is not repeated.
func (a *Application) rebuildFromLocked(m []int) {
	if len(m) > 0 && a.index >= 0 && a.index < len(a.files) && !a.inViewMatching(m) {
		pos := 0
		for pos < len(m) && m[pos] < a.index {
			pos++
		}
		m = append(m[:pos], append([]int{a.index}, m[pos:]...)...)
	}
	a.view = m
}

// recordChecks stores the destination summary of one file (enabled tags only).
// gen is the statusGen the checks were computed under; if the statuses were
// invalidated meanwhile (profile, destination, scan or tag change) the result
// refers to stale inputs and is dropped.
func (a *Application) recordChecks(gen int, fileID int64, assigned []tags.Tag, checks []destination.Check) {
	var sts []destination.Status
	for i, c := range checks {
		if assigned[i].Enabled {
			sts = append(sts, c.Status)
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.statusGen != gen {
		return
	}
	if a.statuses == nil {
		a.statuses = map[int64]fileStatus{}
	}
	a.statuses[fileID] = summarise(sts)
	if a.filter.NeedsStatus() && a.statusKnown {
		a.rebuildLocked()
	}
}

// RefreshStatuses computes the destination status of every tagged file (this
// may hash files, so run it off the UI thread). It enables the status-based
// filters. Cancelling ctx leaves the previous state untouched.
func (a *Application) RefreshStatuses(ctx context.Context, onProgress func(done, total int)) error {
	a.mu.RLock()
	gen := a.statusGen
	a.mu.RUnlock()
	jobs := a.CopyJobs(true)
	plan := a.engine.Plan(ctx, a.Resolver(), jobs, 2, onProgress)
	if err := ctx.Err(); err != nil {
		return err
	}
	per := map[int64][]destination.Status{}
	for i, j := range jobs {
		per[j.FileID] = append(per[j.FileID], plan[i].Status)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.statusGen != gen {
		return ErrStale
	}
	a.statuses = map[int64]fileStatus{}
	for id, sts := range per {
		a.statuses[id] = summarise(sts)
	}
	a.statusKnown = true
	m := a.matchingLocked()
	if a.filter.NeedsStatus() && len(m) > 0 && !a.inViewMatching(m) {
		a.index = firstAfter(m, a.index)
		if !a.inViewMatching(m) {
			a.index = lastBefore(m, a.index)
		}
		if !a.inViewMatching(m) {
			a.index = m[0]
		}
	}
	a.rebuildFromLocked(m)
	return nil
}

// applyCopyResults updates file statuses from a finished copy run.
func (a *Application) applyCopyResults(sum copier.Summary) {
	per := map[int64][]destination.Status{}
	for _, r := range sum.Results {
		var st destination.Status
		switch r.Status {
		case copier.StatusCopied, copier.StatusReplaced, copier.StatusRenamed, copier.StatusAlreadyCopied:
			st = destination.StatusAlreadyCopied
		case copier.StatusConflictSkip:
			st = destination.StatusConflict
		case copier.StatusFailed:
			st = destination.StatusError
		default:
			continue
		}
		per[r.Job.FileID] = append(per[r.Job.FileID], st)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.statuses == nil {
		a.statuses = map[int64]fileStatus{}
	}
	for id, sts := range per {
		if old, ok := a.statuses[id]; ok && old.Total > len(sts) {
			continue // partial run: keep the (re-checked) summary rather than guess
		}
		a.statuses[id] = summarise(sts)
	}
	a.rebuildLocked()
}
