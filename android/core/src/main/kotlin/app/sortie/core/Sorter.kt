package app.sortie.core

import java.io.File
import java.util.concurrent.locks.ReentrantReadWriteLock
import kotlin.concurrent.read
import kotlin.concurrent.write

/** Selects which files are browsed. */
enum class Filter {
    ALL, UNTAGGED, TAGGED, UNRESOLVED, READY, ALREADY_COPIED, CONFLICTS, ERRORS;

    /** Whether the filter depends on destination checks, which look at (and hash) destination files. */
    val needsStatus get() = this == READY || this == ALREADY_COPIED || this == CONFLICTS || this == ERRORS
}

/** The value of one variable for one file. */
data class VarInfo(val name: String, val value: VarValue) {
    /** The value comes from a manual override. */
    val manual get() = value.source == SourceKind.MANUAL
}

/** [Sorter.refreshStatuses] found its inputs changed while it was running: simply run it again. */
class StaleException : Exception("destination statuses became stale while computing")

/**
 * UI-independent application state: the file list, the filtered view, the current position, the
 * active profile (tags, variables, destination), variable values, tag assignments, destination
 * checks and copy orchestration. Methods may read the disk or the database, so call them off the
 * main thread.
 *
 * @param home what "~" in a profile path expands to (the shared storage root on Android)
 * @param fields metadata reader: path → lower-case field name → value
 */
