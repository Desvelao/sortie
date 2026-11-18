package app.sortie.core

import java.time.ZoneOffset
import java.time.ZonedDateTime
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

class PathTemplateTest {
    private fun render(tpl: String, m: Map<String, String>) = PathTemplate.render(tpl) { m[it] }.joinToString("/")

    @Test
    fun validate() {
        val known = listOf("year", "camera", "namespace", "name_prefix")
        for (g in listOf("{year}/{tag}/{filename}", "{tag}/{camera|unknown}/{name}_{year:pad4}{ext}", "Fixed/{filename}", "{camera:slug}/{filename}")) {
            assertNull(PathTemplate.error(g, known), g)
        }
        val bad = listOf(
            "", "/abs/{filename}", "{year}/{tag}", "{nope}/{filename}", "{year/{filename}", "../{filename}",
            "{filename}/x/{name}", "{year:bogus}/{filename}", "{}/{filename}", "{camera|a/b}/{filename}",
            // user variables that merely start with "name" are not the file name
            "{tag}/{namespace}", "{tag}/{name_prefix}_{year}",
        )
        for (b in bad) assertTrue(PathTemplate.error(b, known) != null, "expected error for \"$b\"")
    }

    @Test
    fun render() {
        val m = mapOf(
            "year" to "2021", "tag" to "Work/Invoices", "filename" to "a b.pdf", "name" to "a b", "ext" to ".pdf",
            "client" to "Acme Corp/../x", "n" to "7",
        )
        val cases = mapOf(
            "{year}/{tag}/{filename}" to "2021/Work/Invoices/a b.pdf",
            "{client:slug}/{filename}" to "acme-corp-x/a b.pdf",
            "{client}/{filename}" to "Acme Corp_.._x/a b.pdf",
            "{missing|none}/{filename}" to "none/a b.pdf",
            "{n:pad3}/{missing:upper|x}/{filename}" to "007/X/a b.pdf",
            "{name}-{year}{ext}" to "a b-2021.pdf",
        )
        for ((tpl, want) in cases) assertEquals(want, render(tpl, m), tpl)
    }

    @Test
    fun unresolvedAndTraversal() {
        val e = assertFailsWith<UnresolvedException> { render("{year}/{filename}", mapOf("filename" to "a")) }
        assertEquals("year", e.variable)
        // a value of ".." must not survive as its own component
        assertFailsWith<SorterException> { render("{v}/{filename}", mapOf("v" to "..", "filename" to "a")) }
        // a tag destination with .. is rejected
        assertFailsWith<SorterException> { render("{tag}/{filename}", mapOf("tag" to "a/../..", "filename" to "f")) }
    }

    @Test
    fun emptyValues() {
        assertEquals("noext", render("{name}{ext}", mapOf("name" to "noext", "ext" to "")))
        // {name} empty must not turn "{tag}/{name}" into just "{tag}"
        assertFailsWith<SorterException> { render("{tag}/{name}", mapOf("tag" to "Work", "name" to "")) }
        // an empty value in a middle component is dropped
        assertEquals("Work/a", render("{c}/{tag}/{filename}", mapOf("c" to "", "tag" to "Work", "filename" to "a")))
    }

    @Test
    fun componentLengthLimitIsInBytes() {
        val max = PathTemplate.MAX_COMPONENT_BYTES
        assertFailsWith<SorterException> { render("{tag}/{filename}", mapOf("tag" to "T", "filename" to "a".repeat(max + 1))) }
        render("{tag}/{filename}", mapOf("tag" to "T", "filename" to "a".repeat(max)))
        assertFailsWith<SorterException> { render("{tag}/{filename}", mapOf("tag" to "T", "filename" to "日".repeat(max / 3 + 1))) }
    }

    @Test
    fun slugKeepsUnicodeLettersAndPadCountsCharacters() {
        assertEquals("café-ünï-日本語", PathTemplate.applyFormat("Café Ünï 日本語!", "slug"))
        assertEquals("00日本", PathTemplate.applyFormat("日本", "pad4"))
    }
}

class VarsTest {
    private fun yearDef() = VarDefinition(
        name = "year",
        sources = listOf(
            VarSource("manual"),
            VarSource("filename", pattern = """(^|[^0-9])(20[0-9]{2})([^0-9]|$)""", group = 2),
            VarSource("metadata", field = "date", format = "2006"),
        ),
        validate = VarValidation(min = 1970, max = 2100),
    )

    private fun set(vararg d: VarDefinition) = VarSet(d.toList(), ZoneOffset.UTC)

    @Test
    fun yearFromFilename() {
        val s = set(yearDef())
        val cases = mapOf(
            "IMG_20210315.jpg" to "", "Invoice_2020.pdf" to "2020", "2023-05-17 trip.jpg" to "2023", "unknown_file.pdf" to "",
            "a_1999_b_2005.txt" to "2005", "2019-2020.txt" to "2019", "ñandú_2018_日本.pdf" to "2018", "x2024" to "2024", "2150.txt" to "",
        )
        for ((name, want) in cases) assertEquals(want, s.resolve("year", FileContext(name))!!.value, name)
    }

    @Test
    fun invalidAndMultipleCandidates() {
        val d = yearDef().let { it.copy(sources = it.sources.toMutableList().also { l -> l[1] = l[1].copy(pattern = """(^|[^0-9])([0-9]{4})([^0-9]|$)""") }) }
        val s = set(d)
        assertEquals("2004", s.resolve("year", FileContext("scan_1850_then_2004.pdf"))!!.value)
        val v = s.resolve("year", FileContext("scan_1850.pdf"))!!
        assertEquals(VarStatus.INVALID, v.status)
        assertTrue(v.detail.contains("1850"))
    }

