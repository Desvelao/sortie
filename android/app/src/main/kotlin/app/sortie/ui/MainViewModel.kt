package app.sortie.ui

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import app.sortie.App
import app.sortie.R
import app.sortie.core.Cancel
import app.sortie.core.CancelledException
import app.sortie.core.DestCheck
import app.sortie.core.allBlocked
import app.sortie.core.FileEntry
import app.sortie.core.Filter
import app.sortie.core.HistoryEntry
import app.sortie.core.Profile
import app.sortie.core.ProfileYaml
import app.sortie.core.ScanOptions
import app.sortie.core.Scanner
import app.sortie.core.StaleException
import app.sortie.core.Tag
import app.sortie.core.VarInfo
import app.sortie.data.Storage
import java.io.File
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

data class ProfileItem(val id: String, val name: String, val error: String?)

enum class Screen { SORTER, PROFILES, EDITOR, SETTINGS }

data class UiState(
    val ready: Boolean = false,
    val scanning: Boolean = false,
    val screen: Screen = Screen.SORTER,
    val profileId: String = "",
    val profileName: String = "",
    val profiles: List<ProfileItem> = emptyList(),
    val inputDir: String = "",
    val destRoot: String = "",
    val conflictPolicy: String = "",
    val current: FileEntry? = null,
    val position: Int = 0,
    val count: Int = 0,
    val total: Int = 0,
    val tagged: Int = 0,
    val tags: List<Tag> = emptyList(),
    val assigned: Set<Int> = emptySet(),
    val checks: List<DestCheck> = emptyList(),
    val vars: List<VarInfo> = emptyList(),
    val filter: Filter = Filter.ALL,
    val statusesKnown: Boolean = false,
    val statusProgress: Pair<Int, Int>? = null,
    val strip: List<FileEntry> = emptyList(),
    val stripIndex: Int = 0,
    val showFilmstrip: Boolean = true,
    val skipHidden: Boolean = true,
    val cacheMb: Int = 100,
    /** +1 / -1: which way the last move went, for the slide animation. */
    val direction: Int = 1,
    val message: String? = null,
    val interrupted: Int = 0,
    val leftovers: List<String> = emptyList(),
    /** The profile being edited: its id ("" for none) and the text it had when the editor opened. */
    val editingId: String = "",
    val editingProfile: Profile? = null,
    val editingText: String = "",
) {
    /**
     * True when every tag assigned to the current file has an invalid or unresolved destination, so copying it
     * can only fail. Stays false while the checks are still running.
     */
    val currentDestinationBlocked: Boolean
        get() {
            val assignedTags = tags.filter { it.enabled && it.id in assigned }
            val mine = assignedTags.mapNotNull { t -> checks.firstOrNull { it.tagId == t.id } }
            return assignedTags.isNotEmpty() && mine.size == assignedTags.size && mine.allBlocked()
        }
}

@OptIn(ExperimentalCoroutinesApi::class)
class MainViewModel(app: Application) : AndroidViewModel(app) {
    private val c = (app as App).container
    private val sorter get() = c.sorter
    val copy get() = c.copy
    val thumbnails get() = c.thumbnails

    private val _ui = MutableStateFlow(UiState(showFilmstrip = c.prefs.showFilmstrip, skipHidden = c.prefs.skipHidden, cacheMb = c.prefs.thumbnailCacheMb))
    val ui: StateFlow<UiState> = _ui

    /** Everything that changes the sorter runs here, one at a time and in order. */
    private val serial = Dispatchers.IO.limitedParallelism(1)
    private var checksJob: Job? = null
    private var checksCancel: Cancel? = null
    private var statusJob: Job? = null
    private var statusCancel: Cancel? = null
    private var started = false

    private fun str(id: Int, vararg args: Any) = getApplication<Application>().getString(id, *args)

    private fun run(block: suspend () -> Unit) = viewModelScope.launch(serial) {
        try {
            block()
        } catch (e: Exception) {
            say(e.message ?: e.toString())
        }
        refresh()
    }

    fun say(text: String) = _ui.update { it.copy(message = text) }

    fun messageShown() = _ui.update { it.copy(message = null) }

    /** Loads the active profile and its folder. Called once storage access is granted. */
    fun start() {
        if (started) return
        started = true
        run {
            // The first use of `copy` creates the sorter, which opens the database: never on the main thread.
            copy.onFinished = { viewModelScope.launch(serial) { refresh() } }
            if (c.profiles.list().isEmpty()) c.profiles.create(str(R.string.default_profile_name))
            val entries = c.profiles.list()
            val pick = entries.firstOrNull { it.id == c.prefs.activeProfile && it.profile != null } ?: entries.firstOrNull { it.profile != null }
            if (pick != null) activate(pick.id) else say(entries.firstOrNull()?.error ?: "")
            val interrupted = sorter.interruptedAtStart
            val leftovers = sorter.leftoverTempFiles()
            refresh() // so the first screen shown is the real one, not the "no folder" empty state
            _ui.update { it.copy(ready = true, interrupted = interrupted, leftovers = leftovers) }
            c.thumbnails.prune()
        }
    }

