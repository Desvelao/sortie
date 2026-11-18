package app.sortie.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.selection.selectable
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.Close
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.graphics.Color
import androidx.compose.material.icons.filled.Warning
import androidx.compose.foundation.background
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.unit.dp
import app.sortie.R
import app.sortie.copy.CopyController
import app.sortie.copy.CopyState
import app.sortie.core.ConflictPolicy
import app.sortie.core.CopyStatus
import app.sortie.core.DestStatus
import app.sortie.data.Storage

/**
 * Plans a copy, lets the user pick what happens to conflicts, shows the progress and the results.
 * Closing the sheet never stops a running copy; only Cancel does.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun CopySheet(copy: CopyController, state: CopyState, defaultPolicy: String, onClose: () -> Unit) {
    ModalBottomSheet(onDismissRequest = onClose) {
        Column(Modifier.fillMaxWidth().padding(horizontal = 16.dp).padding(bottom = 24.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            when (state) {
                CopyState.Idle -> Unit
                is CopyState.Planning -> Planning(state, copy)
                is CopyState.Planned -> Planned(copy, state, defaultPolicy, onClose)
                is CopyState.Running -> Running(copy, state, onClose)
                is CopyState.Finished -> Finished(state, copy, onClose)
                is CopyState.Failed -> {
                    Text(stringResource(R.string.copy_failed), style = MaterialTheme.typography.titleMedium)
                    Text(state.message)
                    OutlinedButton({ copy.dismiss(); onClose() }, Modifier.heightIn(min = 48.dp)) { Text(stringResource(R.string.close)) }
                }
            }
        }
    }
}

@Composable
private fun Planning(state: CopyState.Planning, copy: CopyController) {
    Text(stringResource(R.string.checking_destinations), style = MaterialTheme.typography.titleMedium)
    if (state.total > 0) {
        LinearProgressIndicator(progress = { state.done.toFloat() / state.total }, Modifier.fillMaxWidth())
        Text(stringResource(R.string.copy_progress, state.done, state.total))
    } else {
        CircularProgressIndicator()
    }
    OutlinedButton({ copy.cancel() }, Modifier.heightIn(min = 48.dp)) { Text(stringResource(R.string.cancel)) }
}

@Composable
private fun Planned(copy: CopyController, state: CopyState.Planned, defaultPolicy: String, onClose: () -> Unit) {
    var policy by remember { mutableStateOf(ConflictPolicy.of(defaultPolicy)) }
    var confirm by remember { mutableStateOf(false) }
    val counts = state.checks.groupingBy { it.status }.eachCount()
    val toCopy = counts[DestStatus.READY] ?: 0
    val conflicts = counts[DestStatus.CONFLICT] ?: 0

    Text(stringResource(if (state.all) R.string.copy_all_title else R.string.copy_current_title), style = MaterialTheme.typography.titleMedium)
    if (state.jobs.isEmpty()) {
        Text(stringResource(R.string.nothing_to_copy))
        OutlinedButton({ copy.dismiss(); onClose() }, Modifier.heightIn(min = 48.dp)) { Text(stringResource(R.string.close)) }
        return
    }
    Text(pluralStringResource(R.plurals.copy_jobs, state.jobs.size, state.jobs.size))
    for (s in DestStatus.entries) {
        val n = counts[s] ?: continue
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Icon(statusIcon(s), null, Modifier.size(20.dp))
            Text("${statusLabel(s)}: $n")
        }
    }
    if (conflicts > 0) {
        Text(stringResource(R.string.on_conflict), style = MaterialTheme.typography.titleSmall)
        for ((p, label) in listOf(
            ConflictPolicy.SKIP to R.string.policy_skip,
            ConflictPolicy.RENAME to R.string.policy_rename,
            ConflictPolicy.REPLACE to R.string.policy_replace,
        )) {
            Row(
                Modifier.fillMaxWidth().heightIn(min = 48.dp).selectable(policy == p, role = Role.RadioButton, onClick = { policy = p }),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                RadioButton(policy == p, onClick = null)
                Text(stringResource(label), Modifier.padding(start = 8.dp))
            }
        }
    }
    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        Button(
            onClick = { if (policy == ConflictPolicy.REPLACE && conflicts > 0) confirm = true else copy.start(policy, false) },
            enabled = toCopy > 0 || conflicts > 0,
            modifier = Modifier.heightIn(min = 48.dp),
        ) { Text(stringResource(R.string.start_copy)) }
        OutlinedButton({ copy.dismiss(); onClose() }, Modifier.heightIn(min = 48.dp)) { Text(stringResource(R.string.cancel)) }
    }
    if (confirm) {
        ConfirmDialog(
            title = stringResource(R.string.replace_title),
            text = pluralStringResource(R.plurals.replace_warning, conflicts, conflicts),
            confirm = stringResource(R.string.policy_replace),
            onDismiss = { confirm = false },
            onConfirm = {
                confirm = false
                copy.start(ConflictPolicy.REPLACE, true)
            },
        )
    }
}

@Composable
private fun Running(copy: CopyController, s: CopyState.Running, onClose: () -> Unit) {
    Text(stringResource(R.string.copying), style = MaterialTheme.typography.titleLarge)
    Text(stringResource(R.string.copy_progress, s.done, s.total), style = MaterialTheme.typography.displaySmall)
    LinearProgressIndicator(
        progress = { if (s.total == 0) 0f else s.done.toFloat() / s.total },
        modifier = Modifier.fillMaxWidth().heightIn(min = 8.dp),
        strokeCap = androidx.compose.ui.graphics.StrokeCap.Round,
    )
    if (s.name.isNotEmpty()) Text(s.name, maxLines = 1)
    if (s.bytesTotal > 0) LinearProgressIndicator(progress = { (s.bytes.toFloat() / s.bytesTotal).coerceIn(0f, 1f) }, Modifier.fillMaxWidth())
    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        Button(onClose, Modifier.heightIn(min = 48.dp)) { Text(stringResource(R.string.run_in_background)) }
        OutlinedButton({ copy.cancel() }, enabled = !s.cancelling, modifier = Modifier.heightIn(min = 48.dp)) {
            Text(stringResource(if (s.cancelling) R.string.cancelling else R.string.cancel))
        }
    }
    Text(stringResource(R.string.background_hint), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
}

@Composable
private fun Finished(state: CopyState.Finished, copy: CopyController, onClose: () -> Unit) {
    val copied = state.results.count { it.status.ok }
    val failed = state.results.count { it.status == CopyStatus.FAILED }
    val skipped = state.results.size - copied - failed
    Text(stringResource(R.string.copy_finished), style = MaterialTheme.typography.titleLarge)
    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        val c = MaterialTheme.colorScheme
        ResultTile(Modifier.weight(1f), stringResource(R.string.tile_copied), copied, Icons.Filled.Check, c.secondaryContainer, c.onSecondaryContainer)
        ResultTile(Modifier.weight(1f), stringResource(R.string.tile_skipped), skipped, Icons.Filled.Warning, c.surfaceVariant, c.onSurfaceVariant)
        ResultTile(Modifier.weight(1f), stringResource(R.string.tile_failed), failed, Icons.Filled.Close, c.errorContainer, c.onErrorContainer)
    }
    LazyColumn(Modifier.heightIn(max = 360.dp)) {
        items(state.results) { r ->
            Row(Modifier.fillMaxWidth().heightIn(min = 48.dp).padding(vertical = 4.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Icon(if (r.status.ok) Icons.Filled.Check else Icons.Filled.Close, null, Modifier.size(20.dp))
                Column {
                    Text("${r.job.name} → ${r.job.tag.name}: ${copyStatusLabel(r.status)}", style = MaterialTheme.typography.bodyMedium)
                    val where = if (r.destPath.isNotEmpty()) Storage.display(r.destPath) else ""
                    val line = listOf(where, r.detail).filter { it.isNotEmpty() }.joinToString(" · ")
                    if (line.isNotEmpty()) Text(line, style = MaterialTheme.typography.bodySmall)
                }
            }
        }
    }
    Button({ copy.dismiss(); onClose() }, Modifier.heightIn(min = 48.dp)) { Text(stringResource(R.string.close)) }
}

/** A count with a label and an icon, so the meaning never rests on colour alone. */
@Composable
private fun ResultTile(modifier: Modifier, label: String, count: Int, icon: androidx.compose.ui.graphics.vector.ImageVector, container: Color, content: Color) {
    Column(modifier.background(container, MaterialTheme.shapes.medium).padding(12.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(4.dp)) {
            Icon(icon, null, Modifier.size(14.dp), tint = content)
            Text(label, style = MaterialTheme.typography.labelMedium, color = content, maxLines = 1)
        }
        Text(count.toString(), style = MaterialTheme.typography.headlineSmall, color = content)
    }
}
