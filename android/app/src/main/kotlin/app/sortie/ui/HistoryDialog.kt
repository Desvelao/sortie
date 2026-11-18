package app.sortie.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.produceState
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import app.sortie.R
import app.sortie.core.FileEntry
import app.sortie.core.HistoryEntry
import app.sortie.data.Storage
import java.text.DateFormat
import java.util.Date

/** Every copy attempt of one file, newest first as the store returns them. */
@Composable
fun HistoryDialog(vm: MainViewModel, file: FileEntry, onDismiss: () -> Unit) {
    val entries by produceState<List<HistoryEntry>?>(null, file.id) { value = vm.history(file) }
    val format = DateFormat.getDateTimeInstance(DateFormat.MEDIUM, DateFormat.SHORT)
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.history_title, file.name)) },
        text = {
            val list = entries
            when {
                list == null -> Text(stringResource(R.string.loading))
                list.isEmpty() -> Text(stringResource(R.string.history_empty))
                else -> LazyColumn(Modifier.heightIn(max = 420.dp)) {
                    items(list, key = { it.id }) { h ->
                        Column(Modifier.fillMaxWidth().padding(vertical = 6.dp)) {
                            val time = if (h.copiedAtMs > 0) format.format(Date(h.copiedAtMs)) else stringResource(R.string.history_in_progress)
                            Text("${h.tagName} · ${h.status} · $time", style = MaterialTheme.typography.bodyMedium)
                            if (h.destPath.isNotEmpty()) Text(Storage.display(h.destPath), style = MaterialTheme.typography.bodySmall)
                            if (h.message.isNotEmpty()) Text(h.message, style = MaterialTheme.typography.bodySmall)
                        }
                    }
                }
            }
        },
        confirmButton = { TextButton(onDismiss, Modifier.heightIn(min = 48.dp)) { Text(stringResource(R.string.close)) } },
    )
}
