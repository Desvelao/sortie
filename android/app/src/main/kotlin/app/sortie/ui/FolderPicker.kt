package app.sortie.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Button
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import app.sortie.R
import app.sortie.data.Storage
import java.io.File

/**
 * A folder browser over real paths: the storage volumes at the top, then their folders.
 * [onPick] gets the folder that is open. With [allowCreate] a new folder can be made first.
 */
@Composable
fun FolderPicker(title: String, start: String, allowCreate: Boolean, onDismiss: () -> Unit, onPick: (String) -> Unit) {
    val context = LocalContext.current
    val roots = remember { Storage.roots(context) }
    var dir by remember { mutableStateOf(start.takeIf { File(it).isDirectory }?.let { File(it) }) }
    var version by remember { mutableIntStateOf(0) }
    var creating by remember { mutableStateOf(false) }
    // the message is a string resource id plus the path it is about, resolved where it is shown
    var error by remember { mutableStateOf<Pair<Int, String>?>(null) }

    Dialog(onDismissRequest = onDismiss, properties = DialogProperties(usePlatformDefaultWidth = false, decorFitsSystemWindows = false)) {
        Surface(Modifier.fillMaxSize()) {
            Column(Modifier.fillMaxSize().safeDrawingPadding()) {
                Bar(
                    title = { Text(title) },
                    navigation = { BackButton(onDismiss) },
                )
                val current = dir
                Row(Modifier.fillMaxWidth().padding(horizontal = 16.dp), verticalAlignment = Alignment.CenterVertically) {
                    Text(
                        if (current == null) stringResource(R.string.storage_volumes) else Storage.display(current.path),
                        Modifier.weight(1f),
                        style = MaterialTheme.typography.bodyMedium,
                    )
                    if (current != null && allowCreate) {
                        TextButton({ creating = true }, Modifier.heightIn(min = 48.dp)) { Text(stringResource(R.string.new_folder)) }
                    }
                }
                HorizontalDivider()
                val entries = remember(current, version) {
                    if (current == null) roots else current.listFiles { f -> f.isDirectory }?.sortedBy { it.name.lowercase() } ?: emptyList()
                }
                LazyColumn(Modifier.weight(1f)) {
                    if (current != null) {
                        item("up") {
                            Row(
                                Modifier.fillMaxWidth().heightIn(min = 48.dp).clickable {
                                    dir = if (roots.any { it.path == current.path }) null else current.parentFile
                                }.padding(horizontal = 16.dp),
                                verticalAlignment = Alignment.CenterVertically,
                                horizontalArrangement = Arrangement.spacedBy(12.dp),
                            ) { Text(stringResource(R.string.parent_folder)) }
                        }
                    }
                    items(entries, key = { it.path }) { f ->
                        Row(
                            Modifier.fillMaxWidth().heightIn(min = 48.dp).clickable { dir = f }.padding(horizontal = 16.dp),
                            verticalAlignment = Alignment.CenterVertically,
                            horizontalArrangement = Arrangement.spacedBy(12.dp),
                        ) {
                            Icon(painterResource(R.drawable.ic_folder), null)
                            Text(if (current == null) Storage.display(f.path).ifEmpty { f.name } else f.name)
                        }
                    }
                    if (entries.isEmpty()) {
                        item("empty") { Text(stringResource(R.string.no_subfolders), Modifier.padding(16.dp)) }
                    }
                }
                HorizontalDivider()
                Button(
                    onClick = { current?.let { onPick(it.path) } },
                    enabled = current != null,
                    modifier = Modifier.fillMaxWidth().padding(16.dp).heightIn(min = 48.dp),
                ) { Text(stringResource(R.string.use_this_folder)) }
            }
        }
    }

    if (creating) {
        TextInputDialog(
            title = stringResource(R.string.new_folder),
            label = stringResource(R.string.folder_name),
            onDismiss = { creating = false },
            onConfirm = { name ->
                val target = File(dir ?: return@TextInputDialog, name)
                if (name.contains('/') || name == "." || name == "..") {
                    error = R.string.invalid_folder_name to ""
                } else if (!target.isDirectory && !target.mkdirs()) {
                    error = R.string.folder_not_created to target.path
                } else {
                    dir = target
                    version++
                }
                creating = false
            },
        )
    }
    error?.let { (res, arg) ->
        ConfirmDialog(stringResource(R.string.new_folder), stringResource(res, arg), stringResource(R.string.ok), { error = null }, { error = null })
    }
}