    private suspend fun activate(id: String) {
        val p = c.profiles.load(id)
        sorter.applyProfile(id, p, Storage.home)
        c.prefs.activeProfile = id
        scan()
    }

    private suspend fun scan() {
        val dir = sorter.inputDir()
        if (dir.isEmpty()) {
            sorter.applyScan("", emptyList(), false)
            return
        }
        _ui.update { it.copy(scanning = true) }
        try {
            val recursive = sorter.profile().recursive
            val res = Scanner.scan(File(dir), ScanOptions(recursive, c.prefs.skipHidden), Cancel()) { Storage.mime(it) }
            sorter.applyScan(dir, res.files, recursive)
            if (res.skipped.isNotEmpty()) say(str(R.string.scan_skipped, res.skipped.size))
        } finally {
            _ui.update { it.copy(scanning = false) }
        }
    }

    /** Publishes the sorter's state and re-checks the destinations of the current file. */
    private fun refresh() {
        val cur = sorter.current()
        val (pos, count) = sorter.position()
        val (strip, at) = sorter.neighbors(15, 15)
        val p = sorter.profile()
        val entries = c.profiles.list().map { ProfileItem(it.id, it.name, it.error) }
        val sameFile = _ui.value.current?.id == cur?.id
        _ui.update {
            it.copy(
                profileId = sorter.profileId(), profileName = p.name, profiles = entries,
                inputDir = sorter.inputDir(), destRoot = sorter.resolver().root, conflictPolicy = p.conflictPolicy,
                current = cur, position = pos, count = count, total = sorter.total(), tagged = sorter.taggedCount(),
                tags = sorter.tags(), assigned = cur?.let { f -> sorter.tagsOf(f) } ?: emptySet(),
                vars = cur?.let { f -> sorter.varsOf(f) } ?: emptyList(),
                checks = if (sameFile) it.checks else emptyList(),
                filter = sorter.currentFilter(), statusesKnown = sorter.statusesKnown(),
                strip = strip, stripIndex = at,
            )
        }
        checksCancel?.cancel()
        checksJob?.cancel()
        if (cur != null) {
            val cancel = Cancel().also { checksCancel = it }
            checksJob = viewModelScope.launch(Dispatchers.IO) {
                val checks = sorter.checks(cur, cancel)
                if (!cancel.cancelled) _ui.update { if (it.current?.id == cur.id) it.copy(checks = checks) else it }
            }
        }
        ensureStatuses()
    }

    /** Status filters need every tagged file checked against its destination; do that in the background. */
    private fun ensureStatuses() {
        if (!sorter.currentFilter().needsStatus || sorter.statusesKnown() || statusJob?.isActive == true) return
        val cancel = Cancel().also { statusCancel = it }
        statusJob = viewModelScope.launch(Dispatchers.IO) {
            var again = true
            while (again && !cancel.cancelled) {
                again = false
                try {
                    sorter.refreshStatuses(cancel) { d, t -> _ui.update { it.copy(statusProgress = d to t) } }
                } catch (_: StaleException) {
                    again = true
                } catch (_: CancelledException) {
                } catch (e: Exception) {
                    say(e.message ?: e.toString())
                }
            }
            _ui.update { it.copy(statusProgress = null) }
            withContext(serial) { refresh() }
        }
    }

    // ---- navigation ----

    private fun move(direction: Int, action: () -> Unit) = run {
        _ui.update { it.copy(direction = direction) }
        action()
    }

    fun next() = move(1) { sorter.next() }
    fun prev() = move(-1) { sorter.prev() }
    fun first() = move(-1) { sorter.first() }
    fun last() = move(1) { sorter.last() }

    fun goTo(f: FileEntry) = move(if ((ui.value.strip.indexOf(f)) >= ui.value.stripIndex) 1 else -1) { sorter.goTo(f.path) }

    fun setFilter(f: Filter) = run { sorter.setFilter(f) }

    fun rescan() = run { scan() }

    // ---- classification ----

    fun toggleTag(id: Int) = run { sorter.toggleTag(id) }

    /** A hardware key: toggles the tag bound to it. Returns immediately; unknown keys are ignored. */
    fun shortcut(key: String) = run { sorter.toggleByShortcut(key) }

    fun setManual(name: String, value: String) = run { sorter.current()?.let { sorter.setManualVar(it, name, value) } }