    @Test
    fun priorityChain() {
        val s = set(yearDef())
        val meta = { mapOf("date" to "2011:05:01 10:00:00") }
        val bad = { mapOf("date" to "1800-01-01") }
        s.resolve("year", FileContext("x_2020.txt", manual = mapOf("year" to "1999"), fields = meta))!!.let {
            assertEquals("1999", it.value)
            assertEquals(SourceKind.MANUAL, it.source)
        }
        s.resolve("year", FileContext("x_2020.txt", fields = meta))!!.let {
            assertEquals("2020", it.value)
            assertEquals(SourceKind.FILENAME, it.source)
        }
        s.resolve("year", FileContext("none.txt", fields = meta))!!.let {
            assertEquals("2011", it.value)
            assertEquals(SourceKind.METADATA, it.source)
        }
        assertFalse(s.resolve("year", FileContext("none.txt", fields = bad))!!.ok)
    }

    @Test
    fun metadataIsLazy() {
        var called = false
        set(yearDef()).resolveAll(FileContext("a_2020.txt", fields = { called = true; emptyMap() }))
        assertFalse(called, "metadata read although the filename resolved")
    }

    @Test
    fun pathMtimeConstantFallback() {
        val s = set(
            VarDefinition("client", listOf(VarSource("path", pattern = "^clients/([^/]+)")), fallback = "misc"),
            VarDefinition("month", listOf(VarSource("mtime", format = "01"))),
            VarDefinition("kind", listOf(VarSource("constant", value = "scan"))),
        )
        val t = ZonedDateTime.of(2022, 3, 4, 0, 0, 0, 0, ZoneOffset.UTC).toInstant().toEpochMilli()
        val got = s.resolveAll(FileContext("a.pdf", "clients/acme/2020", t))
        assertEquals("acme", got.getValue("client").value)
        assertEquals("03", got.getValue("month").value)
        assertEquals("scan", got.getValue("kind").value)
        val other = s.resolveAll(FileContext("a.pdf", "other", t)).getValue("client")
        assertEquals("misc", other.value)
        assertTrue(other.ok)
    }

    @Test
    fun validateRegexAndDefinitionErrors() {
        val s = set(VarDefinition("code", listOf(VarSource("filename", pattern = """([A-Z]+-\d+)""")), VarValidation(regex = "^ABC-")))
        assertEquals("ABC-22", s.resolve("code", FileContext("XYZ-1 ABC-22.txt"))!!.value)
        val bad = listOf(
            VarDefinition("1x", listOf(VarSource("manual"))),
            VarDefinition("tag", listOf(VarSource("manual"))),
            VarDefinition("a"),
            VarDefinition("a", listOf(VarSource("nope"))),
            VarDefinition("a", listOf(VarSource("filename", pattern = "(unclosed"))),
            VarDefinition("a", listOf(VarSource("filename", pattern = """(\d)""", group = 2))),
            VarDefinition("a", listOf(VarSource("filename"))),
            VarDefinition("a", listOf(VarSource("metadata"))),
            VarDefinition("a", listOf(VarSource("manual")), VarValidation(min = 5, max = 1)),
        )
        bad.forEachIndexed { i, d -> assertFailsWith<SorterException>("case $i") { set(d) } }
        assertFailsWith<SorterException> { set(yearDef(), yearDef()) }
    }

    @Test
    fun wholeMatchWithoutGroup() {
        assertEquals("2021", set(VarDefinition("y", listOf(VarSource("filename", pattern = "20[0-9]{2}")))).resolve("y", FileContext("trip_2021.jpg"))!!.value)
        assertEquals("42", set(VarDefinition("c", listOf(VarSource("filename", pattern = """x_(\d+)""")))).resolve("c", FileContext("x_42.jpg"))!!.value)
    }

    @Test
    fun manualInputIsChecked() {
        val s = set(yearDef())
        s.check("year", "2020")
        assertFailsWith<SorterException> { s.check("year", "1800") }
        assertFailsWith<SorterException> { s.check("year", "abc") }
    }
}

class GoLayoutTest {
    private val t = ZonedDateTime.of(2022, 3, 4, 15, 7, 9, 0, ZoneOffset.UTC)

    @Test
    fun commonLayouts() {
        val cases = mapOf(
            "2006" to "2022", "2006-01" to "2022-03", "2006-01-02" to "2022-03-04", "01" to "03", "Jan" to "Mar",
            "January 2006" to "March 2022", "06" to "22", "2 Jan" to "4 Mar", "15:04:05" to "15:07:09", "3PM" to "3PM",
            "2006/01/02 Mon" to "2022/03/04 Fri", "20060102" to "20220304",
        )
        for ((layout, want) in cases) assertEquals(want, GoLayout.format(t, layout), layout)
    }

    @Test
    fun reformatMetadataDates() {
        assertEquals("2011-05", GoLayout.reformat("2011:05:01 10:00:00", "2006-01"))
        assertEquals("2019", GoLayout.reformat("2019-07-01T12:00:00+02:00", "2006"))
        assertEquals("2020", GoLayout.reformat("2020", "2006"))
        assertEquals("not a date", GoLayout.reformat("not a date", "2006"))
        assertEquals("2011:05:01", GoLayout.reformat("2011:05:01", ""))
    }
}
