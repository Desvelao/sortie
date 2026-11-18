package app.sortie.media

import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.Canvas
import android.graphics.Color
import android.graphics.Matrix
import android.graphics.pdf.PdfRenderer
import android.media.MediaMetadataRetriever
import android.os.ParcelFileDescriptor
import android.util.LruCache
import androidx.exifinterface.media.ExifInterface
import app.sortie.core.FileEntry
import java.io.File
import java.security.MessageDigest

/** Decodes pictures of files for the preview and the filmstrip. Blocking: call off the main thread. */
object Pictures {
    /** A bitmap of the file no larger than about [maxPx] on its long side, or null when it has no picture. */
    fun load(f: FileEntry, maxPx: Int): Bitmap? = try {
        when {
            f.ext == ".pdf" -> pdfPage(f.path, maxPx)
            Metadata.isVideo(f.ext) -> videoFrame(f.path, maxPx)
            Metadata.isAudio(f.ext) -> audioCover(f.path, maxPx)
            Metadata.isImage(f.ext) || f.mime.startsWith("image/") -> image(f.path, maxPx)
            else -> null
        }
    } catch (_: Throwable) {
        null // out of memory or a broken file: no picture
    }

    private fun image(path: String, maxPx: Int): Bitmap? {
        val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
        BitmapFactory.decodeFile(path, bounds)
        if (bounds.outWidth <= 0) return null
        var sample = 1
        while (maxOf(bounds.outWidth, bounds.outHeight) / (sample * 2) >= maxPx) sample *= 2
        val bmp = BitmapFactory.decodeFile(path, BitmapFactory.Options().apply { inSampleSize = sample }) ?: return null
        val orientation = runCatching { ExifInterface(path).getAttributeInt(ExifInterface.TAG_ORIENTATION, ExifInterface.ORIENTATION_NORMAL) }
            .getOrDefault(ExifInterface.ORIENTATION_NORMAL)
        val m = Matrix()
        when (orientation) {
            ExifInterface.ORIENTATION_ROTATE_90 -> m.postRotate(90f)
            ExifInterface.ORIENTATION_ROTATE_180 -> m.postRotate(180f)
            ExifInterface.ORIENTATION_ROTATE_270 -> m.postRotate(270f)
            ExifInterface.ORIENTATION_FLIP_HORIZONTAL -> m.postScale(-1f, 1f)
            ExifInterface.ORIENTATION_FLIP_VERTICAL -> m.postScale(1f, -1f)
            ExifInterface.ORIENTATION_TRANSPOSE -> { m.postRotate(90f); m.postScale(-1f, 1f) }
            ExifInterface.ORIENTATION_TRANSVERSE -> { m.postRotate(270f); m.postScale(-1f, 1f) }
            else -> return bmp
        }
        return Bitmap.createBitmap(bmp, 0, 0, bmp.width, bmp.height, m, true)
    }

    private fun pdfPage(path: String, maxPx: Int): Bitmap? =
        ParcelFileDescriptor.open(File(path), ParcelFileDescriptor.MODE_READ_ONLY).use { fd ->
            PdfRenderer(fd).use { r ->
                if (r.pageCount == 0) return null
                r.openPage(0).use { page ->
                    val scale = maxPx.toFloat() / maxOf(page.width, page.height)
                    val bmp = Bitmap.createBitmap((page.width * scale).toInt().coerceAtLeast(1), (page.height * scale).toInt().coerceAtLeast(1), Bitmap.Config.ARGB_8888)
                    Canvas(bmp).drawColor(Color.WHITE)
                    page.render(bmp, null, null, PdfRenderer.Page.RENDER_MODE_FOR_DISPLAY)
                    bmp
                }
            }
        }

    private fun videoFrame(path: String, maxPx: Int): Bitmap? = retriever(path) { it.frameAtTime?.let { b -> shrink(b, maxPx) } }

    private fun audioCover(path: String, maxPx: Int): Bitmap? =
        retriever(path) { r -> r.embeddedPicture?.let { BitmapFactory.decodeByteArray(it, 0, it.size) }?.let { shrink(it, maxPx) } }

    private fun <T> retriever(path: String, body: (MediaMetadataRetriever) -> T): T {
        val r = MediaMetadataRetriever()
        try {
            r.setDataSource(path)
            return body(r)
        } finally {
            runCatching { r.release() }
        }
    }

    private fun shrink(b: Bitmap, maxPx: Int): Bitmap {
        val long = maxOf(b.width, b.height)
        if (long <= maxPx) return b
        val s = maxPx.toFloat() / long
        return Bitmap.createScaledBitmap(b, (b.width * s).toInt().coerceAtLeast(1), (b.height * s).toInt().coerceAtLeast(1), true)
    }
}

/**
 * Thumbnails cached as JPEGs on disk, keyed by (path, size, modification time), plus a small
 * in-memory cache. The folder is size-limited (least recently used files are pruned) and safe to
 * delete.
 */
class Thumbnails(private val dir: File, @Volatile var maxBytes: Long) {
    private val memory = LruCache<String, Bitmap>(120)
    private val none = HashSet<String>()

    private fun key(f: FileEntry): String {
        val d = MessageDigest.getInstance("SHA-1").digest("${f.path}|${f.size}|${f.modTimeMs}".toByteArray())
        return d.joinToString("") { "%02x".format(it) }
    }

    /** The thumbnail of [f], generated on a miss; null when the file has no picture. */
    fun load(f: FileEntry): Bitmap? {
        val k = key(f)
        memory.get(k)?.let { return it }
        synchronized(none) { if (k in none) return null }
        val file = File(dir, "$k.jpg")
        var bmp: Bitmap? = null
        if (file.isFile) {
            bmp = BitmapFactory.decodeFile(file.path)
            if (bmp != null) file.setLastModified(System.currentTimeMillis()) // recently used
        }
        if (bmp == null) {
            bmp = Pictures.load(f, SIZE)
            if (bmp != null) {
                runCatching {
                    dir.mkdirs()
                    val tmp = File(dir, "$k.tmp")
                    tmp.outputStream().use { bmp.compress(Bitmap.CompressFormat.JPEG, 80, it) }
                    tmp.renameTo(file)
                }
            }
        }
        if (bmp == null) synchronized(none) { none += k } else memory.put(k, bmp)
        return bmp
    }

    fun sizeBytes(): Long = dir.listFiles()?.sumOf { it.length() } ?: 0

    /** Deletes the least recently used thumbnails until the folder fits the limit. */
    fun prune(): Int {
        val files = dir.listFiles()?.sortedBy { it.lastModified() } ?: return 0
        var total = files.sumOf { it.length() }
        var removed = 0
        for (f in files) {
            if (total <= maxBytes) break
            total -= f.length()
            if (f.delete()) removed++
        }
        return removed
    }

    fun clear() {
        dir.listFiles()?.forEach { it.delete() }
        memory.evictAll()
        synchronized(none) { none.clear() }
    }

    companion object {
        const val SIZE = 256
    }
}
