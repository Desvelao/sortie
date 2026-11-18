package app.sortie.core

import java.io.File
import java.io.FileInputStream
import java.io.FileOutputStream
import java.nio.channels.FileChannel
import java.nio.file.FileAlreadyExistsException
import java.nio.file.Files
import java.nio.file.LinkOption
import java.nio.file.StandardCopyOption
import java.nio.file.StandardOpenOption
import java.security.MessageDigest
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit

/** What happens when a different file already exists at the destination. */
enum class ConflictPolicy(val id: String) {
    SKIP("skip"), // default
    RENAME("rename"), // write "name (1).ext"
    REPLACE("replace"), // overwrite; requires explicit confirmation
    ;

    companion object {
        fun of(id: String) = entries.firstOrNull { it.id == id } ?: SKIP
    }
}

/** The outcome of one job. Names double as history statuses. */
enum class CopyStatus {
    COPIED, REPLACED, RENAMED, ALREADY_COPIED, CONFLICT_SKIPPED,
    SKIPPED_INVALID, // variable or path unusable
    FAILED, CANCELLED,
    ;

    /** Whether the destination holds the file afterwards. */
    val ok get() = this == COPIED || this == REPLACED || this == RENAMED || this == ALREADY_COPIED
}

/** One (file, tag) copy. */
data class CopyJob(
    val fileId: Long,
    val sourcePath: String,
    val name: String,
    val size: Long, // size recorded by the scan
    val modTimeMs: Long, // mtime recorded by the scan; 0 disables the changed-since-scan check
    val vars: Map<String, String>,
    val tag: Tag,
)

data class CopyResult(
    val job: CopyJob,
    val status: CopyStatus = CopyStatus.FAILED,
    val destPath: String = "",
    val detail: String = "",
    val srcHash: String = "",
    val dstHash: String = "",
    val bytes: Long = 0,
)

sealed class CopyEvent {
    abstract val index: Int
    abstract val total: Int
    abstract val done: Int

    data class Start(override val index: Int, override val total: Int, override val done: Int, val job: CopyJob) : CopyEvent()
    data class Bytes(override val index: Int, override val total: Int, override val done: Int, val job: CopyJob, val bytes: Long, val bytesTotal: Long) : CopyEvent()
    data class Done(override val index: Int, override val total: Int, override val done: Int, val result: CopyResult) : CopyEvent()
}

class CopyOptions(
    val policy: ConflictPolicy = ConflictPolicy.SKIP,
    val confirmReplace: Boolean = false, // must be true to use REPLACE
    val workers: Int = 2,
    val onEvent: ((CopyEvent) -> Unit)? = null, // calls are serialised
)

class CopySummary(val results: List<CopyResult>) {
    fun count(s: CopyStatus) = results.count { it.status == s }
    val bytes get() = results.sumOf { it.bytes }
}

/** Records copy attempts. A failing history never blocks a copy; the error is attached to the result. */
interface CopyHistory {
    fun begin(job: CopyJob, destPath: String): Long
    fun note(id: Long, note: String)
    fun finish(id: Long, result: CopyResult)
}

/**
 * Runs copy jobs write-safely: temp file next to the target → fsync → read-back SHA-256
 * verification → no-clobber rename. Sources are only ever read.
 */
class CopyEngine(private val checker: Checker, private val history: CopyHistory? = null) {

    /** Evaluates every job without writing anything. */
    fun plan(r: Resolver, jobs: List<CopyJob>, cancel: Cancel, workers: Int = 2, onDone: ((Int, Int) -> Unit)? = null): List<DestCheck> {
        val out = arrayOfNulls<DestCheck>(jobs.size)
        var done = 0
        val lock = Any()
        parallel(jobs.size, workers, cancel) { i ->
            val j = jobs[i]
            out[i] = checker.check(r, j.sourcePath, j.name, j.vars, listOf(j.tag), cancel)[0]
            if (onDone != null) synchronized(lock) { onDone(++done, jobs.size) }
        }
        return noteBatchDuplicates(out.mapIndexed { i, c -> c ?: DestCheck(tagId = jobs[i].tag.id, tagName = jobs[i].tag.name, detail = "cancelled") })
    }

