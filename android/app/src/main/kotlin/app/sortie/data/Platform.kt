package app.sortie.data

import android.Manifest
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Environment
import android.provider.Settings
import android.webkit.MimeTypeMap
import androidx.core.content.ContextCompat
import androidx.core.content.FileProvider
import app.sortie.core.FileEntry
import app.sortie.core.Mime
import java.io.File

/** Application-wide settings (not use-case settings: those live in profiles). */
class Prefs(context: Context) {
    private val sp = context.getSharedPreferences("settings", Context.MODE_PRIVATE)

    var activeProfile: String
        get() = sp.getString("active_profile", "") ?: ""
        set(v) = sp.edit().putString("active_profile", v).apply()

    var showFilmstrip: Boolean
        get() = sp.getBoolean("show_filmstrip", true)
        set(v) = sp.edit().putBoolean("show_filmstrip", v).apply()

    var skipHidden: Boolean
        get() = sp.getBoolean("skip_hidden", true)
        set(v) = sp.edit().putBoolean("skip_hidden", v).apply()

    var thumbnailCacheMb: Int
        get() = sp.getInt("thumbnail_cache_mb", 100)
        set(v) = sp.edit().putInt("thumbnail_cache_mb", v.coerceIn(10, 2000)).apply()

    var askedNotifications: Boolean
        get() = sp.getBoolean("asked_notifications", false)
        set(v) = sp.edit().putBoolean("asked_notifications", v).apply()
}

/** Shared storage as real paths: the permission that makes them readable, and where the volumes are. */
object Storage {
    /** The primary shared storage root; "~" in a profile expands to it. */
    val home: String get() = Environment.getExternalStorageDirectory().path

    fun hasAccess(context: Context): Boolean =
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            Environment.isExternalStorageManager()
        } else {
            ContextCompat.checkSelfPermission(context, Manifest.permission.WRITE_EXTERNAL_STORAGE) == PackageManager.PERMISSION_GRANTED
        }

    /** The system screen where the user grants "All files access" to this app (Android 11+). */
    fun accessIntent(context: Context): Intent =
        Intent(Settings.ACTION_MANAGE_APP_ALL_FILES_ACCESS_PERMISSION, Uri.parse("package:${context.packageName}"))

    fun fallbackAccessIntent(): Intent = Intent(Settings.ACTION_MANAGE_ALL_FILES_ACCESS_PERMISSION)

    /** Roots of the mounted volumes: internal shared storage first, then SD cards and USB drives. */
    fun roots(context: Context): List<File> {
        val out = LinkedHashSet<String>()
        out += home
        for (d in context.getExternalFilesDirs(null)) {
            val p = d?.path ?: continue
            val i = p.indexOf("/Android/")
            if (i > 0) out += p.substring(0, i)
        }
        return out.map { File(it) }.filter { it.isDirectory }
    }

    /** "~/Pictures" for a path under the primary storage, so profiles stay portable; else the path. */
    fun portable(path: String): String {
        val h = home.trimEnd('/')
        return when {
            path == h -> "~"
            path.startsWith("$h/") -> "~" + path.substring(h.length)
            else -> path
        }
    }

    /** A short label for a path: the primary storage root is hidden. */
    fun display(path: String): String = portable(path).removePrefix("~/").let { if (it == "~") "/" else it }

    fun mime(file: File): String = Mime.detect(file) { ext -> MimeTypeMap.getSingleton().getMimeTypeFromExtension(ext.removePrefix(".")) }

    /** Opens a file with whatever app the user has for it; false when there is none. */
    fun openWith(context: Context, f: FileEntry): Boolean = try {
        val uri = FileProvider.getUriForFile(context, context.packageName + ".files", File(f.path))
        val intent = Intent(Intent.ACTION_VIEW).setDataAndType(uri, f.mime.ifEmpty { "*/*" })
            .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK)
        context.startActivity(intent)
        true
    } catch (_: Exception) {
        false
    }
}
