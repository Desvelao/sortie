package app.sortie.ui

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.selection.toggleable
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.KeyboardArrowDown
import androidx.compose.material.icons.filled.KeyboardArrowUp
import androidx.compose.material3.Card
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.RadioButton
import androidx.compose.material3.ScrollableTabRow
import androidx.compose.material3.Switch
import androidx.compose.material3.Tab
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import app.sortie.R
import app.sortie.core.Profile
import app.sortie.core.ProfileTag
import app.sortie.core.ProfileYaml
import app.sortie.core.SourceKind
import app.sortie.core.VarDefinition
import app.sortie.core.VarSource
import app.sortie.core.VarValidation
import app.sortie.data.Storage
import app.sortie.media.Metadata
import kotlinx.coroutines.launch

private const val GENERAL = 0
private const val TAGS = 1
private const val VARIABLES = 2
private const val YAML = 3

/**
 * Edits one profile in forms or as raw YAML. The text is kept as long as the forms are untouched
 * (so comments survive); editing in the forms writes the profile back with [ProfileYaml.marshal].
 */
@Composable
fun ProfileEditor(vm: MainViewModel, ui: UiState) {
    val id = ui.editingId
    val scope = rememberCoroutineScope()
    var profile by remember(id) { mutableStateOf(ui.editingProfile) }
    var text by remember(id) { mutableStateOf(ui.editingText) }
    var tab by remember(id) { mutableIntStateOf(if (ui.editingProfile == null) YAML else GENERAL) }
    var textIsCurrent by remember(id) { mutableStateOf(true) } // text matches profile
    var profileIsCurrent by remember(id) { mutableStateOf(true) } // profile matches text
    var dirty by remember(id) { mutableStateOf(false) }
    var error by remember(id) { mutableStateOf<String?>(null) }
    var leaving by remember(id) { mutableStateOf(false) }

    fun leave() = vm.open(Screen.PROFILES)
    BackHandler { if (dirty) leaving = true else leave() }

    fun change(p: Profile) {
        profile = p
        textIsCurrent = false
        dirty = true
    }

    fun select(next: Int) {
        error = null
        if (next == YAML && tab != YAML) {
            if (!textIsCurrent) {
                text = ProfileYaml.marshal(profile!!)
                textIsCurrent = true
            }
        } else if (next != YAML && tab == YAML && !profileIsCurrent) {
            try {
                profile = ProfileYaml.parse(text)
                profileIsCurrent = true
            } catch (e: Exception) {
                error = e.message ?: e.toString()
                return
            }
        } else if (next != YAML && profile == null) {
            // the file never parsed: the forms need a valid profile first
            try {
                profile = ProfileYaml.parse(text)
                profileIsCurrent = true
            } catch (e: Exception) {
                error = e.message ?: e.toString()
                return
            }
        }
        tab = next
    }

    fun save() {
        scope.launch {
            error = if (tab == YAML || textIsCurrent) vm.saveProfile(id, null, text) else vm.saveProfile(id, profile, null)
        }
    }

    Column(Modifier.fillMaxSize()) {
        Bar(
            title = { Text(stringResource(R.string.edit_profile)) },
            navigation = { BackButton { if (dirty) leaving = true else leave() } },
            actions = { TextButton(::save, Modifier.heightIn(min = 48.dp)) { Text(stringResource(R.string.save)) } },
        )
        ScrollableTabRow(selectedTabIndex = tab, edgePadding = 0.dp) {
            for ((i, label) in listOf(R.string.tab_general, R.string.tab_tags, R.string.tab_variables, R.string.tab_yaml).withIndex()) {
                Tab(selected = tab == i, onClick = { select(i) }, text = { Text(stringResource(label)) }, modifier = Modifier.heightIn(min = 48.dp))
            }
        }
        error?.let { Text(it, Modifier.fillMaxWidth().padding(12.dp), color = MaterialTheme.colorScheme.error) }
        Column(Modifier.weight(1f).verticalScroll(rememberScrollState()).padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            val p = profile
            when {
                tab == YAML -> OutlinedTextField(
                    text,
                    {
                        text = it
                        profileIsCurrent = false
                        textIsCurrent = true
                        dirty = true
                    },
                    Modifier.fillMaxWidth(),
                    textStyle = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace),
                    label = { Text(stringResource(R.string.tab_yaml)) },
                    minLines = 12,
                )
                p == null -> Unit
                tab == GENERAL -> General(p, ::change)
                tab == TAGS -> TagsForm(p, ::change)
                else -> VariablesForm(p, ::change)
            }
        }
    }

    if (leaving) {
        ConfirmDialog(
            stringResource(R.string.discard_title),
            stringResource(R.string.discard_text),
            stringResource(R.string.discard),
            onDismiss = { leaving = false },
            onConfirm = { leaving = false; leave() },
        )
    }
}

// ---- general ----

