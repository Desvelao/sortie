package app.sortie.core

import java.io.File
import java.io.FileInputStream
import java.nio.file.Files
import java.nio.file.LinkOption
import java.nio.file.Path
import java.security.MessageDigest
import java.util.concurrent.Semaphore

/** Status of one (file, tag) destination. */
enum class DestStatus {
    READY, // file can be copied
    ALREADY_COPIED, // identical content exists
    CONFLICT, // different content exists
    UNRESOLVED_VAR, // a variable of the template cannot be determined
    INVALID_PATH, // destination path is invalid
    ERROR, // filesystem error
}

/** The result for one tag of one file. */
data class DestCheck(
    val tagId: Int = 0,
    val tagName: String = "",
    val path: String = "", // "" if it could not be resolved
    val status: DestStatus = DestStatus.ERROR,
    val detail: String = "",
    val srcHash: String = "",
    val dstHash: String = "",
) {
    /** Only READY is copied: unresolved variables, invalid paths, conflicts and errors never are. */
    val canCopy get() = status == DestStatus.READY
}

/**
 * True when there are checks and none can ever be copied: every one is [DestStatus.INVALID_PATH] (for example the
 * destination root is missing) or [DestStatus.UNRESOLVED_VAR]. The UI then disables its copy button. Other statuses
 * never count: a conflict can still be resolved by the conflict policy, and an error may be temporary.
 */
fun List<DestCheck>.allBlocked(): Boolean =
    isNotEmpty() && all { it.status == DestStatus.INVALID_PATH || it.status == DestStatus.UNRESOLVED_VAR }

/** Turns (file, variables, tag) into a destination path under [root]. Never writes. */
data class Resolver(
    val root: String = "", // absolute destination root
    val template: String = PathTemplate.DEFAULT, // a tag's own template wins
    val varNames: List<String> = emptyList(),
) {
    fun templateFor(tag: Tag): String =
        tag.destinationTemplate.trim().ifEmpty { template.trim().ifEmpty { PathTemplate.DEFAULT } }

    /**
     * Throws unless [root] can receive copies right now: configured, absolute, existing and a directory.
     * The pre-copy [Checker] and the copy itself ([CopyEngine]) both call it, so a destination shown as
     * ready cannot then fail on its root.
     */
    fun checkRoot() {
        if (root.isBlank()) throw SorterException("destination root is not configured")
        validateRoot(root)
        val f = File(root)
        if (!f.exists()) throw SorterException("destination root $root is not available (is the storage mounted?)")
        if (!f.isDirectory) throw SorterException("destination root $root is not a directory")
    }

    /**
     * The absolute destination path for one tag. [vars] holds resolved variable values by name
     * (unresolved ones are absent). The result is guaranteed to lie inside [root], also when
     * existing directories along the way are symlinks. Throws [UnresolvedException] or
     * [SorterException].
     */
    fun resolve(name: String, vars: Map<String, String>, tag: Tag): String {
        if (root.isBlank()) throw SorterException("destination root is not configured")
        validateRoot(root)
        val tpl = templateFor(tag)
        PathTemplate.validate(tpl, varNames)
        if (name.isEmpty() || name == "." || name == ".." || name.any { it == '/' || it == '\u0000' }) {
            throw SorterException("invalid file name \"$name\"")
        }
        try {
            Tags.validateDestination(tag.destination)
        } catch (e: SorterException) {
            throw SorterException("tag \"${tag.name}\": ${e.message}")
        }
        val (stem, ext) = splitName(name)
        val tagDir = tag.destination.trim().replace('\\', '/')
        val parts = PathTemplate.render(tpl) { n ->
            when (n) {
                "tag" -> tagDir
                "filename" -> name
                "name" -> stem
                "ext" -> ext
                else -> vars[n]
            }
        }
        val rootPath = File(root).toPath().normalize()
        var full = rootPath
        for (p in parts) full = full.resolve(p)
        full = full.normalize()
        if (!full.startsWith(rootPath)) throw SorterException("destination escapes the destination root")
        checkSymlinks(rootPath, full)
        return full.toString()
    }

    /** The deepest existing part of [path] must still lie inside [root] after resolving symlinks. */
    private fun checkSymlinks(root: Path, path: Path) {
        val realRoot = try {
            root.toRealPath()
        } catch (_: Exception) {
            return // root does not exist yet: nothing can escape
        }
        var p: Path? = path
        while (p != null && !Files.exists(p, LinkOption.NOFOLLOW_LINKS)) p = p.parent
        if (p == null) return
        val real = try {
            p.toRealPath()
        } catch (e: Exception) {
            throw SorterException("cannot resolve $p: ${e.message}")
        }
        if (!real.startsWith(realRoot)) throw SorterException("$p leads outside the destination root (symbolic link)")
    }

    companion object {
        /** Accepts "" (not configured yet) or an absolute path. */
        fun validateRoot(root: String) {
            if (root.isBlank()) return
            if (root.contains('\u0000')) throw SorterException("destination root contains an invalid character")
            if (!File(root).isAbsolute) throw SorterException("destination root must be an absolute path")
        }

        /** "photo.jpg" → ("photo", ".jpg"); a dotfile such as ".bashrc" has no extension. */
        fun splitName(name: String): Pair<String, String> {
            val i = name.lastIndexOf('.')
            return if (i <= 0) name to "" else name.substring(0, i) to name.substring(i)
        }
    }
}