    suspend fun history(f: FileEntry): List<HistoryEntry> = withContext(Dispatchers.IO) { runCatching { sorter.history(f) }.getOrDefault(emptyList()) }

    suspend fun metadata(f: FileEntry): Map<String, String> = withContext(Dispatchers.IO) { sorter.metadataOf(f) }

    // ---- profiles ----

    fun open(screen: Screen) = _ui.update { it.copy(screen = screen) }

    fun selectProfile(id: String) = run {
        if (copy.busy) throw IllegalStateException(str(R.string.busy_copying))
        activate(id)
    }

    /** Sets the folder to sort in the active profile and scans it. */
    fun setInputDir(path: String) = run {
        val next = sorter.updateProfile { it.copy(inputDirectory = Storage.portable(path)) }
        c.profiles.save(sorter.profileId(), next)
        scan()
    }

    fun createProfile(name: String) = run { c.profiles.create(name) }

    fun duplicateProfile(id: String, name: String) = run { c.profiles.duplicate(id, name) }

    fun renameProfile(id: String, name: String) = run {
        c.profiles.rename(id, name)
        if (id == sorter.profileId()) sorter.applyProfile(id, c.profiles.load(id), Storage.home)
    }

    fun deleteProfile(id: String) = run {
        if (id == sorter.profileId()) throw IllegalStateException(str(R.string.profile_in_use))
        c.profiles.delete(id)
        sorter.deleteProfileData(id)
    }

    fun importProfile(fileName: String, text: String) = run {
        val id = c.profiles.import(fileName, text)
        say(str(R.string.profile_imported, c.profiles.load(id).name))
    }

    suspend fun profileText(id: String): String? = withContext(Dispatchers.IO) { runCatching { c.profiles.read(id) }.getOrNull() }

    suspend fun taggedCount(id: String): Int = withContext(Dispatchers.IO) { runCatching { sorter.taggedFileCount(id) }.getOrDefault(0) }

    fun editProfile(id: String) = run {
        val text = c.profiles.read(id)
        val p = runCatching { ProfileYaml.parse(text) }.getOrNull()
        _ui.update { it.copy(screen = Screen.EDITOR, editingId = id, editingProfile = p, editingText = text) }
    }

    /**
     * Saves the edited profile, from the forms ([profile]) or from the YAML editor ([text]).
     * Returns the problem, or null when it was saved. A profile that fails validation is never
     * written, and the one in use stays in use.
     */
    suspend fun saveProfile(id: String, profile: Profile?, text: String?): String? = withContext(serial) {
        try {
            val p = profile ?: ProfileYaml.parse(text!!)
            if (id == sorter.profileId()) {
                if (copy.busy) throw IllegalStateException(str(R.string.busy_copying))
                val before = sorter.inputDir() to sorter.profile().recursive
                sorter.applyProfile(id, p, Storage.home)
                if (text != null) c.profiles.saveText(id, text) else c.profiles.save(id, p)
                if (before != sorter.inputDir() to p.recursive) scan()
            } else {
                if (text != null) c.profiles.saveText(id, text) else c.profiles.save(id, p)
            }
            refresh()
            _ui.update { it.copy(screen = Screen.PROFILES, editingId = "", editingProfile = null) }
            null
        } catch (e: Exception) {
            e.message ?: e.toString()
        }
    }

    // ---- settings ----

    fun setShowFilmstrip(v: Boolean) {
        c.prefs.showFilmstrip = v
        _ui.update { it.copy(showFilmstrip = v) }
    }

    fun setSkipHidden(v: Boolean) {
        c.prefs.skipHidden = v
        _ui.update { it.copy(skipHidden = v) }
        rescan()
    }

    fun setCacheMb(v: Int) {
        c.prefs.thumbnailCacheMb = v
        c.thumbnails.maxBytes = c.prefs.thumbnailCacheMb * 1024L * 1024L
        _ui.update { it.copy(cacheMb = c.prefs.thumbnailCacheMb) }
        viewModelScope.launch(Dispatchers.IO) { c.thumbnails.prune() }
    }

    fun clearThumbnails() = viewModelScope.launch(Dispatchers.IO) {
        c.thumbnails.clear()
        say(str(R.string.cache_cleared))
    }

    suspend fun cacheSize(): Long = withContext(Dispatchers.IO) { c.thumbnails.sizeBytes() }

    fun removeLeftovers() = run {
        val n = sorter.removeLeftoverTempFiles()
        _ui.update { it.copy(leftovers = sorter.leftoverTempFiles(), interrupted = 0) }
        say(str(R.string.leftovers_removed, n))
    }

    fun dismissInterrupted() = _ui.update { it.copy(interrupted = 0, leftovers = emptyList()) }

    override fun onCleared() {
        checksCancel?.cancel()
        statusCancel?.cancel()
    }
}
