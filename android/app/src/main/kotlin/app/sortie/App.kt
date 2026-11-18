package app.sortie

import android.app.Application
import app.sortie.copy.CopyController
import app.sortie.core.ProfileStore
import app.sortie.core.Sorter
import app.sortie.data.AppDatabase
import app.sortie.data.Prefs
import app.sortie.data.RoomStore
import app.sortie.data.Storage
import app.sortie.media.Metadata
import app.sortie.media.Thumbnails
import java.io.File
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob

/** The objects that live as long as the process. Created lazily: the database opens on first use, off the main thread. */
class AppContainer(app: Application) {
    val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    val prefs = Prefs(app)
    val profiles = ProfileStore(File(app.filesDir, "profiles"))
    val thumbnails = Thumbnails(File(app.cacheDir, "thumbnails"), prefs.thumbnailCacheMb * 1024L * 1024L)
    val sorter: Sorter by lazy { Sorter(RoomStore(AppDatabase.open(app)), Storage.home) { Metadata.fields(it) } }
    val copy: CopyController by lazy { CopyController(app, sorter, scope) }
}

class App : Application() {
    lateinit var container: AppContainer
        private set

    override fun onCreate() {
        super.onCreate()
        container = AppContainer(this)
    }
}
