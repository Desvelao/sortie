package app.sortie.core

import java.time.Instant
import java.time.ZoneId
import java.util.regex.Pattern
import java.util.regex.PatternSyntaxException

/** Where a variable's value may come from. [id] is the spelling used in profile files. */
enum class SourceKind(val id: String) {
    MANUAL("manual"), FILENAME("filename"), PATH("path"), METADATA("metadata"), MTIME("mtime"), CONSTANT("constant");

    companion object {
        fun of(id: String): SourceKind? = entries.firstOrNull { it.id == id }
    }
}

enum class VarStatus { RESOLVED, UNRESOLVED, INVALID }

/** One step of a variable's extraction chain. */
data class VarSource(
    val kind: String,
    val pattern: String = "", // filename, path
    val group: Int = 0, // capture group (default 1; 0 = whole match when the pattern has no groups)
    val field: String = "", // metadata
    val format: String = "", // Go date layout
    val value: String = "", // constant
)

data class VarValidation(val regex: String = "", val min: Int? = null, val max: Int? = null) {
    val isEmpty get() = regex.isEmpty() && min == null && max == null
}

data class VarDefinition(
    val name: String,
    val sources: List<VarSource> = emptyList(),
    val validate: VarValidation = VarValidation(),
    val fallback: String = "",
)

/** Everything a source may look at. [fields] is lazy: metadata is only read when needed. */
class FileContext(
    val name: String,
    val relDir: String = "", // directory relative to the input root, "/" separated
    val modTimeMs: Long = 0,
    val manual: Map<String, String> = emptyMap(),
    val fields: (() -> Map<String, String>)? = null,
)

data class VarValue(
    val value: String = "",
    val source: SourceKind? = null,
    val status: VarStatus = VarStatus.UNRESOLVED,
    val detail: String = "",
) {
    val ok get() = status == VarStatus.RESOLVED
}

/** A validated group of variable definitions. Throws [SorterException] when a definition is wrong. */
class VarSet(defs: List<VarDefinition>, private val zone: ZoneId = ZoneId.systemDefault()) {
    private class CompiledSource(val src: VarSource, val kind: SourceKind, val re: Pattern?, val group: Int)
    private class Compiled(val def: VarDefinition, val sources: List<CompiledSource>, val valRe: Pattern?)

    private val vars: List<Compiled>
    val names: List<String>

    init {
        val seen = HashSet<String>()
        vars = defs.map { d ->
            if (!NAME_RE.matches(d.name)) {
                throw SorterException("variable name \"${d.name}\" is invalid (letters, digits, _; must start with a letter)")
            }
            if (d.name in RESERVED) throw SorterException("variable name \"${d.name}\" is reserved")
            if (!seen.add(d.name)) throw SorterException("variable \"${d.name}\" is defined twice")
            val valRe = if (d.validate.regex.isNotEmpty()) {
                try {
                    compile(d.validate.regex)
                } catch (e: SorterException) {
                    throw SorterException("variable \"${d.name}\" validate: ${e.message}")
                }
            } else {
                null
            }
            val mn = d.validate.min
            val mx = d.validate.max
            if (mn != null && mx != null && mn > mx) throw SorterException("variable \"${d.name}\": min is greater than max")
            if (d.sources.isEmpty() && d.fallback.isEmpty()) {
                throw SorterException("variable \"${d.name}\" has no sources and no fallback")
            }
            val sources = d.sources.mapIndexed { i, src ->
                val n = i + 1
                val kind = SourceKind.of(src.kind)
                    ?: throw SorterException("variable \"${d.name}\" source $n: unknown kind \"${src.kind}\"")
                var re: Pattern? = null
                var group = src.group
                when (kind) {
                    SourceKind.MANUAL, SourceKind.MTIME -> {}
                    SourceKind.FILENAME, SourceKind.PATH -> {
                        if (src.pattern.isBlank()) {
                            throw SorterException("variable \"${d.name}\" source $n (${kind.id}): pattern is empty")
                        }
                        re = try {
                            compile(src.pattern)
                        } catch (e: SorterException) {
                            throw SorterException("variable \"${d.name}\" source $n: ${e.message}")
                        }
                        val groups = re.matcher("").groupCount()
                        if (group == 0 && groups > 0) group = 1
                        if (group < 0 || group > groups) {
                            throw SorterException(
                                "variable \"${d.name}\" source $n: capture group $group does not exist (the expression has $groups group(s))",
                            )
                        }
                    }
                    SourceKind.METADATA ->
                        if (src.field.isBlank()) throw SorterException("variable \"${d.name}\" source $n: metadata field is empty")
                    SourceKind.CONSTANT ->
                        if (src.value.isEmpty()) throw SorterException("variable \"${d.name}\" source $n: constant value is empty")
                }
                if (kind == SourceKind.MTIME || kind == SourceKind.METADATA) {
                    try {
                        GoLayout.formatter(src.format)
                    } catch (e: Exception) {
                        throw SorterException("variable \"${d.name}\" source $n: invalid date format \"${src.format}\"")
                    }
                }
                CompiledSource(src, kind, re, group)
            }
            Compiled(d, sources, valRe)
        }
        names = vars.map { it.def.name }
    }

