package app.sortie.ui

import android.Manifest
import android.content.pm.PackageManager
import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInHorizontally
import androidx.compose.animation.slideOutHorizontally
import androidx.compose.animation.togetherWith
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.focusable
import androidx.compose.foundation.gestures.detectHorizontalDragGestures
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.horizontalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material3.AssistChip
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.Spacer
import androidx.compose.ui.draw.clip
import androidx.compose.material3.FilterChipDefaults
import androidx.compose.material3.AssistChipDefaults
import androidx.compose.material.icons.filled.Warning
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEvent
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.isAltPressed
import androidx.compose.ui.input.key.isCtrlPressed
import androidx.compose.ui.input.key.isMetaPressed
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.nativeKeyCode
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import app.sortie.App
import app.sortie.R
import app.sortie.copy.CopyState
import app.sortie.core.FileEntry
import app.sortie.core.Filter
import app.sortie.core.Tag
import app.sortie.core.VarInfo
import app.sortie.core.VarStatus
import app.sortie.data.Storage
import app.sortie.ui.previews.Preview
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

@Composable
fun SorterScreen(vm: MainViewModel, ui: UiState) {
    val context = LocalContext.current
    val copyState by vm.copy.state.collectAsStateWithLifecycle()
    var sheet by remember { mutableStateOf(false) }
    var picker by remember { mutableStateOf(false) }
    var history by remember { mutableStateOf(false) }
    var manual by remember { mutableStateOf<VarInfo?>(null) }
    var pendingAll by remember { mutableStateOf<Boolean?>(null) }
    val prefs = remember { (context.applicationContext as App).container.prefs }

    fun plan(all: Boolean) {
        sheet = true
        vm.copy.plan(all)
    }

    val notifications = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) {
        pendingAll?.let { plan(it) }
        pendingAll = null
    }

    /** Asks for the notification permission once (Android 13+) so the progress shows while away, then plans. */
    fun requestCopy(all: Boolean) {
        if (!all && ui.currentDestinationBlocked) return // the destination bar says why
        val needs = Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU && !prefs.askedNotifications &&
            ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        prefs.askedNotifications = true
        if (needs) {
            pendingAll = all
            notifications.launch(Manifest.permission.POST_NOTIFICATIONS)
        } else {
            plan(all)
        }
    }

    val focus = remember { FocusRequester() }
    LaunchedEffect(Unit) { runCatching { focus.requestFocus() } }

    Column(
        Modifier.fillMaxSize().focusRequester(focus).focusable().onPreviewKeyEvent { handleKey(it, vm) { requestCopy(false) } },
    ) {
        SorterTopBar(
            vm, ui,
            onCopy = ::requestCopy,
            onChooseFolder = { picker = true },
            onHistory = { history = true },
        )
        Banner(vm, ui)
        if (copyState is CopyState.Running && !sheet) {
            val s = copyState as CopyState.Running
            Row(
                Modifier.fillMaxWidth().heightIn(min = 48.dp).clickable { sheet = true }.padding(horizontal = 16.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(12.dp),
            ) {
                CircularProgressIndicator(Modifier.size(20.dp), strokeWidth = 2.dp)
                Text(stringResource(R.string.copying) + " " + stringResource(R.string.copy_progress, s.done, s.total))
            }
        }
        Box(
            Modifier.weight(1f).fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp)
                .clip(MaterialTheme.shapes.extraLarge).pointerInput(Unit) {
                var total = 0f
                detectHorizontalDragGestures(
                    onDragStart = { total = 0f },
                    onDragEnd = {
                        if (total < -SWIPE_PX) vm.next() else if (total > SWIPE_PX) vm.prev()
                    },
                ) { _, d -> total += d }
            },
        ) {
            val file = ui.current
            when {
                ui.inputDir.isEmpty() -> Empty(stringResource(R.string.no_folder), stringResource(R.string.choose_folder)) { picker = true }
                ui.scanning && ui.total == 0 -> CircularProgressIndicator(Modifier.align(Alignment.Center))
                ui.total == 0 -> Empty(stringResource(R.string.folder_empty), stringResource(R.string.rescan)) { vm.rescan() }
                file == null -> Empty(stringResource(R.string.no_match), stringResource(R.string.show_all)) { vm.setFilter(Filter.ALL) }
                else -> {
                    val dir = ui.direction
                    AnimatedContent(
                        targetState = file,
                        contentKey = { it.path },
                        transitionSpec = {
                            (slideInHorizontally { it * dir } + fadeIn()) togetherWith (slideOutHorizontally { -it * dir } + fadeOut())
                        },
                        label = "file",
                    ) { f -> Preview(f, vm::metadata) }
                }
            }
        }
        if (ui.current != null) {
            VariablesRow(ui.vars) { manual = it }
            if (ui.showFilmstrip && ui.strip.isNotEmpty()) Filmstrip(vm, ui)
            TagPanel(vm, ui)
            CopyBar(ui, enabled = copyState !is CopyState.Running && !ui.currentDestinationBlocked) { requestCopy(false) }
        }
    }

    if (sheet && copyState !is CopyState.Idle) {
        CopySheet(vm.copy, copyState, ui.conflictPolicy) { sheet = false }
    }
    if (picker) {
        FolderPicker(
            title = stringResource(R.string.choose_folder),
            start = ui.inputDir.ifEmpty { Storage.home },
            allowCreate = false,
            onDismiss = { picker = false },
            onPick = {
                picker = false
                vm.setInputDir(it)
            },
        )
    }
    if (history) ui.current?.let { HistoryDialog(vm, it) { history = false } }
    manual?.let { info ->
        TextInputDialog(
            title = stringResource(R.string.manual_value_title, info.name),
            label = stringResource(R.string.manual_value_label),
            initial = if (info.manual) info.value.value else "",
            allowEmpty = true,
            onDismiss = { manual = null },
            onConfirm = {
                vm.setManual(info.name, it)
                manual = null
            },
        )
    }
}

