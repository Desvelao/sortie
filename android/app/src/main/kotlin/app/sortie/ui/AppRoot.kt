package app.sortie.ui

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.compose.foundation.layout.WindowInsets

/** The permission gate until storage access is granted, then the screen named by [UiState.screen]. */
@Composable
fun AppRoot(access: Boolean, recheck: () -> Unit) {
    if (!access) {
        PermissionGate(recheck)
        return
    }
    val vm: MainViewModel = viewModel()
    LaunchedEffect(Unit) { vm.start() }
    val ui by vm.ui.collectAsStateWithLifecycle()
    val snackbar = remember { SnackbarHostState() }
    LaunchedEffect(ui.message) {
        ui.message?.let {
            snackbar.showSnackbar(it)
            vm.messageShown()
        }
    }
    BackHandler(ui.screen != Screen.SORTER) {
        vm.open(if (ui.screen == Screen.EDITOR) Screen.PROFILES else Screen.SORTER)
    }
    // The activity already insets the content, so the scaffold adds no insets of its own.
    Scaffold(contentWindowInsets = WindowInsets(0), snackbarHost = { SnackbarHost(snackbar) }) { pad ->
        Box(Modifier.fillMaxSize().padding(pad)) {
            if (!ui.ready) {
                CircularProgressIndicator(Modifier.align(Alignment.Center))
            } else {
                when (ui.screen) {
                    Screen.SORTER -> SorterScreen(vm, ui)
                    Screen.PROFILES -> ProfilesScreen(vm, ui)
                    Screen.EDITOR -> ProfileEditor(vm, ui)
                    Screen.SETTINGS -> SettingsScreen(vm, ui)
                }
            }
        }
    }
}
