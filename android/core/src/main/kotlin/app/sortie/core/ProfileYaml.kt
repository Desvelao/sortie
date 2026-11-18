package app.sortie.core

import org.snakeyaml.engine.v2.api.Dump
import org.snakeyaml.engine.v2.api.DumpSettings
import org.snakeyaml.engine.v2.api.LoadSettings
import org.snakeyaml.engine.v2.api.lowlevel.Compose
import org.snakeyaml.engine.v2.common.FlowStyle
import org.snakeyaml.engine.v2.exceptions.MarkedYamlEngineException
import org.snakeyaml.engine.v2.exceptions.YamlEngineException
import org.snakeyaml.engine.v2.nodes.MappingNode
import org.snakeyaml.engine.v2.nodes.Node
import org.snakeyaml.engine.v2.nodes.ScalarNode
import org.snakeyaml.engine.v2.nodes.SequenceNode
import org.snakeyaml.engine.v2.nodes.Tag as YamlTag

/**
 * Reads and writes profiles in the desktop Sortie's YAML format (version 1). Unknown keys,
 * wrong types and invalid content are errors that carry a line number where one is known.
 */
object ProfileYaml {
    private class Decoder {
        /** path ("tags[2]", "destination_template") → 1-based line, for validation errors. */
        val lines = HashMap<String, Int>()

        fun line(n: Node): Int = n.startMark.map { it.line + 1 }.orElse(0)

        fun fail(n: Node, msg: String): Nothing {
            val l = line(n)
            throw SorterException("invalid profile: " + (if (l > 0) "line $l: " else "") + msg)
        }

        fun isNull(n: Node) = n is ScalarNode && n.tag == YamlTag.NULL

        fun map(n: Node, what: String, known: Set<String>, path: String = ""): Map<String, Node> {
            if (n !is MappingNode) fail(n, "$what must be a mapping (key: value)")
            val out = LinkedHashMap<String, Node>()
            for (t in n.value) {
                val k = t.keyNode
                if (k !is ScalarNode) fail(k, "$what has a key that is not plain text")
                if (k.value !in known) fail(k, "unknown key \"${k.value}\" in $what")
                if (out.put(k.value, t.valueNode) != null) fail(k, "key \"${k.value}\" appears twice in $what")
                if (path.isEmpty()) lines[k.value] = line(k)
            }
            return out
        }

        fun str(n: Node?, what: String): String {
            if (n == null || isNull(n)) return ""
            if (n !is ScalarNode) fail(n, "$what must be text")
            return n.value
        }

        fun int(n: Node?, what: String, default: Int = 0): Int {
            if (n == null || isNull(n)) return default
            if (n !is ScalarNode) fail(n, "$what must be a whole number")
            return n.value.trim().toIntOrNull() ?: fail(n, "$what must be a whole number, not \"${n.value}\"")
        }

        fun bool(n: Node?, what: String, default: Boolean): Boolean {
            if (n == null || isNull(n)) return default
            if (n !is ScalarNode) fail(n, "$what must be true or false")
            return when (n.value.trim().lowercase()) {
                "true", "yes", "on" -> true
                "false", "no", "off" -> false
                else -> fail(n, "$what must be true or false, not \"${n.value}\"")
            }
        }

        fun list(n: Node?, what: String): List<Node> {
            if (n == null || isNull(n)) return emptyList()
            if (n !is SequenceNode) fail(n, "$what must be a list")
            return n.value
        }

        fun profile(root: Node): Profile {
            val m = map(root, "the profile", PROFILE_KEYS)
            val vars = list(m["variables"], "variables").mapIndexed { i, vn ->
                lines["variables[$i]"] = line(vn)
                val what = "variables[$i]"
                val vm = map(vn, what, VAR_KEYS, what)
                val sources = list(vm["sources"], "$what sources").mapIndexed { j, sn ->
                    val sw = "$what sources[$j]"
                    val sm = map(sn, sw, SOURCE_KEYS, sw)
                    VarSource(
                        kind = str(sm["kind"], "$sw kind"), pattern = str(sm["pattern"], "$sw pattern"),
                        group = int(sm["group"], "$sw group"), field = str(sm["field"], "$sw field"),
                        format = str(sm["format"], "$sw format"), value = str(sm["value"], "$sw value"),
                    )
                }
                val validate = vm["validate"]?.takeUnless { isNull(it) }?.let { v ->
                    val w = "$what validate"
                    val x = map(v, w, VALIDATE_KEYS, w)
                    VarValidation(
                        regex = str(x["regex"], "$w regex"),
                        min = x["min"]?.takeUnless { isNull(it) }?.let { int(it, "$w min") },
                        max = x["max"]?.takeUnless { isNull(it) }?.let { int(it, "$w max") },
                    )
                } ?: VarValidation()
                VarDefinition(str(vm["name"], "$what name"), sources, validate, str(vm["fallback"], "$what fallback"))
            }
            val tags = list(m["tags"], "tags").mapIndexed { i, tn ->
                lines["tags[$i]"] = line(tn)
                val what = "tags[$i]"
                val tm = map(tn, what, TAG_KEYS, what)
                ProfileTag(
                    name = str(tm["name"], "$what name"), shortcut = str(tm["shortcut"], "$what shortcut"),
                    destination = str(tm["destination"], "$what destination"),
                    destinationTemplate = str(tm["destination_template"], "$what destination_template"),
                    enabled = bool(tm["enabled"], "$what enabled", true), color = str(tm["color"], "$what color"),
                )
            }
            return Profile(
                version = int(m["version"], "version"), name = str(m["name"], "name"),
                description = str(m["description"], "description"),
                inputDirectory = str(m["input_directory"], "input_directory"),
                recursive = bool(m["recursive"], "recursive", false),
                destinationRoot = str(m["destination_root"], "destination_root"),
                destinationTemplate = str(m["destination_template"], "destination_template"),
                conflictPolicy = str(m["conflict_policy"], "conflict_policy"),
                variables = vars, tags = tags,
            )
        }
    }