private const val SWIPE_PX = 120f
private val SHORTCUT = Regex("[A-Z0-9]|F([1-9]|1[0-2])")

/** Hardware keys: arrows and Home/End move, tag shortcuts toggle, Ctrl+Enter copies the current file. */
private fun handleKey(e: KeyEvent, vm: MainViewModel, copyCurrent: () -> Unit): Boolean {
    if (e.type != KeyEventType.KeyDown) return false
    val enter = e.key == Key.Enter || e.key == Key.NumPadEnter
    return when {
        e.key == Key.DirectionRight -> true.also { vm.next() }
        e.key == Key.DirectionLeft -> true.also { vm.prev() }
        e.key == Key.MoveHome -> true.also { vm.first() }
        e.key == Key.MoveEnd -> true.also { vm.last() }
        enter && e.isCtrlPressed -> true.also { copyCurrent() }
        e.isCtrlPressed || e.isAltPressed || e.isMetaPressed -> false
        else -> {
            val name = android.view.KeyEvent.keyCodeToString(e.key.nativeKeyCode).removePrefix("KEYCODE_")
            if (SHORTCUT.matches(name)) true.also { vm.shortcut(name) } else false
        }
    }
}

@Composable
private fun Empty(text: String, action: String, onAction: () -> Unit) {
    Column(Modifier.fillMaxSize().padding(24.dp), verticalArrangement = Arrangement.spacedBy(16.dp, Alignment.CenterVertically), horizontalAlignment = Alignment.CenterHorizontally) {
        Text(text, style = MaterialTheme.typography.bodyLarge)
        Button(onAction, Modifier.heightIn(min = 48.dp)) { Text(action) }
    }
}

@Composable
private fun Banner(vm: MainViewModel, ui: UiState) {
    if (ui.interrupted == 0 && ui.leftovers.isEmpty()) return
    Surface(color = MaterialTheme.colorScheme.secondaryContainer, modifier = Modifier.fillMaxWidth()) {
        Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            if (ui.interrupted > 0) Text(pluralStringResource(R.plurals.interrupted_banner, ui.interrupted, ui.interrupted))
            if (ui.leftovers.isNotEmpty()) Text(pluralStringResource(R.plurals.leftovers_banner, ui.leftovers.size, ui.leftovers.size))
            Row {
                if (ui.leftovers.isNotEmpty()) {
                    TextButton(vm::removeLeftovers, Modifier.heightIn(min = 48.dp)) { Text(stringResource(R.string.remove_temp_files)) }
                }
                TextButton(vm::dismissInterrupted, Modifier.heightIn(min = 48.dp)) { Text(stringResource(R.string.dismiss)) }
            }
        }
    }
}

