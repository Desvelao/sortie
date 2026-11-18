package app.sortie.core

import java.time.LocalDate
import java.time.LocalDateTime
import java.time.OffsetDateTime
import java.time.Year
import java.time.YearMonth
import java.time.ZoneOffset
import java.time.ZonedDateTime
import java.time.format.DateTimeFormatter
import java.time.format.DateTimeFormatterBuilder
import java.time.format.DateTimeParseException
import java.time.format.SignStyle
import java.time.format.TextStyle
import java.time.temporal.ChronoField
import java.util.Locale

/**
 * Profiles are shared with the desktop app, whose date formats are Go reference layouts
 * (`2006` year, `01` month, `02` day, `15:04:05` time, `Jan`, `Monday`...). This turns such a
 * layout into a [DateTimeFormatter] so the same profile gives the same folders on both.
 */
object GoLayout {
    // Longest tokens first: "2006" must win over "2", "01" over "1", "January" over "Jan".
    private val tokens: List<Pair<String, (DateTimeFormatterBuilder) -> Unit>> = listOf(
        "January" to { b -> b.appendText(ChronoField.MONTH_OF_YEAR, TextStyle.FULL) },
        "Monday" to { b -> b.appendText(ChronoField.DAY_OF_WEEK, TextStyle.FULL) },
        "Z07:00" to { b -> b.appendOffset("+HH:MM", "Z") },
        "-07:00" to { b -> b.appendOffset("+HH:MM", "+00:00") },
        "Z0700" to { b -> b.appendOffset("+HHMM", "Z") },
        "-0700" to { b -> b.appendOffset("+HHMM", "+0000") },
        "2006" to { b -> b.appendValue(ChronoField.YEAR, 4, 10, SignStyle.NORMAL) },
        "Jan" to { b -> b.appendText(ChronoField.MONTH_OF_YEAR, TextStyle.SHORT) },
        "Mon" to { b -> b.appendText(ChronoField.DAY_OF_WEEK, TextStyle.SHORT) },
        "MST" to { b -> b.appendZoneText(TextStyle.SHORT) },
        "002" to { b -> b.appendValue(ChronoField.DAY_OF_YEAR, 3) },
        "-07" to { b -> b.appendOffset("+HH", "+00") },
        "01" to { b -> b.appendValue(ChronoField.MONTH_OF_YEAR, 2) },
        "02" to { b -> b.appendValue(ChronoField.DAY_OF_MONTH, 2) },
        "03" to { b -> b.appendValue(ChronoField.CLOCK_HOUR_OF_AMPM, 2) },
        "04" to { b -> b.appendValue(ChronoField.MINUTE_OF_HOUR, 2) },
        "05" to { b -> b.appendValue(ChronoField.SECOND_OF_MINUTE, 2) },
        "06" to { b -> b.appendValueReduced(ChronoField.YEAR, 2, 2, 2000) },
        "15" to { b -> b.appendValue(ChronoField.HOUR_OF_DAY, 2) },
        "PM" to { b -> b.appendText(ChronoField.AMPM_OF_DAY, mapOf(0L to "AM", 1L to "PM")) },
        "pm" to { b -> b.appendText(ChronoField.AMPM_OF_DAY, mapOf(0L to "am", 1L to "pm")) },
        "_2" to { b -> b.padNext(2).appendValue(ChronoField.DAY_OF_MONTH) },
        "1" to { b -> b.appendValue(ChronoField.MONTH_OF_YEAR) },
        "2" to { b -> b.appendValue(ChronoField.DAY_OF_MONTH) },
        "3" to { b -> b.appendValue(ChronoField.CLOCK_HOUR_OF_AMPM) },
        "4" to { b -> b.appendValue(ChronoField.MINUTE_OF_HOUR) },
        "5" to { b -> b.appendValue(ChronoField.SECOND_OF_MINUTE) },
    )

    private val cache = java.util.concurrent.ConcurrentHashMap<String, DateTimeFormatter>()

    fun formatter(layout: String): DateTimeFormatter = cache.getOrPut(layout) {
        val b = DateTimeFormatterBuilder()
        var i = 0
        val literal = StringBuilder()
        fun flush() {
            if (literal.isNotEmpty()) {
                b.appendLiteral(literal.toString())
                literal.setLength(0)
            }
        }
        while (i < layout.length) {
            val tok = tokens.firstOrNull { layout.startsWith(it.first, i) }
            if (tok == null) {
                literal.append(layout[i])
                i++
            } else {
                flush()
                tok.second(b)
                i += tok.first.length
            }
        }
        flush()
        b.toFormatter(Locale.ENGLISH)
    }

    fun format(time: ZonedDateTime, layout: String): String = time.format(formatter(layout))

    private val localDateTimes = listOf(
        DateTimeFormatter.ISO_LOCAL_DATE_TIME,
        DateTimeFormatter.ofPattern("uuuu-MM-dd HH:mm:ss"),
        DateTimeFormatter.ofPattern("uuuu:MM:dd HH:mm:ss"),
    )
    private val localDates = listOf(
        DateTimeFormatter.ISO_LOCAL_DATE,
        DateTimeFormatter.ofPattern("uuuu:MM:dd"),
        DateTimeFormatter.ofPattern("uuuu/MM/dd"),
    )

    /** Parses the date spellings metadata readers produce; null when [raw] is not a date. */
    fun parse(raw: String): ZonedDateTime? {
        val s = raw.trim()
        try {
            return OffsetDateTime.parse(s).toZonedDateTime()
        } catch (_: DateTimeParseException) {
        }
        for (f in localDateTimes) {
            try {
                return LocalDateTime.parse(s, f).atZone(ZoneOffset.UTC)
            } catch (_: DateTimeParseException) {
            }
        }
        for (f in localDates) {
            try {
                return LocalDate.parse(s, f).atStartOfDay(ZoneOffset.UTC)
            } catch (_: DateTimeParseException) {
            }
        }
        try {
            return YearMonth.parse(s).atDay(1).atStartOfDay(ZoneOffset.UTC)
        } catch (_: DateTimeParseException) {
        }
        if (s.length == 4 && s.all { it in '0'..'9' }) {
            return Year.of(s.toInt()).atDay(1).atStartOfDay(ZoneOffset.UTC)
        }
        return null
    }

    /** Re-renders [raw] with [layout]. Without a layout, or when raw is not a date, raw is returned. */
    fun reformat(raw: String, layout: String): String {
        if (layout.isEmpty()) return raw
        val t = parse(raw) ?: return raw.trim()
        return try {
            format(t, layout)
        } catch (_: Exception) {
            raw.trim()
        }
    }
}