    /**
     * Executes jobs with a bounded worker pool. Errors of single jobs never stop the batch.
     * Cancelling aborts running copies (their temporary files are removed) and marks unstarted
     * jobs CANCELLED. Running the same jobs again resumes: finished destinations are recognised
     * by content. Throws [SorterException] when the run cannot start at all.
     */
    fun run(r: Resolver, jobs: List<CopyJob>, opts: CopyOptions, cancel: Cancel): CopySummary {
        if (opts.policy == ConflictPolicy.REPLACE && !opts.confirmReplace) {
            throw SorterException("replacing existing files requires explicit confirmation")
        }
        preflight(r)
        val results = arrayOfNulls<CopyResult>(jobs.size)
        val batch = BatchPaths()
        val lock = Any()
        var done = 0
        fun emit(make: (Int) -> CopyEvent) {
            val cb = opts.onEvent ?: return
            synchronized(lock) { cb(make(done)) }
        }
        parallel(jobs.size, opts.workers, cancel) { i ->
            val job = jobs[i]
            emit { CopyEvent.Start(i, jobs.size, it, job) }
            val res = try {
                runJob(r, batch, job, opts, cancel) { b, t -> emit { CopyEvent.Bytes(i, jobs.size, it, job, b, t) } }
            } catch (e: Exception) {
                CopyResult(job, CopyStatus.FAILED, detail = e.message ?: e.toString())
            }
            results[i] = res
            synchronized(lock) { done++ }
            emit { CopyEvent.Done(i, jobs.size, it, res) }
        }
        return CopySummary(results.mapIndexed { i, r0 -> r0 ?: CopyResult(jobs[i], CopyStatus.CANCELLED, detail = "not started") })
    }

    private fun parallel(n: Int, workers: Int, cancel: Cancel, body: (Int) -> Unit) {
        if (n == 0) return
        val pool = Executors.newFixedThreadPool(maxOf(1, minOf(if (workers < 1) 2 else workers, n)))
        try {
            for (i in 0 until n) {
                pool.execute { if (!cancel.cancelled) body(i) }
            }
        } finally {
            pool.shutdown()
            while (!pool.awaitTermination(1, TimeUnit.HOURS)) { /* keep waiting */ }
        }
    }

    private fun preflight(r: Resolver) = r.checkRoot()

    /** Destinations written during one run, so REPLACE never overwrites a file this batch just wrote. */
    private class BatchPaths {
        val paths = HashSet<String>()

        /** Whether this batch already wrote dest, directly or under a name that is the same file on disk. */
        fun wrote(dest: String): Boolean {
            if (dest in paths) return true
            return paths.any {
                it.equals(dest, ignoreCase = true) && runCatching { Files.isSameFile(File(it).toPath(), File(dest).toPath()) }.getOrDefault(false)
            }
        }
    }

    private fun runJob(r: Resolver, batch: BatchPaths, job: CopyJob, opts: CopyOptions, cancel: Cancel, progress: (Long, Long) -> Unit): CopyResult {
        if (cancel.cancelled) return CopyResult(job, CopyStatus.CANCELLED, detail = "cancelled")
        val chk = checker.check(r, job.sourcePath, job.name, job.vars, listOf(job.tag), cancel)[0]
        val res = CopyResult(job, destPath = chk.path, srcHash = chk.srcHash, dstHash = chk.dstHash, detail = chk.detail)
        when (chk.status) {
            DestStatus.ALREADY_COPIED -> return res.copy(status = CopyStatus.ALREADY_COPIED)
            DestStatus.UNRESOLVED_VAR, DestStatus.INVALID_PATH -> return res.copy(status = CopyStatus.SKIPPED_INVALID)
            DestStatus.ERROR -> return res.copy(status = if (cancel.cancelled) CopyStatus.CANCELLED else CopyStatus.FAILED)
            DestStatus.CONFLICT -> if (opts.policy == ConflictPolicy.SKIP) return res.copy(status = CopyStatus.CONFLICT_SKIPPED)
            DestStatus.READY -> {}
        }
        return write(batch, job, res, chk.status == DestStatus.CONFLICT, opts.policy, cancel, progress)
    }

