package app.sortie.core

import java.io.File
import java.io.FileInputStream
import java.nio.file.FileSystems
import java.nio.file.Files
import java.nio.file.Paths
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit

/** A file offered for sorting. [id] is the database identifier (0 = not persisted). */
data class FileEntry(
    val id: Long = 0,
    val path: String, // absolute source path
    val name: String,
    val ext: String = extensionOf(name), // lower-case extension including the dot, may be empty
    val mime: String = "",
    val size: Long = 0,
    val modTimeMs: Long = 0,
) {
    val parentPath: String get() = File(path).parent ?: ""

    companion object {
        /** ".jpg" for "photo.JPG"; "" for "noext" and for dotfiles such as ".bashrc". */
        fun extensionOf(name: String): String {
            val base = name.substringAfterLast('/')
            val i = base.lastIndexOf('.')
            return if (i <= 0) "" else base.substring(i).lowercase()
        }
    }
}

class ScanOptions(
    val recursive: Boolean = false,
    /** Leaves out names starting with "." and, when recursive, everything below hidden folders. */
    val skipHidden: Boolean = false,
    /** Glob patterns tested against the file name (e.g. "*.part"). */
    val exclude: List<String> = emptyList(),
)

class ScanResult(val files: List<FileEntry>, val skipped: List<String>)

/** Discovers regular files in a folder. Strictly read-only. */
object Scanner {
    private const val MIME_WORKERS = 4

    /**
     * Lists regular files under [root], sorted by path (case-insensitively). Symlinks and
     * non-regular files are skipped; unreadable folders are recorded in [ScanResult.skipped].
     * Throws [SorterException] when root itself cannot be read and [CancelledException] on cancel.
     */
    fun scan(root: File, opts: ScanOptions, cancel: Cancel, mime: (File) -> String = { Mime.detect(it) }): ScanResult {
        val abs = root.absoluteFile
        if (!abs.isDirectory) throw SorterException("${abs.path} is not a folder or cannot be read")
        val matchers = opts.exclude.mapNotNull { runCatching { FileSystems.getDefault().getPathMatcher("glob:$it") }.getOrNull() }
        val found = ArrayList<File>()
        val skipped = ArrayList<String>()
        val stack = ArrayDeque<File>()
        stack.add(abs)
        while (stack.isNotEmpty()) {
            cancel.check()
            val dir = stack.removeLast()
            val children = dir.listFiles()
            if (children == null) {
                if (dir == abs) throw SorterException("${abs.path} cannot be read")
                skipped += dir.path
                continue
            }
            for (c in children) {
                val n = c.name
                if (Files.isSymbolicLink(c.toPath())) continue
                if (c.isDirectory) {
                    if (opts.recursive && !(opts.skipHidden && n.startsWith("."))) stack.add(c)
                    continue
                }
                if (!c.isFile) continue // devices, sockets...
                if (CopyEngine.isTempFile(n) || (opts.skipHidden && n.startsWith("."))) continue
                if (matchers.any { it.matches(Paths.get(n)) }) continue
                found += c
            }
        }
        val out = arrayOfNulls<FileEntry>(found.size)
        val pool = Executors.newFixedThreadPool(MIME_WORKERS)
        try {
            for ((i, f) in found.withIndex()) {
                pool.execute {
                    if (!cancel.cancelled) {
                        out[i] = FileEntry(path = f.path, name = f.name, mime = runCatching { mime(f) }.getOrDefault(Mime.UNKNOWN), size = f.length(), modTimeMs = f.lastModified())
                    }
                }
            }
        } finally {
            pool.shutdown()
            while (!pool.awaitTermination(1, TimeUnit.HOURS)) { /* keep waiting */ }
        }
        cancel.check()
        val files = out.filterNotNull().sortedWith(compareBy<FileEntry> { it.path.lowercase() }.thenBy { it.path })
        return ScanResult(files, skipped)
    }
}

/** MIME detection: content first (magic numbers), extension second. */
object Mime {
    const val UNKNOWN = "application/octet-stream"

