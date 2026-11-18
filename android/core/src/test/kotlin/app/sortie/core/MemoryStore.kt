package app.sortie.core

/** An in-memory [Store] with the same semantics as the app's database, for tests. */
class MemoryStore : Store {
    class Row(var id: Long, var path: String, var size: Long, var mtime: Long, var missing: Boolean)
    class TagRow(val id: Int, val profile: String, val name: String)
    class Hist(val id: Long, val fileId: Long, val tagName: String, val src: String, val dst: String, var status: String, var message: String, var at: Long = 0)

    val rows = ArrayList<Row>()
    val tagRows = ArrayList<TagRow>()
    val fileTags = HashSet<Pair<Long, Int>>()
    val fileVars = HashMap<Triple<Long, String, String>, String>()
    val hist = ArrayList<Hist>()
    var onNote: (() -> Unit)? = null
    private var session: Pair<String, Long>? = null
    private var nextFile = 1L
    private var nextTag = 1
    private var nextHist = 1L

    @Synchronized
    override fun syncFiles(dir: String, files: List<FileEntry>, recursive: Boolean): Pair<List<FileEntry>, SyncResult> {
        val existing = rows.filter { FileSync.inScope(it.path, dir, recursive) }.map { FileRow(it.id, it.path, it.size, it.mtime, it.missing) }
        val plan = FileSync.plan(existing, files)
        val byId = rows.associateBy { it.id }
        for (f in plan.update + plan.rename) byId.getValue(f.id).apply { path = f.path; size = f.size; mtime = f.modTimeMs; missing = false }
        val out = plan.files.toMutableList()
        for (i in plan.insert) {
            val f = out[i].copy(id = nextFile++)
            out[i] = f
            rows += Row(f.id, f.path, f.size, f.modTimeMs, false)
        }
        for (id in plan.missing) byId.getValue(id).missing = true
        return out to plan.result()
    }

    @Synchronized override fun loadSession() = session

    @Synchronized override fun saveSession(dir: String, fileId: Long) {
        session = dir to fileId
    }

    @Synchronized
    override fun syncProfileTags(profile: String, tags: List<Tag>): Map<String, Int> {
        val want = tags.map { it.name }.toSet()
        val gone = tagRows.filter { it.profile == profile && it.name !in want }
        fileTags.removeAll { p -> gone.any { it.id == p.second } }
        tagRows.removeAll(gone)
        return tags.associate { t ->
            val row = tagRows.firstOrNull { it.profile == profile && it.name == t.name } ?: TagRow(nextTag++, profile, t.name).also { tagRows += it }
            t.name to row.id
        }
    }

    @Synchronized
    override fun loadAssignments(profile: String): Map<Long, Set<Int>> {
        val ids = tagRows.filter { it.profile == profile }.map { it.id }.toSet()
        return fileTags.filter { it.second in ids }.groupBy({ it.first }, { it.second }).mapValues { it.value.toSet() }
    }

    @Synchronized
    override fun toggleTag(fileId: Long, tagId: Int): Boolean {
        val k = fileId to tagId
        return if (fileTags.remove(k)) false else fileTags.add(k)
    }

    @Synchronized override fun countTaggedFiles(profile: String) = loadAssignments(profile).size

    @Synchronized
    override fun deleteProfile(profile: String) {
        val ids = tagRows.filter { it.profile == profile }.map { it.id }.toSet()
        fileTags.removeAll { it.second in ids }
        tagRows.removeAll { it.profile == profile }
        fileVars.keys.removeAll { it.second == profile }
    }

    @Synchronized
    override fun loadFileVars(profile: String): Map<Long, Map<String, String>> =
        fileVars.filterKeys { it.second == profile }.entries.groupBy({ it.key.first }, { it.key.third to it.value }).mapValues { it.value.toMap() }

    @Synchronized
    override fun setFileVar(fileId: Long, profile: String, name: String, value: String) {
        val k = Triple(fileId, profile, name)
        if (value.isBlank()) fileVars.remove(k) else fileVars[k] = value.trim()
    }

    @Synchronized
    override fun pruneFileVars(profile: String, keep: List<String>) {
        fileVars.keys.removeAll { it.second == profile && it.third !in keep }
    }

    @Synchronized
    override fun markInterrupted(): Int {
        val open = hist.filter { it.status == Store.IN_PROGRESS }
        for (h in open) {
            h.status = Store.INTERRUPTED
            h.message = "interrupted" + if (h.message.isEmpty()) "" else " (temporary file: ${h.message})"
        }
        return open.size
    }

    @Synchronized
    override fun interruptedTempFiles(): List<String> = hist.filter { it.status == Store.INTERRUPTED }
        .mapNotNull { Regex("""\(temporary file: (.*)\)$""").find(it.message)?.groupValues?.get(1) }

    @Synchronized
    override fun history(fileId: Long): List<HistoryEntry> = hist.filter { it.fileId == fileId }.sortedByDescending { it.id }
        .map { HistoryEntry(it.id, it.fileId, it.tagName, it.src, it.dst, "", "", it.status, it.at, it.message) }

    @Synchronized
    override fun begin(job: CopyJob, destPath: String): Long {
        val h = Hist(nextHist++, job.fileId, job.tag.name, job.sourcePath, destPath, Store.IN_PROGRESS, "")
        hist += h
        return h.id
    }

    override fun note(id: Long, note: String) {
        synchronized(this) { hist.first { it.id == id }.message = note }
        onNote?.invoke()
    }

    @Synchronized
    override fun finish(id: Long, result: CopyResult) {
        hist.first { it.id == id }.apply { status = result.status.name; message = result.detail; at = 1 }
    }
}
