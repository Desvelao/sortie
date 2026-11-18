package app.sortie.core

/** A template variable has no value and no default. */
class UnresolvedException(val variable: String) : SorterException("variable {$variable} cannot be determined")

/**
 * Destination path templates such as `{tag}/{year}/{camera|unknown}/{filename}`.
 *
 *     {name}            value of a built-in or user-defined variable
 *     {name:format}     with a format: upper, lower, slug, padN (zero-pad to N)
 *     {name|default}    literal default when the variable is unresolved
 *     {name:fmt|dflt}   both
 *
 * Rendered values are sanitised into single path components, so a value can never add or remove
 * directory levels or contain "..".
 */
object PathTemplate {
    /** Always available. `{filename}` may only appear in the last path component. */
    val BUILTINS = listOf("tag", "filename", "name", "ext", "parent")

    /** Longest file or folder name common filesystems accept, in UTF-8 bytes. */
    const val MAX_COMPONENT_BYTES = 255

    const val DEFAULT = "{tag}/{filename}"

    private val placeholderRe = Regex("""\{[^{}]*\}""")

    private class Placeholder(val name: String, val format: String, val default: String?)

    private fun parsePlaceholder(raw: String): Placeholder {
        var body = raw.substring(1, raw.length - 1)
        var default: String? = null
        body.indexOf('|').let { if (it >= 0) { default = body.substring(it + 1); body = body.substring(0, it) } }
        var format = ""
        body.indexOf(':').let { if (it >= 0) { format = body.substring(it + 1); body = body.substring(0, it) } }
        val name = body.trim()
        if (name.isEmpty()) throw SorterException("empty placeholder $raw")
        if (default?.any { it == '/' || it == '\\' } == true) {
            throw SorterException("default in $raw must not contain a path separator")
        }
        checkFormat(format)?.let { throw SorterException("$raw: $it") }
        return Placeholder(name, format, default)
    }

    private fun checkFormat(f: String): String? {
        if (f.isEmpty() || f == "upper" || f == "lower" || f == "slug") return null
        if (f.startsWith("pad")) {
            val n = f.substring(3).toIntOrNull()
            if (n != null && n in 1..32) return null
        }
        return "unknown format \"$f\" (use upper, lower, slug, padN)"
    }

    /** Checks syntax and that every variable is built in or in [known]; throws [SorterException]. */
    fun validate(template: String, known: Collection<String>) {
        val t = template.trim()
        if (t.isEmpty()) throw SorterException("template is empty")
        if (t.startsWith("/")) throw SorterException("template must be relative (no leading /)")
        if (t.contains('\u0000')) throw SorterException("template contains an invalid character")
        val ok = BUILTINS.toSet() + known
        for (m in placeholderRe.findAll(t)) {
            val p = parsePlaceholder(m.value)
            if (p.name !in ok) throw SorterException("unknown variable {${p.name}}")
        }
        if (placeholderRe.replace(t, "").any { it == '{' || it == '}' }) {
            throw SorterException("unbalanced braces in template")
        }
        val parts = splitPath(t)
        if (parts.isEmpty()) throw SorterException("template is empty")
        parts.forEachIndexed { i, part ->
            if (part == "..") throw SorterException("template must not contain '..'")
            if (i < parts.size - 1 && part.contains("{filename}")) {
                throw SorterException("{filename} is only allowed in the last path component")
            }
        }
        val hasName = placeholderRe.findAll(parts.last()).any {
            val n = parsePlaceholder(it.value).name
            n == "filename" || n == "name"
        }
        if (!hasName) throw SorterException("the last path component must contain {filename} or {name}")
    }

    /** [validate] as a message: null when the template is fine. */
    fun error(template: String, known: Collection<String>): String? =
        try {
            validate(template, known)
            null
        } catch (e: SorterException) {
            e.message
        }

    /** Splits on "/" dropping empty and "." components. */
    fun splitPath(p: String): List<String> = p.split('/').filter { it.isNotEmpty() && it != "." }

    /**
     * Fills the template and returns the relative path components. [lookup] returns null for an
     * unresolved variable (an empty string is a value, e.g. `{ext}` of a file without extension).
     * Throws [UnresolvedException] or [SorterException].
     */
    fun render(template: String, lookup: (String) -> String?): List<String> {
        val out = placeholderRe.replace(template.trim()) { m ->
            val p = parsePlaceholder(m.value)
            val v = lookup(p.name)?.trim() ?: p.default ?: throw UnresolvedException(p.name)
            applyFormat(sanitize(v, keepSlash = p.name == "tag"), p.format)
        }
        // splitPath drops empty components, so an empty last one would silently turn the previous
        // component (e.g. the tag folder) into the file name.
        val last = out.substringAfterLast('/')
        if (last.trim().isEmpty() || last == ".") {
            throw SorterException("the file name part of the resolved path is empty")
        }
        val parts = splitPath(out)
        if (parts.isEmpty()) throw SorterException("resolved path is empty")
        for (p in parts) {
            if (p == "..") throw SorterException("resolved path contains '..'")
            val bytes = p.toByteArray(Charsets.UTF_8).size
            if (bytes > MAX_COMPONENT_BYTES) {
                throw SorterException("a path component is $bytes bytes long (maximum $MAX_COMPONENT_BYTES): \"${p.take(40)}\"…")
            }
        }
        return parts
    }

    /** Makes v safe inside one path component. The tag destination keeps "/" (nested folders). */
    private fun sanitize(v: String, keepSlash: Boolean): String {
        val sb = StringBuilder(v.length)
        for (c in v) {
            when {
                c.code < 0x20 || c.code == 0x7f -> {}
                c == '\\' -> sb.append('/')
                else -> sb.append(c)
            }
        }
        return if (keepSlash) sb.toString() else sb.toString().replace('/', '_')
    }

    internal fun applyFormat(v: String, f: String): String = when {
        f == "upper" -> v.uppercase()
        f == "lower" -> v.lowercase()
        f == "slug" -> slug(v, unicode = true, empty = "")
        f.startsWith("pad") -> {
            val n = f.substring(3).toIntOrNull() ?: 0
            val len = v.codePointCount(0, v.length)
            if (len < n) "0".repeat(n - len) + v else v
        }
        else -> v
    }

    /** Lower-case letters and digits joined by single dashes. */
    internal fun slug(v: String, unicode: Boolean, empty: String): String {
        val sb = StringBuilder()
        var dash = false
        val lower = v.lowercase()
        var i = 0
        while (i < lower.length) {
            val cp = lower.codePointAt(i)
            i += Character.charCount(cp)
            val keep = if (unicode) Character.isLetter(cp) || Character.isDigit(cp) else (cp in 'a'.code..'z'.code || cp in '0'.code..'9'.code)
            if (keep) {
                sb.appendCodePoint(cp)
                dash = false
            } else if (!dash && sb.isNotEmpty()) {
                sb.append('-')
                dash = true
            }
        }
        return sb.toString().trim('-').ifEmpty { empty }
    }
}