@Composable
private fun SorterTopBar(vm: MainViewModel, ui: UiState, onCopy: (Boolean) -> Unit, onChooseFolder: () -> Unit, onHistory: () -> Unit) {
    var profileMenu by remember { mutableStateOf(false) }
    var filterMenu by remember { mutableStateOf(false) }
    var copyMenu by remember { mutableStateOf(false) }
    var overflow by remember { mutableStateOf(false) }
    Bar(
        title = {
            Column {
                Box {
                    Text(
                        ui.profileName,
                        Modifier.clickable { profileMenu = true }.heightIn(min = 24.dp),
                        style = MaterialTheme.typography.titleMedium,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                    DropdownMenu(profileMenu, { profileMenu = false }) {
                        for (p in ui.profiles.filter { it.error == null }) {
                            DropdownMenuItem(
                                text = { Text(if (p.id == ui.profileId) "✓ ${p.name}" else p.name) },
                                onClick = {
                                    profileMenu = false
                                    if (p.id != ui.profileId) vm.selectProfile(p.id)
                                },
                            )
                        }
                        DropdownMenuItem(text = { Text(stringResource(R.string.manage_profiles)) }, onClick = { profileMenu = false; vm.open(Screen.PROFILES) })
                    }
                }
                val progress = ui.statusProgress
                val sub = when {
                    progress != null -> stringResource(R.string.checking_statuses, progress.first, progress.second)
                    ui.count == 0 -> stringResource(R.string.no_files)
                    else -> stringResource(R.string.position_summary, ui.position, ui.count, ui.tagged)
                }
                Text(sub, style = MaterialTheme.typography.bodySmall, maxLines = 1)
            }
        },
        actions = {
            Box {
                IconButton({ filterMenu = true }) { Icon(painterResource(R.drawable.ic_filter), stringResource(R.string.filter_menu)) }
                DropdownMenu(filterMenu, { filterMenu = false }) {
                    for (f in Filter.entries) {
                        DropdownMenuItem(
                            text = { Text(if (f == ui.filter) "✓ ${filterLabel(f)}" else filterLabel(f)) },
                            modifier = Modifier.semantics { selected = f == ui.filter },
                            onClick = { filterMenu = false; vm.setFilter(f) },
                        )
                    }
                }
            }
            Box {
                IconButton({ copyMenu = true }, enabled = ui.total > 0) { Icon(painterResource(R.drawable.ic_copy), stringResource(R.string.copy_menu)) }
                DropdownMenu(copyMenu, { copyMenu = false }) {
                    DropdownMenuItem(text = { Text(stringResource(R.string.copy_current)) }, enabled = ui.current != null && !ui.currentDestinationBlocked, onClick = { copyMenu = false; onCopy(false) })
                    DropdownMenuItem(text = { Text(stringResource(R.string.copy_all)) }, onClick = { copyMenu = false; onCopy(true) })
                }
            }
            Box {
                IconButton({ overflow = true }) { Icon(Icons.Filled.MoreVert, stringResource(R.string.more)) }
                DropdownMenu(overflow, { overflow = false }) {
                    DropdownMenuItem(text = { Text(stringResource(R.string.choose_folder)) }, onClick = { overflow = false; onChooseFolder() })
                    DropdownMenuItem(text = { Text(stringResource(R.string.rescan)) }, onClick = { overflow = false; vm.rescan() })
                    DropdownMenuItem(text = { Text(stringResource(R.string.first_file)) }, enabled = ui.count > 0, onClick = { overflow = false; vm.first() })
                    DropdownMenuItem(text = { Text(stringResource(R.string.last_file)) }, enabled = ui.count > 0, onClick = { overflow = false; vm.last() })
                    DropdownMenuItem(text = { Text(stringResource(R.string.history)) }, enabled = ui.current != null, onClick = { overflow = false; onHistory() })
                    DropdownMenuItem(text = { Text(stringResource(R.string.profiles)) }, onClick = { overflow = false; vm.open(Screen.PROFILES) })
                    DropdownMenuItem(text = { Text(stringResource(R.string.settings)) }, onClick = { overflow = false; vm.open(Screen.SETTINGS) })
                }
            }
        },
    )
}

@Composable
fun filterLabel(f: Filter): String = stringResource(
    when (f) {
        Filter.ALL -> R.string.filter_all
        Filter.UNTAGGED -> R.string.filter_untagged
        Filter.TAGGED -> R.string.filter_tagged
        Filter.UNRESOLVED -> R.string.filter_unresolved
        Filter.READY -> R.string.filter_ready
        Filter.ALREADY_COPIED -> R.string.filter_already_copied
        Filter.CONFLICTS -> R.string.filter_conflicts
        Filter.ERRORS -> R.string.filter_errors
    },
)

@Composable
private fun VariablesRow(vars: List<VarInfo>, onEdit: (VarInfo) -> Unit) {
    if (vars.isEmpty()) return
    Row(Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()).padding(horizontal = 16.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        for (v in vars) {
            val state = when {
                v.manual -> stringResource(R.string.var_manual)
                v.value.status == VarStatus.UNRESOLVED -> stringResource(R.string.var_unresolved)
                v.value.status == VarStatus.INVALID -> stringResource(R.string.var_invalid)
                else -> ""
            }
            val value = v.value.value.ifEmpty { "—" }
            val text = listOf("${v.name}: $value", state).filter { it.isNotEmpty() }.joinToString(" · ")
            val bad = v.value.status != VarStatus.RESOLVED && !v.manual
            AssistChip(
                onClick = { onEdit(v) },
                label = { Text(text, maxLines = 1) },
                leadingIcon = { Icon(if (bad) Icons.Filled.Warning else Icons.Filled.Check, null, Modifier.size(16.dp)) },
                colors = if (bad) {
                    AssistChipDefaults.assistChipColors(
                        containerColor = MaterialTheme.colorScheme.errorContainer,
                        labelColor = MaterialTheme.colorScheme.onErrorContainer,
                        leadingIconContentColor = MaterialTheme.colorScheme.onErrorContainer,
                    )
                } else {
                    AssistChipDefaults.assistChipColors()
                },
            )
        }
    }
}

@Composable
private fun Filmstrip(vm: MainViewModel, ui: UiState) {
    val state = rememberLazyListState()
    LaunchedEffect(ui.strip, ui.stripIndex) { state.animateScrollToItem((ui.stripIndex - 2).coerceAtLeast(0)) }
    LazyRow(state = state, modifier = Modifier.fillMaxWidth().padding(vertical = 4.dp), contentPadding = androidx.compose.foundation.layout.PaddingValues(horizontal = 12.dp), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
        items(ui.strip, key = { it.path }) { f -> Thumb(vm, f, f.path == ui.current?.path) { vm.goTo(f) } }
    }
}

@Composable
private fun Thumb(vm: MainViewModel, f: FileEntry, current: Boolean, onClick: () -> Unit) {
    val bmp by produceState<androidx.compose.ui.graphics.ImageBitmap?>(null, f.path, f.size, f.modTimeMs) {
        value = withContext(Dispatchers.IO) { vm.thumbnails.load(f)?.asImageBitmap() }
    }
    val shape = RoundedCornerShape(6.dp)
    Box(
        Modifier.size(56.dp)
            .border(if (current) BorderStroke(3.dp, MaterialTheme.colorScheme.primary) else BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant), shape)
            .clickable(onClick = onClick)
            .semantics { contentDescription = f.name; selected = current },
        contentAlignment = Alignment.Center,
    ) {
        val b = bmp
        if (b != null) Image(b, null, Modifier.fillMaxSize().padding(3.dp), contentScale = ContentScale.Crop)
        else Icon(painterResource(R.drawable.ic_file), null, Modifier.size(24.dp))
    }
}