    /** The actual copy (with history) for a job whose destination is READY, or in conflict with a policy that allows writing. */
    private fun write(
        batch: BatchPaths, job: CopyJob, start: CopyResult, conflictAtStart: Boolean, policy: ConflictPolicy,
        cancel: Cancel, progress: (Long, Long) -> Unit,
    ): CopyResult {
        var res = start
        var histId: Long? = null
        if (history != null) {
            try {
                histId = history.begin(job, res.destPath)
            } catch (e: Exception) {
                res = res.copy(detail = "history unavailable: ${e.message}")
            }
        }
        fun finish(r: CopyResult): CopyResult {
            val id = histId ?: return r
            return try {
                history!!.finish(id, r)
                r
            } catch (e: Exception) {
                if (r.detail.isEmpty()) r.copy(detail = "history not updated: ${e.message}") else r
            }
        }
        fun fail(e: Throwable): CopyResult = finish(
            if (e is CancelledException) {
                res.copy(status = CopyStatus.CANCELLED, detail = "cancelled; temporary file removed")
            } else {
                res.copy(status = CopyStatus.FAILED, detail = e.message ?: e.toString())
            },
        )

        // 1. the source must still be what the scan saw
        val src = File(job.sourcePath)
        if (!src.exists()) return fail(SorterException("source: ${job.sourcePath} does not exist"))
        if (!src.isFile) return fail(SorterException("source is no longer a regular file"))
        if (job.modTimeMs != 0L && (src.length() != job.size || src.lastModified() != job.modTimeMs)) {
            return fail(SorterException("source changed since it was scanned; refresh the directory"))
        }

        var conflict = conflictAtStart
        var dest = res.destPath
        var overwrite = conflict && policy == ConflictPolicy.REPLACE
        var rename = conflict && policy == ConflictPolicy.RENAME
        var tmp: File? = null
        try {
            if (rename) { // reuse an identical alternative if a previous run created one
                val (alt, identical) = pickAlternative(job.sourcePath, dest, cancel)
                if (identical) {
                    return finish(res.copy(status = CopyStatus.ALREADY_COPIED, destPath = alt, detail = "identical copy exists under an alternative name"))
                }
                dest = alt
            }

            // 2. directories below the (existing) root
            val dir = File(dest).parentFile ?: throw SorterException("destination has no parent folder")
            if (!dir.isDirectory && !dir.mkdirs() && !dir.isDirectory) throw SorterException("cannot create folder ${dir.path}")

            // 3. copy to a temporary file next to the destination, verified
            val copied = copyToTemp(src, dir, cancel, progress) { t -> histId?.let { runCatching { history!!.note(it, t) } } }
            tmp = copied.tmp
            res = res.copy(srcHash = copied.hash, dstHash = copied.hash, bytes = copied.bytes)

            // 4. publish; never overwrite unless explicitly allowed
            var tries = 0
            while (true) {
                // Publish and record under one lock, so a concurrent job that finds the file just
                // written cannot overwrite it before it is recorded.
                val published = synchronized(batch) {
                    if (overwrite && batch.wrote(dest)) {
                        null
                    } else {
                        val ok = if (overwrite) {
                            moveReplacing(copied.tmp, File(dest))
                            true
                        } else {
                            moveNoReplace(copied.tmp, File(dest))
                        }
                        if (ok) batch.paths += dest
                        ok
                    }
                }
                if (published == null) {
                    return finish(
                        res.copy(
                            status = CopyStatus.CONFLICT_SKIPPED, bytes = 0,
                            detail = "another file in this batch was already written to this destination; not replaced",
                        ),
                    )
                }
                if (published) break
                if (++tries > 20) throw SorterException("the destination keeps changing; giving up")
                // someone created the destination while we were copying
                if (rename) {
                    val (alt, identical) = pickAlternative(job.sourcePath, res.destPath, cancel)
                    if (identical) return finish(res.copy(status = CopyStatus.ALREADY_COPIED, destPath = alt, bytes = 0))
                    dest = alt
                    continue
                }
                if (checker.compare(job.sourcePath, dest, cancel).status == DestStatus.ALREADY_COPIED) {
                    return finish(res.copy(status = CopyStatus.ALREADY_COPIED, bytes = 0, detail = "appeared during copy"))
                }
                when (policy) {
                    ConflictPolicy.RENAME -> {
                        rename = true
                        conflict = true
                        dest = pickAlternative(job.sourcePath, res.destPath, cancel).first
                    }
                    ConflictPolicy.REPLACE -> {
                        overwrite = true
                        conflict = true
                    }
                    ConflictPolicy.SKIP ->
                        return finish(res.copy(status = CopyStatus.CONFLICT_SKIPPED, bytes = 0, detail = "a different file appeared during copy"))
                }
            }
            tmp = null
            syncDir(dir)
            val status = when {
                overwrite && conflict -> CopyStatus.REPLACED
                rename && conflict -> CopyStatus.RENAMED
                else -> CopyStatus.COPIED
            }
            // success: drop the pre-check remarks
            val detail = if (res.detail.startsWith("history unavailable")) res.detail else ""
            return finish(res.copy(status = status, destPath = dest, detail = detail))
        } catch (e: Exception) {
            return fail(e)
        } finally {
            tmp?.delete()
        }
    }

