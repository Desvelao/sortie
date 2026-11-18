package app.sortie.core

import java.io.File

/** Summary of a rescan. Renamed files keep their id, tags and manual variable values. */
data class SyncResult(val added: Int = 0, val modified: Int = 0, val removed: Int = 0, val renamed: Int = 0)

data class HistoryEntry(
    val id: Long,
    val fileId: Long,
    val tagName: String,
    val sourcePath: String,
    val destPath: String,
    val sourceHash: String,
    val destHash: String,
    val status: String,
    val copiedAtMs: Long, // 0 while in progress
    val message: String,
)

/**
 * Operational state kept between runs: the file inventory, per-profile tags, assignments and
 * manual values, the session and the copy history. The app implements it with Room.
 * Implementations must be safe for concurrent use.
 */
interface Store : CopyHistory {
    /**
     * Reconciles scanned files with the inventory in one transaction (see [FileSync]) and returns
     * the files with their ids filled in.
     */
    fun syncFiles(dir: String, files: List<FileEntry>, recursive: Boolean): Pair<List<FileEntry>, SyncResult>

    /** The last directory and current file id, if any. */
    fun loadSession(): Pair<String, Long>?
    fun saveSession(dir: String, fileId: Long)

    /**
     * Makes the stored tags of a profile match [tags] by name: missing ones are removed with their
     * assignments, new ones are added. Returns name → id.
     */
    fun syncProfileTags(profile: String, tags: List<Tag>): Map<String, Int>
    fun loadAssignments(profile: String): Map<Long, Set<Int>>

    /** Toggles a tag on a file and returns whether it is assigned afterwards. */
    fun toggleTag(fileId: Long, tagId: Int): Boolean
    fun countTaggedFiles(profile: String): Int

    /** Removes the tags, assignments and manual values of a profile. */
    fun deleteProfile(profile: String)

    fun loadFileVars(profile: String): Map<Long, Map<String, String>>

    /** Stores a manual value; an empty value removes it. */
    fun setFileVar(fileId: Long, profile: String, name: String, value: String)

    /** Drops manual values of variables the profile no longer defines. */
    fun pruneFileVars(profile: String, keep: List<String>)

    /** Turns IN_PROGRESS history rows (a previous run was killed) into INTERRUPTED; returns how many. */
    fun markInterrupted(): Int

    /** Temporary files noted by interrupted copies. */
    fun interruptedTempFiles(): List<String>
    fun history(fileId: Long): List<HistoryEntry>

    companion object {
        const val IN_PROGRESS = "IN_PROGRESS"
        const val INTERRUPTED = "INTERRUPTED"
        const val FILE_PENDING = "PENDING"
        const val FILE_MISSING = "MISSING"
    }
}

/** A row of the file inventory as the sync needs it. */
data class FileRow(val id: Long, val path: String, val size: Long?, val modTimeMs: Long?, val missing: Boolean)

/**
 * The decisions of a rescan, for a store to apply in one transaction: known files keep their id;
 * new files are inserted; files that disappeared are marked MISSING (never deleted, so
 * classifications survive); a file that vanished and a new file with the same size and
 * modification time, when that match is unique, are the same file renamed.
 */
class FileSync private constructor(
    /** The scanned files; known and renamed ones carry their id, new ones have id 0. */
    val files: List<FileEntry>,
    /** Known files whose size/time changed or that were MISSING: refresh the row. */
    val update: List<FileEntry>,
    /** Renamed files: the row with this id gets the new path. */
    val rename: List<FileEntry>,
    /** Indices into [files] of files to insert. */
    val insert: List<Int>,
    /** Ids of rows to mark MISSING. */
    val missing: List<Long>,
    val modified: Int,
) {
    fun result(inserted: Int = insert.size, renamed: Int = rename.size) =
        SyncResult(added = inserted, modified = modified, removed = missing.size, renamed = renamed)

    companion object {
        /** [existing] must already be limited to the scanned folder (direct children only when not recursive). */
        fun plan(existing: List<FileRow>, scanned: List<FileEntry>): FileSync {
            val byPath = existing.associateBy { it.path }
            val files = scanned.toMutableList()
            val seen = HashSet<Long>()
            val update = ArrayList<FileEntry>()
            val fresh = ArrayList<Int>()
            var modified = 0
            for ((i, f) in scanned.withIndex()) {
                val r = byPath[f.path]
                if (r == null) {
                    files[i] = f.copy(id = 0)
                    fresh += i
                    continue
                }
                val known = f.copy(id = r.id)
                files[i] = known
                seen += r.id
                val changed = r.size != f.size || r.modTimeMs != f.modTimeMs
                if (changed) modified++
                if (changed || r.missing) update += known
            }
            val unseen = existing.filter { it.id !in seen }
            val goneByKey = unseen.filter { (it.size ?: 0) > 0 && it.modTimeMs != null }.groupBy { "${it.size}|${it.modTimeMs}" }
            val freshByKey = fresh.filter { files[it].size > 0 }.groupBy { "${files[it].size}|${files[it].modTimeMs}" }
            val rename = ArrayList<FileEntry>()
            val renamedRows = HashSet<Long>()
            val renamedFresh = HashSet<Int>()
            for ((k, gone) in goneByKey) {
                val nf = freshByKey[k] ?: continue
                if (gone.size == 1 && nf.size == 1) {
                    val f = files[nf[0]].copy(id = gone[0].id)
                    files[nf[0]] = f
                    rename += f
                    renamedRows += gone[0].id
                    renamedFresh += nf[0]
                }
            }
            return FileSync(
                files = files, update = update, rename = rename,
                insert = fresh.filter { it !in renamedFresh },
                missing = unseen.filter { it.id !in renamedRows && !it.missing }.map { it.id },
                modified = modified,
            )
        }

        /** Whether a stored path belongs to a scan of [dir]. */
        fun inScope(path: String, dir: String, recursive: Boolean): Boolean {
            val prefix = dir.trimEnd('/') + "/"
            return path.startsWith(prefix) && (recursive || File(path).parent == File(dir).path)
        }
    }
}