    private val byExtension = mapOf(
        ".jpg" to "image/jpeg", ".jpeg" to "image/jpeg", ".png" to "image/png", ".gif" to "image/gif", ".bmp" to "image/bmp",
        ".webp" to "image/webp", ".tif" to "image/tiff", ".tiff" to "image/tiff", ".svg" to "image/svg+xml",
        ".heic" to "image/heic", ".heif" to "image/heif", ".avif" to "image/avif", ".dng" to "image/x-adobe-dng",
        ".pdf" to "application/pdf", ".txt" to "text/plain", ".md" to "text/markdown", ".csv" to "text/csv",
        ".log" to "text/plain", ".json" to "application/json", ".xml" to "application/xml", ".yaml" to "application/yaml",
        ".yml" to "application/yaml", ".html" to "text/html", ".htm" to "text/html", ".ini" to "text/plain", ".conf" to "text/plain",
        ".mp3" to "audio/mpeg", ".flac" to "audio/flac", ".ogg" to "audio/ogg", ".oga" to "audio/ogg", ".opus" to "audio/ogg",
        ".wav" to "audio/wav", ".m4a" to "audio/mp4", ".m4b" to "audio/mp4", ".aac" to "audio/aac", ".mka" to "audio/x-matroska",
        ".mp4" to "video/mp4", ".m4v" to "video/mp4", ".mov" to "video/quicktime", ".mkv" to "video/x-matroska",
        ".webm" to "video/webm", ".avi" to "video/x-msvideo", ".3gp" to "video/3gpp",
        ".zip" to "application/zip", ".doc" to "application/msword",
        ".docx" to "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
        ".xlsx" to "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
        ".pptx" to "application/vnd.openxmlformats-officedocument.presentationml.presentation",
        ".odt" to "application/vnd.oasis.opendocument.text", ".epub" to "application/epub+zip", ".apk" to "application/vnd.android.package-archive",
    )

    fun byExtension(ext: String): String? = byExtension[ext.lowercase()]

    /** [extra] can supply types for extensions this table does not know (the platform's MIME map). */
    fun detect(file: File, extra: (String) -> String? = { null }): String {
        val ext = FileEntry.extensionOf(file.name)
        val byExt = byExtension(ext) ?: extra(ext)
        val head = try {
            FileInputStream(file).use { input ->
                val buf = ByteArray(16)
                val n = input.read(buf)
                if (n <= 0) ByteArray(0) else buf.copyOf(n)
            }
        } catch (_: Exception) {
            ByteArray(0)
        }
        val sniffed = sniff(head)
        // Containers (zip, ISO media, RIFF, Matroska) are refined by the extension: a .docx is a zip.
        if (sniffed != null && !(byExt != null && sniffed in GENERIC)) return sniffed
        return byExt ?: sniffed ?: UNKNOWN
    }

    private val GENERIC = setOf("application/zip", "video/mp4", "video/x-matroska", "audio/ogg", "audio/mpeg")

    private fun sniff(b: ByteArray): String? {
        fun at(off: Int, s: String) = b.size >= off + s.length && s.indices.all { b[off + it] == s[it].code.toByte() }
        fun bytes(vararg v: Int) = b.size >= v.size && v.indices.all { b[it] == v[it].toByte() }
        return when {
            bytes(0x89, 0x50, 0x4E, 0x47) -> "image/png"
            bytes(0xFF, 0xD8, 0xFF) -> "image/jpeg"
            at(0, "GIF8") -> "image/gif"
            at(0, "BM") && b.size >= 14 -> "image/bmp"
            at(0, "RIFF") && at(8, "WEBP") -> "image/webp"
            at(0, "RIFF") && at(8, "WAVE") -> "audio/wav"
            at(0, "RIFF") && at(8, "AVI ") -> "video/x-msvideo"
            bytes(0x49, 0x49, 0x2A, 0x00) || bytes(0x4D, 0x4D, 0x00, 0x2A) -> "image/tiff"
            at(0, "%PDF-") -> "application/pdf"
            at(0, "PK\u0003\u0004") -> "application/zip"
            at(0, "fLaC") -> "audio/flac"
            at(0, "OggS") -> "audio/ogg"
            at(0, "ID3") -> "audio/mpeg"
            bytes(0x1A, 0x45, 0xDF, 0xA3) -> "video/x-matroska"
            at(4, "ftyp") -> "video/mp4"
            else -> null
        }
    }

    fun isTextual(mime: String): Boolean =
        mime.startsWith("text/") || mime.contains("json") || mime.contains("xml") || mime.contains("javascript") || mime.contains("yaml")
}