    /** The first free "name (n).ext", or the existing alternative whose content already equals the source. */
    private fun pickAlternative(src: String, dest: String, cancel: Cancel): Pair<String, Boolean> {
        for (n in 1 until 10000) {
            val cand = alternativeName(dest, n)
            if (!Files.exists(File(cand).toPath(), LinkOption.NOFOLLOW_LINKS)) return cand to false
            val c = checker.compare(src, cand, cancel)
            if (c.status == DestStatus.ALREADY_COPIED) return cand to true
            if (c.status == DestStatus.ERROR) throw if (cancel.cancelled) CancelledException() else SorterException(c.detail)
        }
        throw SorterException("no free alternative file name")
    }

    private class Copied(val tmp: File, val hash: String, val bytes: Long)

    /**
     * Copies src into a new temporary file inside dir and verifies it by reading it back. The
     * source is opened read-only. On any error the temporary file is removed.
     */
    private fun copyToTemp(src: File, dir: File, cancel: Cancel, progress: (Long, Long) -> Unit, note: (String) -> Unit): Copied {
        val size = src.length()
        val mtime = src.lastModified()
        val tmp = File.createTempFile(TEMP_PREFIX, TEMP_SUFFIX, dir)
        note(tmp.path)
        var ok = false
        try {
            val md = MessageDigest.getInstance("SHA-256")
            var n = 0L
            FileInputStream(src).use { input ->
                FileOutputStream(tmp).use { out ->
                    val buf = ByteArray(Hasher.CHUNK)
                    var last = System.nanoTime()
                    while (true) {
                        cancel.check()
                        val k = input.read(buf)
                        if (k < 0) break
                        out.write(buf, 0, k)
                        md.update(buf, 0, k)
                        n += k
                        if (System.nanoTime() - last > 150_000_000L) {
                            progress(n, size)
                            last = System.nanoTime()
                        }
                    }
                    out.flush()
                    out.fd.sync()
                }
            }
            if (n != size) throw SorterException("source changed while copying (read $n of $size bytes)")
            // the source must not have changed during the copy
            if (!src.exists() || src.length() != size || src.lastModified() != mtime) {
                throw SorterException("source changed while copying")
            }
            // read the copy back to verify what actually reached the disk
            val sum = Hasher.hex(md.digest())
            if (Hasher.sha256(tmp, cancel) != sum) throw SorterException("verification failed: the copy differs from the source")
            // Times are best effort: shared storage and FAT/exFAT commonly refuse or round them.
            runCatching { tmp.setLastModified(mtime) }
            ok = true
            return Copied(tmp, sum, n)
        } finally {
            if (!ok) tmp.delete()
        }
    }

