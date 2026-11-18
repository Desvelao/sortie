package app.sortie.core

import java.io.File

/** A tag as written in a profile file. The order of the list is the order in the app. */
data class ProfileTag(
    val name: String,
    val shortcut: String = "",
    val destination: String = "", // the folder {tag} expands to; defaults to name
    val destinationTemplate: String = "", // overrides the profile's template for this tag
    val enabled: Boolean = true,
    val color: String = "",
)

/**
 * Everything needed to apply the sorter to one use case: input and destination, tags, variable
 * extraction rules and destination templates. Stored as a YAML file that the desktop Sortie
 * reads too (see [ProfileYaml]).
 */
data class Profile(
    val version: Int = CURRENT_VERSION,
    val name: String,
    val description: String = "",
    val inputDirectory: String = "",
    val recursive: Boolean = false,
    val destinationRoot: String = "",
    val destinationTemplate: String = "",
    val conflictPolicy: String = "",
    val variables: List<VarDefinition> = emptyList(),
    val tags: List<ProfileTag> = emptyList(),
) {
    fun varSet(): VarSet = VarSet(variables)

    /** The template that applies to a tag. */
    fun templateFor(t: ProfileTag): String =
        t.destinationTemplate.trim().ifEmpty { destinationTemplate.trim().ifEmpty { PathTemplate.DEFAULT } }

    /** Converts to the tag model used by the app. [ids] maps tag name → database id. */
    fun toTags(ids: Map<String, Int> = emptyMap()): List<Tag> = tags.mapIndexed { i, t ->
        val name = t.name.trim()
        val sc = try {
            Tags.normalizeShortcut(t.shortcut)
        } catch (_: SorterException) {
            t.shortcut.trim().uppercase() // validate() reports it
        }
        Tag(
            id = ids[name] ?: 0, name = name, shortcut = sc, destination = t.destination.trim().ifEmpty { name },
            destinationTemplate = t.destinationTemplate.trim(), enabled = t.enabled, sortOrder = i, color = t.color.trim(),
        )
    }

    /** Checks the whole profile; throws [ProfileException] naming the offending field. */
    fun validate() {
        if (version < 1) throw ProfileException("version", "must be $CURRENT_VERSION")
        if (version > CURRENT_VERSION) {
            throw ProfileException("version", "profile format version $version is newer than this program supports ($CURRENT_VERSION)")
        }
        if (name.isBlank()) throw ProfileException("name", "is empty")
        if (inputDirectory.contains('\u0000')) throw ProfileException("input_directory", "contains an invalid character")
        if (destinationRoot.contains('\u0000')) throw ProfileException("destination_root", "contains an invalid character")
        if (conflictPolicy !in listOf("", CONFLICT_SKIP, CONFLICT_RENAME, CONFLICT_REPLACE)) {
            throw ProfileException("conflict_policy", "\"$conflictPolicy\" is not one of skip, rename, replace")
        }
        val names = try {
            VarSet(variables).names
        } catch (e: SorterException) {
            // point at the offending definition
            val i = variables.indexOfFirst { runCatching { VarSet(listOf(it)) }.isFailure }
            throw ProfileException(if (i >= 0) "variables[$i]" else "variables", e.message ?: "invalid")
        }
        destinationTemplate.trim().takeIf { it.isNotEmpty() }?.let { t ->
            PathTemplate.error(t, names)?.let { throw ProfileException("destination_template", it) }
        }
        if (tags.isEmpty()) throw ProfileException("tags", "at least one tag is required")
        tags.forEachIndexed { i, t ->
            t.destinationTemplate.trim().takeIf { it.isNotEmpty() }?.let { tpl ->
                PathTemplate.error(tpl, names)?.let {
                    throw ProfileException("tags[$i]", "tag \"${t.name}\" destination_template: $it")
                }
            }
        }
        // names, shortcuts, destination folders and colors: the tag rules
        val ts = toTags()
        try {
            Tags.validate(ts)
        } catch (e: SorterException) {
            val i = ts.indexOfFirst { runCatching { Tags.validate(listOf(it)) }.isFailure }
            throw ProfileException(if (i >= 0) "tags[$i]" else "tags", e.message ?: "invalid")
        }
        tags.forEachIndexed { i, t ->
            PathTemplate.error(templateFor(t), names)?.let { throw ProfileException("tags[$i]", it) }
        }
    }

    /**
     * A copy with "~" and relative paths in input_directory and destination_root expanded:
     * "~" to [home] (on Android the shared storage root), relative paths against [baseDir].
     */
    fun resolved(home: String, baseDir: String = home): Profile = copy(
        inputDirectory = expandPath(inputDirectory, home, baseDir),
        destinationRoot = expandPath(destinationRoot, home, baseDir),
    )

    companion object {
        /** The profile file format version written by this program. */
        const val CURRENT_VERSION = 1
        const val CONFLICT_SKIP = "skip"
        const val CONFLICT_RENAME = "rename"
        const val CONFLICT_REPLACE = "replace"

        /** A starter profile that sorts by year and tag. */
        fun example(name: String): Profile = Profile(
            name = name,
            recursive = true,
            destinationTemplate = "{year}/{tag}/{filename}",
            conflictPolicy = CONFLICT_SKIP,
            variables = listOf(
                VarDefinition(
                    name = "year",
                    sources = listOf(
                        VarSource(kind = "manual"),
                        VarSource(kind = "filename", pattern = """(^|[^0-9])(20[0-9]{2})([^0-9]|$)""", group = 2),
                        VarSource(kind = "metadata", field = "year"),
                    ),
                    validate = VarValidation(min = 1970, max = 2100),
                ),
            ),
            tags = Tags.DEFAULT_NAMES.mapIndexed { i, n -> ProfileTag(name = n, shortcut = "${i + 1}") },
        )

        /** App tags back to their file form: the destination is omitted when it equals the name. */
        fun fromTags(ts: List<Tag>): List<ProfileTag> = ts.sortedBy { it.sortOrder }.map {
            ProfileTag(
                name = it.name.trim(), shortcut = it.shortcut.trim().uppercase(),
                destination = it.destination.trim().takeIf { d -> d != it.name.trim() } ?: "",
                destinationTemplate = it.destinationTemplate.trim(), enabled = it.enabled, color = it.color.trim(),
            )
        }

        fun expandPath(raw: String, home: String, baseDir: String): String {
            var s = raw.trim()
            if (s.isEmpty()) return ""
            if (s == "~" || s.startsWith("~/")) s = home.trimEnd('/') + s.removePrefix("~")
            val f = File(s)
            val abs = if (f.isAbsolute || baseDir.isEmpty()) f else File(baseDir, s)
            return abs.toPath().normalize().toString()
        }
    }
}

/** Locates a problem inside a profile. [path] is like `tags[2]` or `destination_template`. */
class ProfileException(val path: String, val problem: String, val line: Int = 0) :
    SorterException(if (line > 0) "line $line: $path: $problem" else "$path: $problem") {
    fun atLine(l: Int) = ProfileException(path, problem, l)
}
