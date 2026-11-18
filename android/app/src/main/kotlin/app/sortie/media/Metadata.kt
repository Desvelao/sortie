package app.sortie.media

import android.graphics.pdf.PdfRenderer
import android.media.MediaMetadataRetriever
import android.os.ParcelFileDescriptor
import androidx.exifinterface.media.ExifInterface
import app.sortie.core.FileEntry
import java.io.File
import java.time.LocalDateTime
import java.time.ZoneOffset
import java.time.format.DateTimeFormatter

/**
 * Normalised, lower-case-keyed metadata of a file, so profiles can use any of it as a variable
 * source. Keys that do not apply to the file type are absent:
 *
 *     date      capture/creation date, RFC 3339 (photos, videos); for audio the bare release year
 *     year      4-digit year of date
 *     title, author, artist, album, genre, camera
 *     pages, duration (seconds), width, height, format
 *
 * An unreadable or unsupported file yields an empty map, never an error.
 */
object Metadata {
    val FIELD_NAMES = listOf(
        "date", "year", "title", "author", "artist", "album", "genre", "camera", "pages", "duration", "width", "height", "format",
    )

    private val IMAGE = setOf(".jpg", ".jpeg", ".tif", ".tiff", ".heic", ".heif", ".png", ".webp", ".dng", ".avif")
    private val AUDIO = setOf(".mp3", ".flac", ".ogg", ".oga", ".opus", ".m4a", ".m4b", ".wav", ".aac")
    private val VIDEO = setOf(".mp4", ".m4v", ".mov", ".mkv", ".webm", ".mka", ".3gp", ".avi")

    fun isImage(ext: String) = ext in IMAGE || ext == ".gif" || ext == ".bmp"
    fun isAudio(ext: String) = ext in AUDIO
    fun isVideo(ext: String) = ext in VIDEO

    fun fields(path: String): Map<String, String> {
        val out = HashMap<String, String>()
        try {
            when (val ext = FileEntry.extensionOf(path)) {
                in IMAGE -> image(path, out)
                ".pdf" -> pdf(path, out)
                in AUDIO -> media(path, out, audio = true, ext = ext)
                in VIDEO -> media(path, out, audio = false, ext = ext)
            }
        } catch (_: Throwable) {
            // a broken file must never block sorting
        }
        return out
    }

    private fun put(out: MutableMap<String, String>, k: String, v: String?) {
        val t = v?.trim().orEmpty()
        if (t.isNotEmpty()) out[k] = t
    }

    private fun putDate(out: MutableMap<String, String>, t: LocalDateTime?) {
        if (t == null || t.year <= 1) return
        out["date"] = t.atOffset(ZoneOffset.UTC).format(DateTimeFormatter.ISO_OFFSET_DATE_TIME)
        out["year"] = t.year.toString()
    }

    private val exifDate = DateTimeFormatter.ofPattern("uuuu:MM:dd HH:mm:ss")

    private fun image(path: String, out: MutableMap<String, String>) {
        val x = ExifInterface(path)
        val raw = x.getAttribute(ExifInterface.TAG_DATETIME_ORIGINAL) ?: x.getAttribute(ExifInterface.TAG_DATETIME)
        putDate(out, raw?.let { runCatching { LocalDateTime.parse(it.trim(), exifDate) }.getOrNull() })
        val make = x.getAttribute(ExifInterface.TAG_MAKE)?.trim().orEmpty()
        val model = x.getAttribute(ExifInterface.TAG_MODEL)?.trim().orEmpty()
        put(out, "camera", if (model.startsWith(make, ignoreCase = true)) model else "$make $model")
        val w = x.getAttributeInt(ExifInterface.TAG_IMAGE_WIDTH, 0)
        val h = x.getAttributeInt(ExifInterface.TAG_IMAGE_LENGTH, 0)
        if (w > 0 && h > 0) {
            out["width"] = w.toString()
            out["height"] = h.toString()
        }
    }

    private fun pdf(path: String, out: MutableMap<String, String>) {
        ParcelFileDescriptor.open(File(path), ParcelFileDescriptor.MODE_READ_ONLY).use { fd ->
            PdfRenderer(fd).use { if (it.pageCount > 0) out["pages"] = it.pageCount.toString() }
        }
    }

    // "20210315T101500.000Z", as Android reports the creation time of a video
    private val mediaDate = DateTimeFormatter.ofPattern("uuuuMMdd'T'HHmmss[.SSS]'Z'")

    private fun media(path: String, out: MutableMap<String, String>, audio: Boolean, ext: String) {
        val r = MediaMetadataRetriever()
        try {
            r.setDataSource(path)
            fun s(key: Int) = r.extractMetadata(key)
            put(out, "title", s(MediaMetadataRetriever.METADATA_KEY_TITLE))
            val artist = s(MediaMetadataRetriever.METADATA_KEY_ARTIST) ?: s(MediaMetadataRetriever.METADATA_KEY_ALBUMARTIST)
            put(out, "artist", artist)
            put(out, "author", s(MediaMetadataRetriever.METADATA_KEY_AUTHOR) ?: artist)
            put(out, "album", s(MediaMetadataRetriever.METADATA_KEY_ALBUM))
            put(out, "genre", s(MediaMetadataRetriever.METADATA_KEY_GENRE))
            put(out, "format", ext.removePrefix(".").uppercase())
            s(MediaMetadataRetriever.METADATA_KEY_DURATION)?.toLongOrNull()?.let { if (it > 0) out["duration"] = (it / 1000).toString() }
            val date = s(MediaMetadataRetriever.METADATA_KEY_DATE)?.trim()
            if (audio) {
                val year = s(MediaMetadataRetriever.METADATA_KEY_YEAR)?.trim()?.take(4)?.toIntOrNull()
                    ?: date?.take(4)?.toIntOrNull()
                if (year != null && year > 0) {
                    out["year"] = year.toString()
                    out["date"] = year.toString()
                }
            } else {
                val t = date?.let { runCatching { LocalDateTime.parse(it, mediaDate) }.getOrNull() }
                // containers without a creation time report the 1904 epoch
                if (t != null && t.year > 1970) putDate(out, t)
                val w = s(MediaMetadataRetriever.METADATA_KEY_VIDEO_WIDTH)?.toIntOrNull() ?: 0
                val h = s(MediaMetadataRetriever.METADATA_KEY_VIDEO_HEIGHT)?.toIntOrNull() ?: 0
                if (w > 0 && h > 0) {
                    out["width"] = w.toString()
                    out["height"] = h.toString()
                }
            }
        } finally {
            runCatching { r.release() }
        }
    }
}