/**
 * SHA-256 sums with bounded concurrency and a small cache keyed by (path, size, modification
 * time), so unchanged files are hashed once.
 */
class Hasher(concurrency: Int = 2) {
    private val sem = Semaphore(maxOf(1, concurrency))
    private val cache = HashMap<String, String>()

    fun sum(file: File, cancel: Cancel): String {
        val key = "${file.path}|${file.length()}|${file.lastModified()}"
        synchronized(cache) { cache[key] }?.let { return it }
        sem.acquire()
        try {
            synchronized(cache) { cache[key] }?.let { return it }
            val sum = sha256(file, cancel)
            synchronized(cache) {
                if (cache.size > 2048) cache.clear()
                cache[key] = sum
            }
            return sum
        } finally {
            sem.release()
        }
    }

    companion object {
        const val CHUNK = 1 shl 20

        /** Hex SHA-256 of a file; cancellation is honoured between chunks. */
        fun sha256(file: File, cancel: Cancel): String {
            val md = MessageDigest.getInstance("SHA-256")
            FileInputStream(file).use { input ->
                val buf = ByteArray(CHUNK)
                while (true) {
                    cancel.check()
                    val n = input.read(buf)
                    if (n < 0) break
                    md.update(buf, 0, n)
                }
            }
            return hex(md.digest())
        }

        fun hex(bytes: ByteArray): String {
            val sb = StringBuilder(bytes.size * 2)
            for (b in bytes) {
                sb.append(Character.forDigit((b.toInt() shr 4) and 0xf, 16)).append(Character.forDigit(b.toInt() and 0xf, 16))
            }
            return sb.toString()
        }
    }
}

/** Evaluates destinations. Safe for concurrent use; never writes to the filesystem. */
class Checker(hashWorkers: Int = 2) {
    private val hasher = Hasher(hashWorkers)

    /** Evaluates every tag independently; results follow the order of [tags]. */
    fun check(r: Resolver, srcPath: String, name: String, vars: Map<String, String>, tags: List<Tag>, cancel: Cancel): List<DestCheck> {
        // The copy refuses a root that is missing or not a directory, so the check must not call it ready.
        val rootError = try {
            r.checkRoot()
            null
        } catch (e: SorterException) {
            e.message ?: ""
        }
        return tags.map { t ->
            val base = DestCheck(tagId = t.id, tagName = t.name)
            rootError?.let { return@map base.copy(status = DestStatus.INVALID_PATH, detail = it) }
            val path = try {
                r.resolve(name, vars, t)
            } catch (e: UnresolvedException) {
                return@map base.copy(status = DestStatus.UNRESOLVED_VAR, detail = e.message ?: "")
            } catch (e: SorterException) {
                return@map base.copy(status = DestStatus.INVALID_PATH, detail = e.message ?: "")
            }
            compare(srcPath, base.copy(path = path), cancel)
        }
    }

    /** Checks an explicit destination path against the source. */
    fun compare(srcPath: String, destPath: String, cancel: Cancel): DestCheck = compare(srcPath, DestCheck(path = destPath), cancel)

    private fun compare(srcPath: String, chk: DestCheck, cancel: Cancel): DestCheck {
        fun fail(s: DestStatus, d: String) = chk.copy(status = s, detail = d)
        val src = File(srcPath)
        if (!src.exists()) return fail(DestStatus.ERROR, "source: $srcPath does not exist")
        val dst = File(chk.path)
        if (!Files.exists(dst.toPath(), LinkOption.NOFOLLOW_LINKS)) {
            var p = dst.parentFile
            while (p != null && !p.exists()) p = p.parentFile
            if (p != null && !p.isDirectory) {
                return fail(DestStatus.INVALID_PATH, "a parent of the destination is a file, not a directory")
            }
            return chk.copy(status = DestStatus.READY)
        }
        try {
            if (dst.exists() && Files.isSameFile(src.toPath(), dst.toPath())) {
                return fail(DestStatus.INVALID_PATH, "destination is the source file itself")
            }
        } catch (e: Exception) {
            return fail(DestStatus.ERROR, e.message ?: e.toString())
        }
        if (dst.isDirectory) return fail(DestStatus.CONFLICT, "a directory exists at the destination")
        if (!dst.isFile) return fail(DestStatus.CONFLICT, "a non-regular file exists at the destination")
        if (dst.length() != src.length()) return fail(DestStatus.CONFLICT, "a file with different size exists")
        val sh = try {
            hasher.sum(src, cancel)
        } catch (e: CancelledException) {
            return fail(DestStatus.ERROR, "cancelled")
        } catch (e: Exception) {
            return fail(DestStatus.ERROR, "hashing source: ${e.message}")
        }
        val dh = try {
            hasher.sum(dst, cancel)
        } catch (e: CancelledException) {
            return fail(DestStatus.ERROR, "cancelled")
        } catch (e: Exception) {
            return fail(DestStatus.ERROR, "hashing destination: ${e.message}")
        }
        return if (sh == dh) {
            chk.copy(status = DestStatus.ALREADY_COPIED, srcHash = sh, dstHash = dh)
        } else {
            chk.copy(status = DestStatus.CONFLICT, detail = "a file with the same size but different content exists", srcHash = sh, dstHash = dh)
        }
    }
}