@Composable
private fun TagPanel(vm: MainViewModel, ui: UiState) {
    val tags = ui.tags.filter { it.enabled }
    if (tags.isEmpty()) {
        Text(stringResource(R.string.no_tags), Modifier.padding(16.dp))
        return
    }
    Column(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        for (pair in tags.chunked(2)) {
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                for (t in pair) TagChip(t, t.id in ui.assigned, Modifier.weight(1f)) { vm.toggleTag(t.id) }
                if (pair.size == 1) Box(Modifier.weight(1f))
            }
        }
        val assigned = tags.filter { it.id in ui.assigned }
        // One tag: the bottom bar already shows its destination.
        if (assigned.size > 1) for (t in assigned) {
            val check = ui.checks.firstOrNull { it.tagId == t.id }
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                if (check == null) {
                    Text("${t.name}: ${stringResource(R.string.checking)}", style = MaterialTheme.typography.bodySmall)
                } else {
                    Icon(statusIcon(check.status), null, Modifier.size(16.dp))
                    val where = if (check.path.isNotEmpty()) Storage.display(check.path) else check.detail
                    Text("${t.name}: ${statusLabel(check.status)} — $where", style = MaterialTheme.typography.bodySmall, maxLines = 2, overflow = TextOverflow.Ellipsis)
                }
            }
        }
    }
}