    fun has(name: String) = name in names

    fun definition(name: String): VarDefinition? = vars.firstOrNull { it.def.name == name }?.def

    /** Resolves every variable for a file, in definition order. */
    fun resolveAll(fc: FileContext): Map<String, VarValue> {
        val out = LinkedHashMap<String, VarValue>()
        for (v in vars) out[v.def.name] = resolve(v, fc)
        return out
    }

    fun resolve(name: String, fc: FileContext): VarValue? = vars.firstOrNull { it.def.name == name }?.let { resolve(it, fc) }

    /** Throws [SorterException] when [value] would be rejected for variable [name] (manual input). */
    fun check(name: String, value: String) {
        val v = vars.firstOrNull { it.def.name == name } ?: throw SorterException("unknown variable \"$name\"")
        val (_, ok, why) = check(v, value)
        if (!ok) throw SorterException(why)
    }

    private fun check(c: Compiled, raw: String): Triple<String, Boolean, String> {
        val v = raw.trim()
        if (v.isEmpty()) return Triple("", false, "empty value")
        if (c.valRe != null && !c.valRe.matcher(v).find()) {
            return Triple(v, false, "\"$v\" does not match ${c.def.validate.regex}")
        }
        val mn = c.def.validate.min
        val mx = c.def.validate.max
        if (mn != null || mx != null) {
            val n = v.toIntOrNull() ?: return Triple(v, false, "\"$v\" is not a number")
            if (mn != null && n < mn) return Triple(v, false, "$n is below $mn")
            if (mx != null && n > mx) return Triple(v, false, "$n is above $mx")
        }
        return Triple(v, true, "")
    }

    private fun resolve(c: Compiled, fc: FileContext): VarValue {
        var firstBad: VarValue? = null
        fun attempt(kind: SourceKind, cand: String): VarValue? {
            val (v, ok, why) = check(c, cand)
            if (ok) return VarValue(v, kind, VarStatus.RESOLVED)
            if (v.isNotEmpty() && firstBad == null) firstBad = VarValue(source = kind, status = VarStatus.INVALID, detail = why)
            return null
        }
        for (s in c.sources) {
            when (s.kind) {
                SourceKind.MANUAL -> {
                    // a manual value is the user's explicit choice: not range-checked
                    val m = fc.manual[c.def.name]?.trim()
                    if (!m.isNullOrEmpty()) return VarValue(m, SourceKind.MANUAL, VarStatus.RESOLVED)
                }
                SourceKind.CONSTANT -> attempt(SourceKind.CONSTANT, s.src.value)?.let { return it }
                SourceKind.FILENAME -> matchAll(s, fc.name, ::attempt)?.let { return it }
                SourceKind.PATH -> matchAll(s, fc.relDir, ::attempt)?.let { return it }
                SourceKind.METADATA -> {
                    val raw = fc.fields?.invoke()?.get(s.src.field.lowercase()) ?: continue
                    attempt(SourceKind.METADATA, GoLayout.reformat(raw, s.src.format))?.let { return it }
                }
                SourceKind.MTIME -> {
                    if (fc.modTimeMs <= 0) continue
                    val t = Instant.ofEpochMilli(fc.modTimeMs).atZone(zone)
                    attempt(SourceKind.MTIME, GoLayout.format(t, s.src.format.ifEmpty { "2006" }))?.let { return it }
                }
            }
        }
        val bad = firstBad
        if (c.def.fallback.isNotEmpty()) {
            return VarValue(c.def.fallback, SourceKind.CONSTANT, VarStatus.RESOLVED, "fallback: " + (bad?.detail ?: "no source matched"))
        }
        return bad ?: VarValue(status = VarStatus.UNRESOLVED, detail = "no source matched")
    }

    /** Scans every regexp match in [s]; the first value that validates wins. */
    private fun matchAll(src: CompiledSource, s: String, attempt: (SourceKind, String) -> VarValue?): VarValue? {
        if (s.isEmpty()) return null
        val re = src.re ?: return null
        var pos = 0
        while (pos <= s.length) {
            val m = re.matcher(s.substring(pos))
            if (!m.find()) break
            val st = m.start(src.group)
            var next = pos + m.end()
            if (st >= 0) {
                val en = m.end(src.group)
                attempt(src.kind, s.substring(pos + st, pos + en))?.let { return it }
                next = pos + en
            }
            if (next <= pos) next = pos + 1
            pos = next
        }
        return null
    }

    companion object {
        private val NAME_RE = Regex("^[A-Za-z][A-Za-z0-9_]*$")

        /** Names provided by the template engine itself. */
        val RESERVED = PathTemplate.BUILTINS.toSet()

        internal fun compile(p: String): Pattern = try {
            Pattern.compile(p)
        } catch (e: PatternSyntaxException) {
            throw SorterException("invalid regular expression: ${e.description}: `$p`")
        }
    }
}