@Composable
private fun General(p: Profile, change: (Profile) -> Unit) {
    var pickInput by remember { mutableStateOf(false) }
    var pickDest by remember { mutableStateOf(false) }
    Field(stringResource(R.string.profile_name), p.name) { change(p.copy(name = it)) }
    Field(stringResource(R.string.profile_description), p.description) { change(p.copy(description = it)) }

    Field(stringResource(R.string.input_directory), p.inputDirectory) { change(p.copy(inputDirectory = it)) }
    OutlinedButton({ pickInput = true }, Modifier.heightIn(min = 48.dp)) { Text(stringResource(R.string.browse)) }
    SwitchField(stringResource(R.string.recursive), p.recursive) { change(p.copy(recursive = it)) }

    Field(stringResource(R.string.destination_root), p.destinationRoot) { change(p.copy(destinationRoot = it)) }
    OutlinedButton({ pickDest = true }, Modifier.heightIn(min = 48.dp)) { Text(stringResource(R.string.browse)) }
    Field(stringResource(R.string.destination_template), p.destinationTemplate, hint = stringResource(R.string.template_hint)) { change(p.copy(destinationTemplate = it)) }

    Text(stringResource(R.string.on_conflict), style = MaterialTheme.typography.titleSmall)
    for ((value, label) in listOf(
        Profile.CONFLICT_SKIP to R.string.policy_skip,
        Profile.CONFLICT_RENAME to R.string.policy_rename,
        Profile.CONFLICT_REPLACE to R.string.policy_replace,
    )) {
        val chosen = (p.conflictPolicy.ifEmpty { Profile.CONFLICT_SKIP }) == value
        Row(
            Modifier.fillMaxWidth().heightIn(min = 48.dp).selectable(chosen, role = Role.RadioButton, onClick = { change(p.copy(conflictPolicy = value)) }),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            RadioButton(chosen, onClick = null)
            Text(stringResource(label), Modifier.padding(start = 8.dp))
        }
    }

    if (pickInput) {
        FolderPicker(stringResource(R.string.input_directory), expand(p.inputDirectory), false, { pickInput = false }) {
            pickInput = false
            change(p.copy(inputDirectory = Storage.portable(it)))
        }
    }
    if (pickDest) {
        FolderPicker(stringResource(R.string.destination_root), expand(p.destinationRoot), true, { pickDest = false }) {
            pickDest = false
            change(p.copy(destinationRoot = Storage.portable(it)))
        }
    }
}

private fun expand(path: String): String = when {
    path == "~" -> Storage.home
    path.startsWith("~/") -> Storage.home + path.substring(1)
    else -> path
}

// ---- tags ----

@Composable
private fun TagsForm(p: Profile, change: (Profile) -> Unit) {
    val tags = p.tags
    fun set(list: List<ProfileTag>) = change(p.copy(tags = list))
    for ((i, t) in tags.withIndex()) {
        Card(Modifier.fillMaxWidth()) {
            Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Field(stringResource(R.string.tag_name), t.name) { set(tags.replaced(i, t.copy(name = it))) }
                Field(stringResource(R.string.tag_shortcut), t.shortcut) { set(tags.replaced(i, t.copy(shortcut = it))) }
                Field(stringResource(R.string.tag_destination), t.destination) { set(tags.replaced(i, t.copy(destination = it))) }
                Field(stringResource(R.string.tag_template), t.destinationTemplate) { set(tags.replaced(i, t.copy(destinationTemplate = it))) }
                Field(stringResource(R.string.tag_color), t.color, hint = "#RRGGBB") { set(tags.replaced(i, t.copy(color = it))) }
                SwitchField(stringResource(R.string.tag_enabled), t.enabled) { set(tags.replaced(i, t.copy(enabled = it))) }
                ItemActions(i, tags.size, { set(tags.moved(i, it)) }, { set(tags.removedAt(i)) }, t.name)
            }
        }
    }
    OutlinedButton({ set(tags + ProfileTag(name = "Tag ${tags.size + 1}")) }, Modifier.heightIn(min = 48.dp)) { Text(stringResource(R.string.add_tag)) }
}

// ---- variables ----

@Composable
private fun VariablesForm(p: Profile, change: (Profile) -> Unit) {
    val vars = p.variables
    fun set(list: List<VarDefinition>) = change(p.copy(variables = list))
    for ((i, v) in vars.withIndex()) {
        Card(Modifier.fillMaxWidth()) {
            Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Field(stringResource(R.string.var_name), v.name) { set(vars.replaced(i, v.copy(name = it))) }
                Field(stringResource(R.string.var_fallback), v.fallback) { set(vars.replaced(i, v.copy(fallback = it))) }
                Field(stringResource(R.string.var_regex), v.validate.regex) { set(vars.replaced(i, v.copy(validate = v.validate.copy(regex = it)))) }
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    Field(stringResource(R.string.var_min), v.validate.min?.toString() ?: "", Modifier.weight(1f), number = true) {
                        set(vars.replaced(i, v.copy(validate = v.validate.copy(min = it.toIntOrNull()))))
                    }
                    Field(stringResource(R.string.var_max), v.validate.max?.toString() ?: "", Modifier.weight(1f), number = true) {
                        set(vars.replaced(i, v.copy(validate = v.validate.copy(max = it.toIntOrNull()))))
                    }
                }
                Text(stringResource(R.string.var_sources), style = MaterialTheme.typography.titleSmall)
                for ((j, s) in v.sources.withIndex()) {
                    SourceForm(s, j, v.sources.size, onChange = { ns -> set(vars.replaced(i, v.copy(sources = v.sources.replaced(j, ns)))) },
                        onMove = { d -> set(vars.replaced(i, v.copy(sources = v.sources.moved(j, d)))) },
                        onRemove = { set(vars.replaced(i, v.copy(sources = v.sources.removedAt(j)))) })
                }
                OutlinedButton({ set(vars.replaced(i, v.copy(sources = v.sources + VarSource(SourceKind.FILENAME.id)))) }, Modifier.heightIn(min = 48.dp)) {
                    Text(stringResource(R.string.add_source))
                }
                ItemActions(i, vars.size, { set(vars.moved(i, it)) }, { set(vars.removedAt(i)) }, v.name)
            }
        }
    }
    OutlinedButton({ set(vars + VarDefinition(name = "var${vars.size + 1}", sources = listOf(VarSource(SourceKind.FILENAME.id)))) }, Modifier.heightIn(min = 48.dp)) {
        Text(stringResource(R.string.add_variable))
    }
}

