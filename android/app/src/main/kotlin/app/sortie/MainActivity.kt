package app.sortie

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.material3.Surface
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import app.sortie.data.Storage
import app.sortie.ui.AppRoot
import app.sortie.ui.theme.AppTheme

class MainActivity : ComponentActivity() {
    private var access by mutableStateOf(false)

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        access = Storage.hasAccess(this)
        setContent {
            AppTheme {
                // The surface fills the whole window (including behind the system bars); only its content is inset.
                Surface(Modifier.fillMaxSize()) {
                    Box(Modifier.fillMaxSize().safeDrawingPadding()) {
                        AppRoot(access = access, recheck = { access = Storage.hasAccess(this@MainActivity) })
                    }
                }
            }
        }
    }

    /** The user may have granted access in the system settings and come back. */
    override fun onResume() {
        super.onResume()
        access = Storage.hasAccess(this)
    }
}