    private val PROFILE_KEYS = setOf(
        "version", "name", "description", "input_directory", "recursive", "destination_root",
        "destination_template", "conflict_policy", "variables", "tags",
    )
    private val VAR_KEYS = setOf("name", "sources", "validate", "fallback")
    private val SOURCE_KEYS = setOf("kind", "pattern", "group", "field", "format", "value")
    private val VALIDATE_KEYS = setOf("regex", "min", "max")
    private val TAG_KEYS = setOf("name", "shortcut", "destination", "destination_template", "enabled", "color")

    /** Decodes and validates YAML. Throws [SorterException] ([ProfileException] for invalid content). */
    fun parse(text: String): Profile {
        val root = try {
            Compose(LoadSettings.builder().build()).composeString(text)
        } catch (e: MarkedYamlEngineException) {
            val l = e.problemMark.map { it.line + 1 }.orElse(0)
            throw SorterException("invalid profile: " + (if (l > 0) "line $l: " else "") + (e.problem ?: "not valid YAML"))
        } catch (e: YamlEngineException) {
            throw SorterException("invalid profile: " + (e.message ?: "not valid YAML"))
        }
        val node = root.orElse(null)
        val d = Decoder()
        if (node == null || d.isNull(node)) throw SorterException("the profile file is empty")
        val p = d.profile(node)
        try {
            p.validate()
        } catch (e: ProfileException) {
            throw e.atLine(d.lines[e.path] ?: 0)
        }
        return p
    }

    /** Renders a profile as YAML (2-space indent, same key order as the desktop app). */
    fun marshal(p: Profile): String {
        val m = LinkedHashMap<String, Any>()
        m["version"] = if (p.version == 0) Profile.CURRENT_VERSION else p.version
        m["name"] = p.name
        if (p.description.isNotEmpty()) m["description"] = p.description
        if (p.inputDirectory.isNotEmpty()) m["input_directory"] = p.inputDirectory
        m["recursive"] = p.recursive
        if (p.destinationRoot.isNotEmpty()) m["destination_root"] = p.destinationRoot
        if (p.destinationTemplate.isNotEmpty()) m["destination_template"] = p.destinationTemplate
        if (p.conflictPolicy.isNotEmpty()) m["conflict_policy"] = p.conflictPolicy
        if (p.variables.isNotEmpty()) {
            m["variables"] = p.variables.map { v ->
                val vm = LinkedHashMap<String, Any>()
                vm["name"] = v.name
                vm["sources"] = v.sources.map { s ->
                    val sm = LinkedHashMap<String, Any>()
                    sm["kind"] = s.kind
                    if (s.pattern.isNotEmpty()) sm["pattern"] = s.pattern
                    if (s.group != 0) sm["group"] = s.group
                    if (s.field.isNotEmpty()) sm["field"] = s.field
                    if (s.format.isNotEmpty()) sm["format"] = s.format
                    if (s.value.isNotEmpty()) sm["value"] = s.value
                    sm
                }
                if (!v.validate.isEmpty) {
                    val x = LinkedHashMap<String, Any>()
                    if (v.validate.regex.isNotEmpty()) x["regex"] = v.validate.regex
                    v.validate.min?.let { x["min"] = it }
                    v.validate.max?.let { x["max"] = it }
                    vm["validate"] = x
                }
                if (v.fallback.isNotEmpty()) vm["fallback"] = v.fallback
                vm
            }
        }
        m["tags"] = p.tags.map { t ->
            val tm = LinkedHashMap<String, Any>()
            tm["name"] = t.name
            if (t.shortcut.isNotEmpty()) tm["shortcut"] = t.shortcut
            if (t.destination.isNotEmpty()) tm["destination"] = t.destination
            if (t.destinationTemplate.isNotEmpty()) tm["destination_template"] = t.destinationTemplate
            if (!t.enabled) tm["enabled"] = false
            if (t.color.isNotEmpty()) tm["color"] = t.color
            tm
        }
        val settings = DumpSettings.builder()
            .setDefaultFlowStyle(FlowStyle.BLOCK)
            .setIndent(2)
            .setIndicatorIndent(2)
            .setIndentWithIndicator(true)
            .setWidth(1000)
            .build()
        return Dump(settings).dumpToString(m)
    }

    /**
     * Changes only the top-level `name:` of a profile text. Comments and formatting are kept when
     * the file has a plain one-line `name:` entry; otherwise the profile is re-rendered.
     */
    fun rename(text: String, newName: String): String {
        val name = newName.trim()
        if (name.isEmpty()) throw SorterException("the profile name is empty")
        val lines = text.split("\n")
        val hits = lines.indices.filter { lines[it].startsWith("name:") }
        if (hits.size == 1) {
            val quoted = "\"" + name.replace("\\", "\\\\").replace("\"", "\\\"") + "\""
            val edited = lines.toMutableList().also { it[hits[0]] = "name: $quoted" }.joinToString("\n")
            val ok = runCatching { parse(edited).name == name }.getOrDefault(false)
            if (ok) return edited
        }
        val out = marshal(parse(text).copy(name = name))
        parse(out) // renaming must not produce an invalid profile
        return out
    }
}
