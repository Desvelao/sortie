package app.sortie.ui

import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Info
import androidx.compose.material.icons.filled.Warning
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import app.sortie.R
import app.sortie.core.CopyStatus
import app.sortie.core.DestStatus
import app.sortie.core.Tag
import app.sortie.core.Tags

/** The status as words: colour and icons are never the only cue. */
@Composable
fun statusLabel(s: DestStatus): String = stringResource(
    when (s) {
        DestStatus.READY -> R.string.status_ready
        DestStatus.ALREADY_COPIED -> R.string.status_already_copied
        DestStatus.CONFLICT -> R.string.status_conflict
        DestStatus.UNRESOLVED_VAR -> R.string.status_unresolved
        DestStatus.INVALID_PATH -> R.string.status_invalid_path
        DestStatus.ERROR -> R.string.status_error
    },
)

fun statusIcon(s: DestStatus): ImageVector = when (s) {
    DestStatus.READY -> Icons.Filled.Check
    DestStatus.ALREADY_COPIED -> Icons.Filled.CheckCircle
    DestStatus.CONFLICT -> Icons.Filled.Warning
    DestStatus.UNRESOLVED_VAR -> Icons.Filled.Info
    DestStatus.INVALID_PATH, DestStatus.ERROR -> Icons.Filled.Close
}

@Composable
fun copyStatusLabel(s: CopyStatus): String = stringResource(
    when (s) {
        CopyStatus.COPIED -> R.string.result_copied
        CopyStatus.REPLACED -> R.string.result_replaced
        CopyStatus.RENAMED -> R.string.result_renamed
        CopyStatus.ALREADY_COPIED -> R.string.status_already_copied
        CopyStatus.CONFLICT_SKIPPED -> R.string.result_conflict_skipped
        CopyStatus.SKIPPED_INVALID -> R.string.result_skipped_invalid
        CopyStatus.FAILED -> R.string.result_failed
        CopyStatus.CANCELLED -> R.string.result_cancelled
    },
)

private val palette = listOf(
    0xFF1E88E5, 0xFF43A047, 0xFFF4511E, 0xFF8E24AA, 0xFFF9A825, 0xFF00897B, 0xFFD81B60, 0xFF6D4C41, 0xFF546E7A,
).map { Color(it) }

/** The colour of a tag: its own, or one picked by its position. */
fun tagColor(tag: Tag): Color {
    if (tag.color.isNotBlank()) {
        runCatching { return Color(0xFF000000 or Tags.parseColor(tag.color).toLong()) }
    }
    return palette[Math.floorMod(tag.id, palette.size)]
}

/** A back arrow with a description for screen readers. */
@Composable
fun BackButton(onClick: () -> Unit) {
    IconButton(onClick) { Icon(Icons.AutoMirrored.Filled.ArrowBack, stringResource(R.string.back)) }
}

/** A top bar without insets: the activity already insets the content. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun Bar(
    title: @Composable () -> Unit,
    navigation: @Composable () -> Unit = {},
    actions: @Composable androidx.compose.foundation.layout.RowScope.() -> Unit = {},
) {
    TopAppBar(title = title, navigationIcon = navigation, actions = actions, windowInsets = WindowInsets(0))
}

@Composable
fun TextInputDialog(
    title: String,
    label: String,
    initial: String = "",
    confirm: String = stringResource(R.string.ok),
    allowEmpty: Boolean = false,
    onDismiss: () -> Unit,
    onConfirm: (String) -> Unit,
) {
    var text by remember { mutableStateOf(initial) }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(title) },
        text = { OutlinedTextField(text, { text = it }, label = { Text(label) }, singleLine = true) },
        confirmButton = {
            TextButton(onClick = { onConfirm(text.trim()) }, enabled = allowEmpty || text.isNotBlank(), modifier = Modifier.heightIn(min = 48.dp)) { Text(confirm) }
        },
        dismissButton = { TextButton(onDismiss, Modifier.heightIn(min = 48.dp)) { Text(stringResource(R.string.cancel)) } },
    )
}

@Composable
fun ConfirmDialog(title: String, text: String, confirm: String, onDismiss: () -> Unit, onConfirm: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(title) },
        text = { Text(text) },
        confirmButton = { TextButton(onConfirm, Modifier.heightIn(min = 48.dp)) { Text(confirm) } },
        dismissButton = { TextButton(onDismiss, Modifier.heightIn(min = 48.dp)) { Text(stringResource(R.string.cancel)) } },
    )
}
