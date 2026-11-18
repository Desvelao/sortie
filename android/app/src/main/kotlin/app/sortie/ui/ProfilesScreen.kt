package app.sortie.ui

import android.net.Uri
import android.provider.OpenableColumns
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import app.sortie.R
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

private sealed class Ask {
    data object New : Ask()
    data class Duplicate(val p: ProfileItem) : Ask()
    data class Rename(val p: ProfileItem) : Ask()
    data class Delete(val p: ProfileItem) : Ask()
}

@Composable
fun ProfilesScreen(vm: MainViewModel, ui: UiState) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var ask by remember { mutableStateOf<Ask?>(null) }
    var exporting by remember { mutableStateOf<ProfileItem?>(null) }
    val exportedMsg = stringResource(R.string.profile_exported)
    val exportFailedMsg = stringResource(R.string.profile_export_failed)

    val importer = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri: Uri? ->
        if (uri == null) return@rememberLauncherForActivityResult
        scope.launch {
            val read = withContext(Dispatchers.IO) {
                runCatching {
                    val name = context.contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use { c ->
                        if (c.moveToFirst()) c.getString(0) else null
                    } ?: "profile.yaml"
                    val text = context.contentResolver.openInputStream(uri)!!.use { it.readBytes().decodeToString() }
                    name to text
                }
            }
            read.onSuccess { (name, text) -> vm.importProfile(name, text) }.onFailure { vm.say(it.message ?: it.toString()) }
        }
    }
    val exporter = rememberLauncherForActivityResult(ActivityResultContracts.CreateDocument("application/x-yaml")) { uri: Uri? ->
        val p = exporting
        exporting = null
        if (uri == null || p == null) return@rememberLauncherForActivityResult
        scope.launch {
            val text = vm.profileText(p.id)
            val ok = text != null && withContext(Dispatchers.IO) {
                runCatching { context.contentResolver.openOutputStream(uri, "wt")!!.use { it.write(text.toByteArray()) } }.isSuccess
            }
            vm.say(if (ok) exportedMsg else exportFailedMsg)
        }
    }

    Column(Modifier.fillMaxSize()) {
        Bar(
            title = { Text(stringResource(R.string.profiles)) },
            navigation = { BackButton { vm.open(Screen.SORTER) } },
            actions = {
                TextButton({ importer.launch(arrayOf("*/*")) }, Modifier.heightIn(min = 48.dp)) { Text(stringResource(R.string.import_profile)) }
                IconButton({ ask = Ask.New }) { Icon(Icons.Filled.Add, stringResource(R.string.new_profile)) }
            },
        )
        LazyColumn(Modifier.weight(1f), contentPadding = androidx.compose.foundation.layout.PaddingValues(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            items(ui.profiles, key = { it.id }) { p ->
                val active = p.id == ui.profileId
                var menu by remember { mutableStateOf(false) }
                androidx.compose.material3.Surface(
                    shape = MaterialTheme.shapes.large,
                    color = if (active) MaterialTheme.colorScheme.primaryContainer else MaterialTheme.colorScheme.surfaceContainer,
                    modifier = Modifier.fillMaxWidth(),
                ) {
                Row(
                    Modifier.fillMaxWidth().heightIn(min = 72.dp)
                        .clickable(enabled = p.error == null && !active) { vm.selectProfile(p.id); vm.open(Screen.SORTER) }
                        .padding(start = 20.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Column(Modifier.weight(1f).padding(vertical = 8.dp)) {
                        Text(p.name, style = MaterialTheme.typography.titleMedium)
                        if (active) Text(stringResource(R.string.profile_active), style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.primary)
                        p.error?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error) }
                    }
                    Box {
                        IconButton({ menu = true }) { Icon(Icons.Filled.MoreVert, stringResource(R.string.profile_actions, p.name)) }
                        DropdownMenu(menu, { menu = false }) {
                            if (p.error == null && !active) {
                                DropdownMenuItem(text = { Text(stringResource(R.string.use_profile)) }, onClick = { menu = false; vm.selectProfile(p.id); vm.open(Screen.SORTER) })
                            }
                            DropdownMenuItem(text = { Text(stringResource(R.string.edit)) }, onClick = { menu = false; vm.editProfile(p.id) })
                            if (p.error == null) {
                                DropdownMenuItem(text = { Text(stringResource(R.string.duplicate)) }, onClick = { menu = false; ask = Ask.Duplicate(p) })
                                DropdownMenuItem(text = { Text(stringResource(R.string.rename)) }, onClick = { menu = false; ask = Ask.Rename(p) })
                            }
                            DropdownMenuItem(text = { Text(stringResource(R.string.export)) }, onClick = {
                                menu = false
                                exporting = p
                                exporter.launch(p.id + ".yaml")
                            })
                            DropdownMenuItem(text = { Text(stringResource(R.string.delete)) }, enabled = !active, onClick = { menu = false; ask = Ask.Delete(p) })
                        }
                    }
                }
                }
            }
        }
    }

    when (val a = ask) {
        null -> Unit
        Ask.New -> TextInputDialog(stringResource(R.string.new_profile), stringResource(R.string.profile_name), onDismiss = { ask = null }) {
            ask = null
            vm.createProfile(it)
        }
        is Ask.Duplicate -> TextInputDialog(stringResource(R.string.duplicate), stringResource(R.string.profile_name), a.p.name + " 2", onDismiss = { ask = null }) {
            ask = null
            vm.duplicateProfile(a.p.id, it)
        }
        is Ask.Rename -> TextInputDialog(stringResource(R.string.rename), stringResource(R.string.profile_name), a.p.name, onDismiss = { ask = null }) {
            ask = null
            vm.renameProfile(a.p.id, it)
        }
        is Ask.Delete -> {
            val tagged by produceState(0, a.p.id) { value = vm.taggedCount(a.p.id) }
            ConfirmDialog(
                title = stringResource(R.string.delete_profile_title, a.p.name),
                text = pluralStringResource(R.plurals.delete_profile_text, tagged, tagged),
                confirm = stringResource(R.string.delete),
                onDismiss = { ask = null },
                onConfirm = {
                    ask = null
                    vm.deleteProfile(a.p.id)
                },
            )
        }
    }
}