@Composable
private fun SourceForm(s: VarSource, index: Int, size: Int, onChange: (VarSource) -> Unit, onMove: (Int) -> Unit, onRemove: () -> Unit) {
    Card(Modifier.fillMaxWidth()) {
        Column(Modifier.padding(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Choice(stringResource(R.string.source_kind), s.kind, SourceKind.entries.map { it.id to it.id }) { onChange(s.copy(kind = it)) }
            when (s.kind) {
                "filename", "path" -> {
                    Field(stringResource(R.string.source_pattern), s.pattern) { onChange(s.copy(pattern = it)) }
                    Field(stringResource(R.string.source_group), s.group.toString(), number = true) { onChange(s.copy(group = it.toIntOrNull() ?: 0)) }
                }
                "metadata" -> {
                    Choice(stringResource(R.string.source_field), s.field, Metadata.FIELD_NAMES.map { it to it }) { onChange(s.copy(field = it)) }
                    Field(stringResource(R.string.source_format), s.format, hint = "2006-01") { onChange(s.copy(format = it)) }
                }
                "mtime" -> Field(stringResource(R.string.source_format), s.format, hint = "2006-01") { onChange(s.copy(format = it)) }
                "constant" -> Field(stringResource(R.string.source_value), s.value) { onChange(s.copy(value = it)) }
            }
            ItemActions(index, size, onMove, onRemove, s.kind)
        }
    }
}

// ---- small pieces ----

@Composable
private fun ItemActions(index: Int, size: Int, onMove: (Int) -> Unit, onRemove: () -> Unit, name: String) {
    Row(horizontalArrangement = Arrangement.spacedBy(4.dp)) {
        IconButton({ onMove(-1) }, enabled = index > 0) { Icon(Icons.Filled.KeyboardArrowUp, stringResource(R.string.move_up, name)) }
        IconButton({ onMove(1) }, enabled = index < size - 1) { Icon(Icons.Filled.KeyboardArrowDown, stringResource(R.string.move_down, name)) }
        IconButton(onRemove) { Icon(Icons.Filled.Delete, stringResource(R.string.remove_item, name)) }
    }
}

@Composable
private fun Field(label: String, value: String, modifier: Modifier = Modifier, hint: String? = null, number: Boolean = false, onChange: (String) -> Unit) {
    OutlinedTextField(
        value, onChange, modifier.then(if (modifier == Modifier) Modifier.fillMaxWidth() else Modifier),
        label = { Text(label) },
        placeholder = hint?.let { { Text(it) } },
        singleLine = true,
        keyboardOptions = if (number) KeyboardOptions(keyboardType = KeyboardType.Number) else KeyboardOptions.Default,
    )
}

@Composable
private fun SwitchField(label: String, checked: Boolean, onChange: (Boolean) -> Unit) {
    Row(
        Modifier.fillMaxWidth().heightIn(min = 48.dp).toggleable(checked, role = Role.Switch, onValueChange = onChange),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(label, Modifier.weight(1f))
        Switch(checked, null)
    }
}

@Composable
private fun Choice(label: String, value: String, options: List<Pair<String, String>>, onChange: (String) -> Unit) {
    var open by remember { mutableStateOf(false) }
    Box {
        OutlinedButton({ open = true }, Modifier.fillMaxWidth().heightIn(min = 48.dp)) { Text("$label: ${value.ifEmpty { "—" }}") }
        DropdownMenu(open, { open = false }) {
            for ((v, text) in options) DropdownMenuItem(text = { Text(text) }, onClick = { open = false; onChange(v) })
        }
    }
}

private fun <T> List<T>.replaced(i: Int, v: T): List<T> = toMutableList().also { it[i] = v }
private fun <T> List<T>.removedAt(i: Int): List<T> = toMutableList().also { it.removeAt(i) }
private fun <T> List<T>.moved(i: Int, d: Int): List<T> = toMutableList().also {
    val j = (i + d).coerceIn(0, it.size - 1)
    it.add(j, it.removeAt(i))
}
