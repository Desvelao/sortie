package app.sortie.ui

import android.content.Intent
import android.net.Uri
import android.text.format.Formatter
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.toggleable
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Slider
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.unit.dp
import app.sortie.BuildConfig
import app.sortie.R

@Composable
fun SettingsScreen(vm: MainViewModel, ui: UiState) {
    val context = LocalContext.current
    var usage by remember { mutableLongStateOf(0) }
    LaunchedEffect(ui.cacheMb) { usage = vm.cacheSize() }
    var cache by remember { mutableFloatStateOf(ui.cacheMb.toFloat()) }

    Column(Modifier.fillMaxSize()) {
        Bar(title = { Text(stringResource(R.string.settings)) }, navigation = { BackButton { vm.open(Screen.SORTER) } })
        Column(Modifier.weight(1f).verticalScroll(rememberScrollState()).padding(16.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
            SwitchRow(stringResource(R.string.setting_filmstrip), ui.showFilmstrip, vm::setShowFilmstrip)
            SwitchRow(stringResource(R.string.setting_skip_hidden), ui.skipHidden, vm::setSkipHidden)
            Column {
                Text(stringResource(R.string.setting_cache, cache.toInt()), style = MaterialTheme.typography.bodyLarge)
                Slider(cache, { cache = it }, valueRange = 10f..1000f, onValueChangeFinished = { vm.setCacheMb(cache.toInt()) })
                Text(stringResource(R.string.cache_usage, Formatter.formatShortFileSize(context, usage)), style = MaterialTheme.typography.bodySmall)
                OutlinedButton(
                    onClick = {
                        vm.clearThumbnails()
                        usage = 0
                    },
                    modifier = Modifier.heightIn(min = 48.dp),
                ) { Text(stringResource(R.string.clear_cache)) }
            }
            Column {
                Text(stringResource(R.string.version, BuildConfig.VERSION_NAME), style = MaterialTheme.typography.bodyLarge)
                if (BuildConfig.APP_URL.isNotBlank()) {
                    Text(
                        BuildConfig.APP_URL,
                        Modifier.heightIn(min = 48.dp).clickable {
                            runCatching { context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(BuildConfig.APP_URL))) }
                        },
                        color = MaterialTheme.colorScheme.primary,
                    )
                }
            }
        }
    }
}

@Composable
private fun SwitchRow(label: String, checked: Boolean, onChange: (Boolean) -> Unit) {
    Row(
        Modifier.fillMaxWidth().heightIn(min = 48.dp).toggleable(checked, role = Role.Switch, onValueChange = onChange),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(label, Modifier.weight(1f), style = MaterialTheme.typography.bodyLarge)
        Switch(checked, null)
    }
}
