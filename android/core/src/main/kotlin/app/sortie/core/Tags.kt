package app.sortie.core

/** A user-defined category. Ids are assigned by the database; profiles identify tags by name. */
data class Tag(
    val id: Int = 0,
    val name: String,
    val shortcut: String = "", // single key: A-Z, 0-9, F1-F12; may be empty
    val destination: String = name, // relative directory; what {tag} expands to
    val destinationTemplate: String = "", // replaces the profile template for this tag
    val enabled: Boolean = true,
    val sortOrder: Int = 0,
    val color: String = "", // "#RRGGBB" / "#RGB"; empty = automatic
)

object Tags {
    private val reserved = setOf("LEFT", "RIGHT", "UP", "DOWN", "HOME", "END", "ESCAPE", "RETURN", "TAB", "SPACE")

    val DEFAULT_NAMES = listOf("Family", "Holidays", "Events", "Work", "Music", "Documents", "Personal", "Important", "Archive")

    /** Parses "#RGB" or "#RRGGBB" (the leading # is optional) into 0xRRGGBB. */
    fun parseColor(s: String): Int {
        var h = s.trim().removePrefix("#")
        if (h.length == 3) h = "" + h[0] + h[0] + h[1] + h[1] + h[2] + h[2]
        val v = if (h.length == 6 && h.all { it.isDigit() || it.lowercaseChar() in 'a'..'f' }) h.toIntOrNull(16) else null
        return v ?: throw SorterException("color \"$s\": expected #RRGGBB")
    }

    /** Upper-cases and validates a shortcut ("" is allowed). */
    fun normalizeShortcut(raw: String): String {
        val s = raw.trim().uppercase()
        if (s.isEmpty()) return ""
        if (s in reserved) throw SorterException("shortcut \"$s\" is reserved for navigation")
        if (s.length == 1 && (s[0] in 'A'..'Z' || s[0] in '0'..'9')) return s
        if (s.length >= 2 && s[0] == 'F') {
            val n = s.substring(1).toIntOrNull()
            if (n != null && n in 1..12 && n.toString() == s.substring(1)) return s
        }
        throw SorterException("shortcut \"$s\" is not supported (use A–Z, 0–9 or F1–F12)")
    }

    /** Requires a non-empty relative path without traversal. */
    fun validateDestination(raw: String) {
        val d = raw.trim()
        if (d.isEmpty()) throw SorterException("destination is empty")
        if (d.contains('\u0000')) throw SorterException("destination contains an invalid character")
        if (d.startsWith("/") || d.startsWith("\\") || (d.length > 1 && d[1] == ':')) {
            throw SorterException("destination \"$d\" must be relative")
        }
        val parts = d.split('/', '\\').filter { it.isNotEmpty() }
        if (parts.any { it == ".." }) throw SorterException("destination \"$d\" must not contain '..'")
        if (parts.all { it == "." }) throw SorterException("destination \"$d\" is empty")
    }

    /**
     * Checks names, shortcuts, destinations and colors of a whole set. Shortcuts must be unique
     * among all tags, disabled ones included: a disabled tag can be re-enabled at any time.
     */
    fun validate(tags: List<Tag>) {
        val names = HashSet<String>()
        val keys = HashMap<String, String>()
        for (t in tags) {
            if (t.name.isBlank()) throw SorterException("a tag has an empty name")
            if (!names.add(t.name.trim().lowercase())) throw SorterException("duplicate tag name \"${t.name}\"")
            try {
                val sc = normalizeShortcut(t.shortcut)
                if (sc.isNotEmpty()) {
                    keys.put(sc, t.name)?.let { other ->
                        throw SorterException("shortcut $sc is used by both \"$other\" and \"${t.name}\"")
                    }
                }
                validateDestination(t.destination)
                if (t.color.isNotBlank()) parseColor(t.color)
            } catch (e: SorterException) {
                if (e.message!!.startsWith("shortcut ") && e.message!!.contains("used by both")) throw e
                throw SorterException("tag \"${t.name}\": ${e.message}")
            }
        }
    }

    /** Finds the enabled tag bound to [key]. */
    fun byShortcut(tags: List<Tag>, key: String): Tag? {
        val k = key.uppercase()
        return tags.firstOrNull { it.enabled && it.shortcut.isNotEmpty() && it.shortcut == k }
    }
}