    companion object {
        const val TEMP_PREFIX = ".sortie-"
        const val TEMP_SUFFIX = ".tmp"

        /** Whether path is named like a temporary file of this engine. */
        fun isTempFile(path: String): Boolean {
            val n = File(path).name
            return n.startsWith(TEMP_PREFIX) && n.endsWith(TEMP_SUFFIX)
        }

        /** Deletes a leftover temporary file; anything not named like one is refused. */
        fun removeTempFile(path: String): Boolean {
            if (!isTempFile(path)) throw SorterException("$path is not a temporary file of this app")
            val f = File(path)
            return f.isFile && f.delete()
        }

        /** "name (n).ext"; ".tar.gz" and friends stay together. */
        fun alternativeName(path: String, n: Int): String {
            val f = File(path)
            var (stem, ext) = Resolver.splitName(f.name)
            if (stem.endsWith(".tar") && stem.length > ".tar".length) {
                ext = ".tar$ext"
                stem = stem.removeSuffix(".tar")
            }
            return File(f.parentFile, "$stem ($n)$ext").path
        }

        /**
         * Moves src to dst and returns false (leaving both untouched) if dst already exists, so a
         * destination file is never overwritten silently. A hard link fails atomically when dst
         * exists; where links are not supported (Android shared storage, FAT) it falls back to
         * check-then-move, which leaves a tiny window for another program.
         */
        internal fun moveNoReplace(src: File, dst: File): Boolean {
            try {
                Files.createLink(dst.toPath(), src.toPath())
                src.delete()
                return true
            } catch (_: FileAlreadyExistsException) {
                return false
            } catch (_: Exception) {
                // links unsupported here: fall through
            }
            if (Files.exists(dst.toPath(), LinkOption.NOFOLLOW_LINKS)) return false
            return try {
                Files.move(src.toPath(), dst.toPath())
                true
            } catch (_: FileAlreadyExistsException) {
                false
            }
        }

        internal fun moveReplacing(src: File, dst: File) {
            try {
                Files.move(src.toPath(), dst.toPath(), StandardCopyOption.ATOMIC_MOVE, StandardCopyOption.REPLACE_EXISTING)
            } catch (_: java.nio.file.AtomicMoveNotSupportedException) {
                Files.move(src.toPath(), dst.toPath(), StandardCopyOption.REPLACE_EXISTING)
            }
        }

        /** Flushes directory metadata (best effort). */
        private fun syncDir(dir: File) {
            runCatching { FileChannel.open(dir.toPath(), StandardOpenOption.READ).use { it.force(true) } }
        }

        /**
         * Flags READY jobs whose destination is also the destination of an earlier job of the same
         * batch: such jobs look fine on their own, but only one of the files can end up there.
         */
        internal fun noteBatchDuplicates(checks: List<DestCheck>): List<DestCheck> {
            val seen = HashSet<String>()
            val seenFold = HashSet<String>()
            return checks.map { c ->
                if (c.path.isEmpty()) return@map c
                val fold = c.path.lowercase()
                val out = if (c.status == DestStatus.READY && (c.path in seen || fold in seenFold)) {
                    val note = if (c.path in seen) {
                        "another file in this batch has the same destination"
                    } else {
                        "another file in this batch has a destination that differs only in letter case (the same file on case-insensitive storage)"
                    }
                    c.copy(detail = if (c.detail.isEmpty()) note else "${c.detail}; $note")
                } else {
                    c
                }
                seen += c.path
                seenFold += fold
                out
            }
        }
    }
}