/** Bottom bar: where the current file goes and a button to copy it. */
@Composable
private fun CopyBar(ui: UiState, enabled: Boolean, onCopy: () -> Unit) {
    val tags = ui.tags.filter { it.enabled && it.id in ui.assigned }
    val checks = tags.mapNotNull { t -> ui.checks.firstOrNull { it.tagId == t.id } }
    val title: String
    val detail: String
    when {
        tags.isEmpty() -> { title = ""; detail = stringResource(R.string.bar_no_tags) }
        tags.size > 1 -> { title = ""; detail = stringResource(R.string.bar_destinations, tags.size) }
        else -> {
            val c = checks.firstOrNull()
            title = if (c == null) stringResource(R.string.checking) else statusLabel(c.status)
            detail = c?.let { if (it.path.isNotEmpty()) Storage.display(it.path) else it.detail } ?: ""
        }
    }
    Surface(color = MaterialTheme.colorScheme.surfaceContainer, modifier = Modifier.fillMaxWidth()) {
        Row(Modifier.padding(horizontal = 16.dp, vertical = 12.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            Column(Modifier.weight(1f)) {
                if (title.isNotEmpty()) Text(stringResource(R.string.bar_destination, title), style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                Text(detail, style = MaterialTheme.typography.bodyMedium, maxLines = 2, overflow = TextOverflow.Ellipsis)
            }
            Button(onCopy, enabled = enabled && tags.isNotEmpty(), modifier = Modifier.heightIn(min = 48.dp)) {
                Icon(painterResource(R.drawable.ic_copy), null, Modifier.size(18.dp))
                Spacer(Modifier.width(8.dp))
                Text(stringResource(R.string.copy_menu))
            }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun TagChip(tag: Tag, selected: Boolean, modifier: Modifier = Modifier, onClick: () -> Unit) {
    FilterChip(
        selected = selected,
        onClick = onClick,
        modifier = modifier.heightIn(min = 48.dp),
        shape = MaterialTheme.shapes.medium,
        label = {
            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                Text(tag.name, Modifier.weight(1f), maxLines = 1, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.titleSmall)
                if (tag.shortcut.isNotEmpty()) Text(tag.shortcut, style = MaterialTheme.typography.labelSmall)
            }
        },
        leadingIcon = {
            if (selected) Icon(Icons.Filled.Check, null, Modifier.size(18.dp))
            else Box(Modifier.size(12.dp).background(tagColor(tag), CircleShape))
        },
        colors = FilterChipDefaults.filterChipColors(
            selectedContainerColor = MaterialTheme.colorScheme.primary,
            selectedLabelColor = MaterialTheme.colorScheme.onPrimary,
            selectedLeadingIconColor = MaterialTheme.colorScheme.onPrimary,
        ),
        border = BorderStroke(1.dp, if (selected) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.outlineVariant),
    )
}