class Sorter(
    private val store: Store,
    private val home: String,
    private val fields: (String) -> Map<String, String> = { emptyMap() },
) {
    private class FileStatus(val ready: Int, val already: Int, val conflict: Int, val errors: Int, val total: Int)
    private class CheckInput(val res: Resolver, val tags: List<Tag>, val values: Map<String, String>, val gen: Int)
    private class FieldsEntry(val size: Long, val mtime: Long, val fields: Map<String, String>)

    private val lock = ReentrantReadWriteLock()
    private val updateLock = Any()

    private var files: List<FileEntry> = emptyList()
    private var index = 0 // index into files of the current file
    private var dir = ""

    private var view: List<Int> = emptyList() // indices into files shown under the current filter
    private var filter = Filter.ALL
    private var statuses = HashMap<Long, FileStatus>()
    private var statusKnown = false
    private var statusGen = 0 // bumped whenever statuses are invalidated

    private var assigned = HashMap<Long, MutableSet<Int>>()
    private var tagList: List<Tag> = emptyList()

    private var profileId = "default"
    private var prof = Profile(name = "Default", tags = emptyList())
    private var baseDir = home
    private var varSet = VarSet(emptyList())
    private var manual = HashMap<Long, MutableMap<String, String>>()

    private val fieldsCache = HashMap<String, FieldsEntry>()

    private var resolver = Resolver()
    private val checker = Checker(2)
    private val engine = CopyEngine(checker, store)

    /** How many copies were left IN_PROGRESS by a previous run that was killed. Copying again resumes them. */
    val interruptedAtStart: Int = runCatching { store.markInterrupted() }.getOrDefault(0)

    // ---- profile ----

    /**
     * Validates [p] and makes it the active profile: its variables, tags (synchronised with the
     * store by name), destination and per-profile state take effect immediately. If validation
     * fails nothing changes.
     */
    fun applyProfile(id: String, p: Profile, baseDir: String = home) {
        ProfileStore.validId(id)
        p.validate()
        val res = p.resolved(home, baseDir)
        try {
            Resolver.validateRoot(res.destinationRoot)
        } catch (e: SorterException) {
            throw ProfileException("destination_root", e.message ?: "invalid")
        }
        val set = p.varSet()
        val ids = store.syncProfileTags(id, p.toTags())
        store.pruneFileVars(id, set.names)
        val assign = store.loadAssignments(id)
        val man = store.loadFileVars(id)
        lock.write {
            profileId = id
            prof = p
            this.baseDir = baseDir
            varSet = set
            tagList = p.toTags(ids)
            assigned = toAssigned(assign)
            manual = toManual(man)
            resolver = Resolver(res.destinationRoot, p.destinationTemplate.trim().ifEmpty { PathTemplate.DEFAULT }, set.names)
            invalidateStatuses()
            rebuild()
        }
    }

    /** Removes the stored tagging and manual values of a profile being deleted. The active one cannot be removed. */
    fun deleteProfileData(id: String) {
        if (lock.read { profileId } == id) throw SorterException("profile \"$id\" is in use")
        store.deleteProfile(id)
    }

    /** How many files have tags assigned in profile [id]; deleting that profile discards them. */
    fun taggedFileCount(id: String): Int = store.countTaggedFiles(id)

    /**
     * Applies [change] to the active profile and, if the result is valid, makes it active. The
     * caller persists it when it should outlive the session.
     */
    fun updateProfile(change: (Profile) -> Profile): Profile = synchronized(updateLock) {
        val (id, base, cur) = lock.read { Triple(profileId, baseDir, prof) }
        val next = change(cur)
        applyProfile(id, next, base)
        next
    }

    fun profile(): Profile = lock.read { prof }

    fun profileId(): String = lock.read { profileId }

    /** The profile's input directory with "~" and relative paths resolved; "" if none is configured. */
    fun inputDir(): String = lock.read { prof.resolved(home, baseDir).inputDirectory }

    /** The directory of the last applied scan; "" before the first one. */
    fun scannedDir(): String = lock.read { dir }

    // ---- variables ----

    fun varNames(): List<String> = lock.read { varSet.names }

    /** Resolves every variable for [f] (manual > the profile's sources). */
    fun varsOf(f: FileEntry): List<VarInfo> = lock.read {
        val vals = varSet.resolveAll(fileContext(f))
        varSet.names.map { VarInfo(it, vals.getValue(it)) }
    }

    /** Only the resolved values by name, for templates. */
    private fun values(f: FileEntry): Map<String, String> {
        val out = HashMap<String, String>()
        for ((n, v) in varSet.resolveAll(fileContext(f))) if (v.ok) out[n] = v.value
        // {parent}: name of the folder the file lives in. It is reserved, so no user variable set it.
        File(f.path).parentFile?.name?.takeIf { it.isNotEmpty() }?.let { out["parent"] = it }
        return out
    }

    private fun allResolved(f: FileEntry) = varSet.resolveAll(fileContext(f)).values.all { it.ok }

    private fun fileContext(f: FileEntry): FileContext {
        var rel = ""
        val parent = f.parentPath
        if (dir.isNotEmpty() && parent.startsWith(dir.trimEnd('/') + "/")) rel = parent.substring(dir.trimEnd('/').length + 1)
        return FileContext(f.name, rel, f.modTimeMs, manual[f.id] ?: emptyMap()) { fieldsFor(f) }
    }

    /** Reads metadata once per file version (path, size, mtime). */
    private fun fieldsFor(f: FileEntry): Map<String, String> {
        synchronized(fieldsCache) { fieldsCache[f.path] }?.let { if (it.size == f.size && it.mtime == f.modTimeMs) return it.fields }
        val read = try {
            fields(f.path)
        } catch (_: Exception) {
            emptyMap()
        }
        synchronized(fieldsCache) { fieldsCache[f.path] = FieldsEntry(f.size, f.modTimeMs, read) }
        return read
    }

    /** The metadata fields of [f], as variable sources see them. */
    fun metadataOf(f: FileEntry): Map<String, String> = fieldsFor(f)

    /**
     * Stores a manual value for variable [name] of [f]; an empty value removes it. The value must
     * satisfy the variable's validation rules.
     */
    fun setManualVar(f: FileEntry, name: String, value: String) = lock.write {
        val v = value.trim()
        if (v.isNotEmpty()) {
            try {
                varSet.check(name, v)
            } catch (e: SorterException) {
                throw SorterException("$name: ${e.message}")
            }
        }
        if (f.id == 0L) throw SorterException("${f.name} is not tracked in the database")
        if (!varSet.has(name)) throw SorterException("unknown variable \"$name\"")
        store.setFileVar(f.id, profileId, name, v)
        if (v.isEmpty()) manual[f.id]?.remove(name) else manual.getOrPut(f.id) { HashMap() }[name] = v
        statuses.remove(f.id)
        rebuild()
    }

    // ---- destinations ----

    fun resolver(): Resolver = lock.read { resolver }

    /**
     * Evaluates the destination of every tag assigned to [f], in the display order of the tags.
     * It reads the filesystem and may hash files.
     */
    fun checks(f: FileEntry, cancel: Cancel): List<DestCheck> {
        val snap = lock.read {
            val mine = assigned[f.id] ?: emptySet()
            CheckInput(resolver, tagList.filter { it.id in mine }, values(f), statusGen)
        }
        if (snap.tags.isEmpty()) return emptyList()
        val out = checker.check(snap.res, f.path, f.name, snap.values, snap.tags, cancel)
        if (!cancel.cancelled) {
            lock.write {
                if (statusGen == snap.gen) {
                    statuses[f.id] = summarise(out.filterIndexed { i, _ -> snap.tags[i].enabled }.map { it.status })
                    if (filter.needsStatus && statusKnown) rebuild()
                }
            }
        }
        return out
    }

    // ---- copying ----

    /** Temporary files that interrupted copies left in destination folders and that still exist. */
    fun leftoverTempFiles(): List<String> =
        runCatching { store.interruptedTempFiles() }.getOrDefault(emptyList())
            .distinct().filter { CopyEngine.isTempFile(it) && File(it).isFile }

    /** Deletes the files [leftoverTempFiles] reports; only call it after the user agreed. */
    fun removeLeftoverTempFiles(): Int = leftoverTempFiles().count { CopyEngine.removeTempFile(it) }

    /** One job per (file, enabled assigned tag): for the current file only, or for every listed file. */
    fun copyJobs(all: Boolean): List<CopyJob> = lock.read {
        val src = when {
            all -> files
            view.isNotEmpty() && index in files.indices -> listOf(files[index])
            else -> emptyList()
        }
        src.flatMap { f ->
            val mine = assigned[f.id]
            if (mine.isNullOrEmpty()) return@flatMap emptyList()
            val values = values(f)
            tagList.filter { it.enabled && it.id in mine }.map { CopyJob(f.id, f.path, f.name, f.size, f.modTimeMs, values, it) }
        }
    }

    /** Evaluates jobs without writing anything. */
    fun planCopy(jobs: List<CopyJob>, cancel: Cancel, onDone: ((Int, Int) -> Unit)? = null): List<DestCheck> =
        engine.plan(resolver(), jobs, cancel, 2, onDone)

    /** Copies the jobs. Classification (tags) is never modified, so failed or cancelled jobs can simply be run again. */
    fun runCopy(jobs: List<CopyJob>, opts: CopyOptions, cancel: Cancel): CopySummary {
        val sum = engine.run(resolver(), jobs, opts, cancel)
        applyCopyResults(sum)
        return sum
    }

    fun history(f: FileEntry): List<HistoryEntry> = store.history(f.id)

    // ---- tags ----

    /** The tags in display order. */
    fun tags(): List<Tag> = lock.read { tagList }

    /** The ids of tags assigned to [f]. */
    fun tagsOf(f: FileEntry): Set<Int> = lock.read { assigned[f.id]?.toSet() ?: emptySet() }

    /** Toggles an enabled tag on the current file; other tags are kept. Returns whether it is assigned now. */
    fun toggleTag(tagId: Int): Boolean = lock.write {
        if (view.isEmpty() || index !in files.indices) throw SorterException("no current file")
        val tag = tagList.firstOrNull { it.id == tagId }
        if (tag == null || !tag.enabled) throw SorterException("this tag is not available")
        val id = files[index].id
        if (id == 0L) throw SorterException("file is not tracked in the database")
        val on = store.toggleTag(id, tagId)
        val set = assigned.getOrPut(id) { HashSet() }
        if (on) set += tagId else set -= tagId
        statuses.remove(id)
        rebuild() // the current file stays visible even if it no longer matches
        on
    }

    /** Toggles the enabled tag bound to [key] on the current file; false when no tag uses the key. */
    fun toggleByShortcut(key: String): Boolean {
        val t = lock.read { Tags.byShortcut(tagList, key) } ?: return false
        if (current() == null) return false
        toggleTag(t.id)
        return true
    }

    /** How many listed files have at least one tag. */
    fun taggedCount(): Int = lock.read { files.count { !assigned[it.id].isNullOrEmpty() } }

    // ---- files / navigation ----

    /**
     * Reconciles scanned files with the store (keeping tags and manual values), then makes them
     * the current list. The previously selected file stays selected; on first load the saved
     * session position is restored.
     */
    fun applyScan(dir: String, scanned: List<FileEntry>, recursive: Boolean): SyncResult {
        val (synced, res) = store.syncFiles(dir, scanned, recursive)
        val session = store.loadSession()
        lock.write {
            assigned = toAssigned(store.loadAssignments(profileId))
            manual = toManual(store.loadFileVars(profileId))
            val curPath = if (index in files.indices && this.dir == dir) files[index].path else ""
            files = synced
            this.dir = dir
            index = 0
            val keep = synced.mapTo(HashSet()) { it.path }
            synchronized(fieldsCache) { fieldsCache.keys.retainAll(keep) }
            val i = synced.indexOfFirst {
                if (curPath.isNotEmpty()) it.path == curPath else session != null && session.first == dir && it.id == session.second
            }
            if (i >= 0) index = i
            invalidateStatuses()
            rebuild()
            saveSession()
        }
        return res
    }

    private fun saveSession() {
        if (dir.isEmpty()) return
        runCatching { store.saveSession(dir, files.getOrNull(index)?.id ?: 0) }
    }

    /** The number of files shown under the current filter. */
    fun count(): Int = lock.read { view.size }

    /** The number of scanned files, regardless of the filter. */
    fun total(): Int = lock.read { files.size }

    /** The 1-based position within the filtered view and the size of the view; position is 0 when the view is empty. */
    fun position(): Pair<Int, Int> = lock.read { if (view.isEmpty()) 0 to 0 else viewPos() + 1 to view.size }

    private fun viewPos(): Int = view.indexOf(index).coerceAtLeast(0)

    fun current(): FileEntry? = lock.read { if (view.isEmpty()) null else files.getOrNull(index) }

    fun next() = move { m, cur -> m.firstOrNull { it > cur } ?: cur }
    fun prev() = move { m, cur -> m.lastOrNull { it < cur } ?: cur }
    fun first() = move { m, _ -> m.first() }
    fun last() = move { m, _ -> m.last() }

    /**
     * Navigates among the files that currently match the filter. Files that stopped matching
     * since they were opened (e.g. just tagged while viewing "Untagged") are left behind by the
     * first move.
     */
    private fun move(pick: (List<Int>, Int) -> Int) = lock.write {
        if (view.isEmpty()) return@write
        val m = matching()
        if (m.isEmpty()) return@write
        val i = pick(m, index)
        if (i != index || index !in m) {
            index = i
            rebuildFrom(m)
            saveSession()
        }
    }

    /** Makes the file at [path] current if it is in the filtered view. */
    fun goTo(path: String): Boolean = lock.write {
        val i = view.firstOrNull { files[it].path == path } ?: return@write false
        index = i
        saveSession()
        true
    }

    /**
     * Up to [before]/[after] files around the current one in the filtered view (for the
     * filmstrip), in order, plus the index of the current file within the returned list.
     */
    fun neighbors(before: Int, after: Int): Pair<List<FileEntry>, Int> = lock.read {
        if (view.isEmpty()) return@read emptyList<FileEntry>() to 0
        val p = viewPos()
        val lo = maxOf(0, p - before)
        val hi = minOf(view.size - 1, p + after)
        (lo..hi).map { files[view[it]] } to p - lo
    }

    // ---- filter ----

    /**
     * Changes the filter. If the current file does not match, the nearest matching file becomes
     * current. Status-based filters show nothing until [statusesKnown] (see [refreshStatuses]).
     */
    fun setFilter(f: Filter) = lock.write {
        filter = f
        val m = matching()
        if (m.isNotEmpty() && index !in m) {
            index = m.firstOrNull { it > index } ?: m.last { it < index }
            saveSession()
        }
        rebuildFrom(m)
    }

    fun currentFilter(): Filter = lock.read { filter }

    /** Whether destination statuses have been computed. */
    fun statusesKnown(): Boolean = lock.read { statusKnown }

    /**
     * Computes the destination status of every tagged file (this may hash files) and enables the
     * status-based filters. Throws [StaleException] when the inputs changed meanwhile and
     * [CancelledException] on cancel; both leave the previous state untouched.
     */
    fun refreshStatuses(cancel: Cancel, onProgress: ((Int, Int) -> Unit)? = null) {
        val gen = lock.read { statusGen }
        val jobs = copyJobs(true)
        val plan = engine.plan(resolver(), jobs, cancel, 2, onProgress)
        cancel.check()
        val per = HashMap<Long, MutableList<DestStatus>>()
        jobs.forEachIndexed { i, j -> per.getOrPut(j.fileId) { ArrayList() } += plan[i].status }
        lock.write {
            if (statusGen != gen) throw StaleException()
            statuses = HashMap()
            for ((id, sts) in per) statuses[id] = summarise(sts)
            statusKnown = true
            val m = matching()
            if (filter.needsStatus && m.isNotEmpty() && index !in m) {
                index = m.firstOrNull { it > index } ?: m.lastOrNull { it < index } ?: m[0]
            }
            rebuildFrom(m)
        }
    }

    private fun invalidateStatuses() {
        statuses = HashMap()
        statusKnown = false
        statusGen++
    }

    private fun summarise(sts: List<DestStatus>) = FileStatus(
        ready = sts.count { it == DestStatus.READY },
        already = sts.count { it == DestStatus.ALREADY_COPIED },
        conflict = sts.count { it == DestStatus.CONFLICT },
        errors = sts.count { it == DestStatus.ERROR || it == DestStatus.INVALID_PATH },
        total = sts.size,
    )

    private fun matches(i: Int): Boolean {
        val f = files[i]
        when (filter) {
            Filter.ALL -> return true
            Filter.UNTAGGED -> return assigned[f.id].isNullOrEmpty()
            Filter.TAGGED -> return !assigned[f.id].isNullOrEmpty()
            Filter.UNRESOLVED -> return !allResolved(f)
            else -> {}
        }
        val st = statuses[f.id] ?: return false
        if (st.total == 0) return false
        return when (filter) {
            Filter.READY -> st.ready > 0
            Filter.ALREADY_COPIED -> st.already == st.total
            Filter.CONFLICTS -> st.conflict > 0
            Filter.ERRORS -> st.errors > 0
            else -> false
        }
    }

    private fun matching(): List<Int> = files.indices.filter { matches(it) }

    /**
     * Recomputes the view. The current file is pinned into the view even if it no longer matches
     * (so tagging a file in the "Untagged" view does not make it vanish under the user's hands).
     */
    private fun rebuild() = rebuildFrom(matching())

    private fun rebuildFrom(m: List<Int>) {
        view = if (m.isNotEmpty() && index in files.indices && index !in m) (m + index).sorted() else m
    }

    /** Updates file statuses from a finished copy run. */
    private fun applyCopyResults(sum: CopySummary) {
        val per = HashMap<Long, MutableList<DestStatus>>()
        for (r in sum.results) {
            val st = when (r.status) {
                CopyStatus.COPIED, CopyStatus.REPLACED, CopyStatus.RENAMED, CopyStatus.ALREADY_COPIED -> DestStatus.ALREADY_COPIED
                CopyStatus.CONFLICT_SKIPPED -> DestStatus.CONFLICT
                CopyStatus.FAILED -> DestStatus.ERROR
                else -> continue
            }
            per.getOrPut(r.job.fileId) { ArrayList() } += st
        }
        lock.write {
            for ((id, sts) in per) {
                val old = statuses[id]
                if (old != null && old.total > sts.size) continue // partial run: keep the re-checked summary rather than guess
                statuses[id] = summarise(sts)
            }
            rebuild()
        }
    }

    private fun toAssigned(m: Map<Long, Set<Int>>) = HashMap<Long, MutableSet<Int>>().apply { for ((k, v) in m) put(k, v.toHashSet()) }

    private fun toManual(m: Map<Long, Map<String, String>>) =
        HashMap<Long, MutableMap<String, String>>().apply { for ((k, v) in m) put(k, HashMap(v)) }
}
