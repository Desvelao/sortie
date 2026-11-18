package app.sortie.data

import app.sortie.core.CopyJob
import app.sortie.core.CopyResult
import app.sortie.core.FileEntry
import app.sortie.core.FileRow
import app.sortie.core.FileSync
import app.sortie.core.HistoryEntry
import app.sortie.core.Store
import app.sortie.core.SyncResult
import app.sortie.core.Tag

/** The [Store] behind the sorter, backed by Room. Every call blocks: use it off the main thread. */
class RoomStore(private val db: AppDatabase) : Store {
    private val dao = db.dao()

    override fun syncFiles(dir: String, files: List<FileEntry>, recursive: Boolean): Pair<List<FileEntry>, SyncResult> =
        db.runInTransaction<Pair<List<FileEntry>, SyncResult>> {
            val prefix = dir.trimEnd('/') + "/"
            val existing = dao.filesUnder(prefix, prefix.length)
                .filter { FileSync.inScope(it.sourcePath, dir, recursive) }
                .map { FileRow(it.id, it.sourcePath, it.fileSize, it.modifiedTime, it.status == Store.FILE_MISSING) }
            val plan = FileSync.plan(existing, files)
            for (f in plan.update + plan.rename) dao.updateFile(f.id, f.path, f.name, f.ext, f.mime, f.size, f.modTimeMs)
            val out = plan.files.toMutableList()
            for (i in plan.insert) {
                val f = out[i]
                out[i] = f.copy(id = dao.insertFile(FileRowEntity(0, f.path, f.name, f.ext, f.mime, f.size, f.modTimeMs, Store.FILE_PENDING)))
            }
            for (id in plan.missing) dao.markMissing(id)
            out to plan.result()
        }

    override fun loadSession(): Pair<String, Long>? = dao.session()?.let { it.inputDirectory to it.currentFileId }

    override fun saveSession(dir: String, fileId: Long) = dao.saveSession(SessionEntity(1, dir, fileId))

    override fun syncProfileTags(profile: String, tags: List<Tag>): Map<String, Int> = db.runInTransaction<Map<String, Int>> {
        val existing = dao.tags(profile).associateBy { it.name }.toMutableMap()
        val want = tags.map { it.name }.toSet()
        for ((name, row) in existing.toMap()) {
            if (name in want) continue
            dao.deleteAssignmentsOfTag(row.id)
            dao.deleteTag(row.id)
            existing.remove(name)
        }
        tags.associate { t ->
            val row = existing[t.name]
            val id = if (row != null) {
                dao.updateTag(row.id, t.shortcut, t.destination, t.enabled, t.sortOrder)
                row.id
            } else {
                dao.insertTag(TagEntity(0, profile, t.name, t.shortcut, t.destination, t.enabled, t.sortOrder))
            }
            t.name to id.toInt()
        }
    }

    override fun loadAssignments(profile: String): Map<Long, Set<Int>> =
        dao.assignments(profile).groupBy({ it.fileId }, { it.tagId.toInt() }).mapValues { it.value.toSet() }

    override fun toggleTag(fileId: Long, tagId: Int): Boolean = db.runInTransaction<Boolean> {
        if (dao.unassign(fileId, tagId.toLong()) > 0) {
            false
        } else {
            dao.assign(FileTagEntity(fileId, tagId.toLong(), System.currentTimeMillis()))
            true
        }
    }

    override fun countTaggedFiles(profile: String): Int = dao.countTaggedFiles(profile)

    override fun deleteProfile(profile: String) = db.runInTransaction {
        dao.deleteAssignmentsOfProfile(profile)
        dao.deleteTagsOfProfile(profile)
        dao.deleteVarsOfProfile(profile)
    }

    override fun loadFileVars(profile: String): Map<Long, Map<String, String>> =
        dao.fileVars(profile).groupBy { it.fileId }.mapValues { e -> e.value.associate { it.name to it.value } }

    override fun setFileVar(fileId: Long, profile: String, name: String, value: String) {
        val v = value.trim()
        if (v.isEmpty()) dao.deleteFileVar(fileId, profile, name) else dao.setFileVar(FileVarEntity(fileId, profile, name, v))
    }

    override fun pruneFileVars(profile: String, keep: List<String>) {
        if (keep.isEmpty()) dao.deleteVarsOfProfile(profile) else dao.pruneFileVars(profile, keep)
    }

    override fun markInterrupted(): Int = dao.markInterrupted()

    override fun interruptedTempFiles(): List<String> = dao.interruptedMessages().mapNotNull { msg ->
        val i = msg.indexOf(TEMP_PREFIX)
        if (i >= 0 && msg.endsWith(")")) msg.substring(i + TEMP_PREFIX.length, msg.length - 1) else null
    }

    override fun history(fileId: Long): List<HistoryEntry> = dao.history(fileId).map {
        HistoryEntry(
            it.id, it.fileId, it.tagName, it.sourcePath, it.destinationPath, it.sourceHash ?: "", it.destinationHash ?: "",
            it.status, it.copiedAt ?: 0, it.errorMessage ?: "",
        )
    }

    override fun begin(job: CopyJob, destPath: String): Long = dao.insertHistory(
        CopyHistoryEntity(0, job.fileId, job.tag.id.toLong().takeIf { it > 0 }, job.tag.name, job.sourcePath, destPath, null, null, Store.IN_PROGRESS, null, null),
    )

    override fun note(id: Long, note: String) = dao.noteHistory(id, note)

    override fun finish(id: Long, result: CopyResult) = dao.finishHistory(
        id, result.status.name, result.srcHash.ifEmpty { null }, result.dstHash.ifEmpty { null }, result.detail.ifEmpty { null },
        System.currentTimeMillis(),
    )

    private companion object {
        const val TEMP_PREFIX = "(temporary file: "
    }
}
