package app.sortie.core

import java.io.File
import java.nio.file.Files
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

class ProfileTest {
    private fun resource(name: String) = javaClass.getResource("/profiles/$name")!!.readText()

    private val minimal = """
        version: 1
        name: Test
        destination_template: "{tag}/{filename}"
        tags:
          - {name: A, shortcut: "1"}
          - name: B
            shortcut: b
            destination: Sub/B
            enabled: false
            color: "#abc"
    """.trimIndent()

    @Test
    fun desktopExampleProfilesParse() {
        for (f in listOf("family-photos.yaml", "music-library.yaml", "client-documents.yaml", "invoices.yaml")) {
            val p = ProfileYaml.parse(resource(f))
            assertTrue(p.tags.isNotEmpty(), f)
            // a round trip through our writer gives the same profile
            assertEquals(p, ProfileYaml.parse(ProfileYaml.marshal(p)), f)
        }
        val photos = ProfileYaml.parse(resource("family-photos.yaml"))
        assertEquals("Family photos", photos.name)
        assertTrue(photos.recursive)
        assertEquals("~/Pictures/Sorted", photos.destinationRoot)
        assertEquals(listOf("manual", "filename", "metadata", "mtime"), photos.variables[0].sources.map { it.kind })
        assertEquals(2, photos.variables[0].sources[1].group)
        assertEquals(1990, photos.variables[0].validate.min)
        assertEquals("1", photos.tags[0].shortcut)
        val docs = ProfileYaml.parse(resource("client-documents.yaml"))
        assertEquals("rename", docs.conflictPolicy)
        assertEquals("Tax/{year}/{client}/{filename}", docs.tags[3].destinationTemplate)
        assertEquals("unassigned", docs.variables[0].fallback)
    }

    @Test
    fun parsesFieldsAndDefaults() {
        val p = ProfileYaml.parse(minimal)
        assertFalse(p.recursive)
        val tags = p.toTags(mapOf("A" to 7))
        assertEquals(7, tags[0].id)
        assertEquals("A", tags[0].destination)
        assertTrue(tags[0].enabled)
        assertEquals("B", tags[1].shortcut)
        assertEquals("Sub/B", tags[1].destination)
        assertFalse(tags[1].enabled)
        assertEquals(1, tags[1].sortOrder)
    }

    @Test
    fun errorsNameTheLine() {
        fun err(text: String) = assertFailsWith<SorterException> { ProfileYaml.parse(text) }.message!!
        assertTrue(err(minimal.replace("destination: Sub/B", "destinatoin: Sub/B")).let { it.contains("line 8") && it.contains("destinatoin") })
        assertTrue(err(minimal.replace("{tag}/{filename}", "{clent}/{filename}")).let { it.contains("line 3") && it.contains("{clent}") })
        assertTrue(err(minimal.replace("shortcut: b", "shortcut: \"1\"")).contains("shortcut 1 is used by both"))
        assertTrue(err(minimal.replace("enabled: false", "enabled: maybe")).contains("line 9"))
        assertTrue(err(minimal.replace("color: \"#abc\"", "color: red")).let { it.contains("line 6") && it.contains("#RRGGBB") })
        assertTrue(err(minimal.replace("version: 1", "version: 2")).contains("newer"))
        assertTrue(err("").contains("empty"))
        assertTrue(err("version: 1\nname: X\n").contains("at least one tag"))
        assertTrue(err("version: 1\nname: [unclosed\n").contains("invalid profile"))
        assertTrue(err(minimal.replace("name: B", "name: a")).contains("duplicate tag name"))
        assertTrue(err(minimal.replace("destination: Sub/B", "destination: ../B")).contains(".."))
    }

    @Test
    fun exampleProfileIsValidAndWritable() {
        val p = Profile.example("Mine")
        p.validate()
        val text = ProfileYaml.marshal(p)
        assertTrue(text.contains("shortcut: '1'") || text.contains("shortcut: \"1\""), text)
        assertEquals(p, ProfileYaml.parse(text))
    }

    @Test
    fun expandPaths() {
        assertEquals("/sd/Pictures/in", Profile.expandPath("~/Pictures/in", "/sd", "/base"))
        assertEquals("/sd", Profile.expandPath("~", "/sd/", "/base"))
        assertEquals("/base/rel", Profile.expandPath("rel/x/..", "/sd", "/base"))
        assertEquals("/abs", Profile.expandPath("/abs", "/sd", "/base"))
        assertEquals("", Profile.expandPath("  ", "/sd", "/base"))
    }

    @Test
    fun renameKeepsComments() {
        val text = "# my notes\nversion: 1\nname: Old # old name\ntags:\n  - {name: A} # keep\n"
        val out = ProfileYaml.rename(text, "New \"name\"")
        assertTrue(out.contains("# my notes") && out.contains("# keep"))
        assertEquals("New \"name\"", ProfileYaml.parse(out).name)
    }

    @Test
    fun storeLifecycle() {
        val dir = Files.createTempDirectory("profiles").toFile()
        val store = ProfileStore(dir)
        assertTrue(store.list().isEmpty())
        val id = store.create("Family Photos")
        assertEquals("family-photos", id)
        assertEquals("family-photos-2", store.create("Family Photos"))
        val dup = store.duplicate(id, "Copy of it")
        assertEquals("Copy of it", store.load(dup).name)
        store.rename(id, "Renamed")
        assertEquals(listOf("Copy of it", "Family Photos", "Renamed"), store.list().map { it.name })

        val imported = store.import("/some/where/Music Library.yaml", resource("music-library.yaml"))
        assertEquals("music-library", imported)
        assertFailsWith<SorterException> { store.import("bad.yaml", "name: x") }

        File(dir, "broken.yaml").writeText("version: 1\nname: Broken\n")
        val broken = store.list().first { it.id == "broken" }
        assertNotNull(broken.error)
        assertEquals("broken", broken.name)

        val trashed = store.delete(dup)
        assertTrue(trashed.exists() && trashed.parentFile.name == ".trash")
        assertFalse(store.exists(dup))
        assertFailsWith<SorterException> { store.fileFor("../etc") }
        assertFailsWith<SorterException> { store.saveText(id, "nonsense: true") }
        store.saveText(id, minimal)
        assertEquals("Test", store.load(id).name)
        dir.deleteRecursively()
    }

    @Test
    fun slug() {
        assertEquals("family-photos", ProfileStore.slug("Family  Photos!"))
        assertEquals("profile", ProfileStore.slug("日本"))
    }
}

class TagsTest {
    @Test
    fun shortcuts() {
        assertEquals("A", Tags.normalizeShortcut(" a "))
        assertEquals("F12", Tags.normalizeShortcut("f12"))
        assertEquals("", Tags.normalizeShortcut(""))
        for (bad in listOf("F13", "F0", "F01", "AB", "!", "left", "space")) assertFailsWith<SorterException>(bad) { Tags.normalizeShortcut(bad) }
    }

    @Test
    fun destinationsAndColors() {
        Tags.validateDestination("Work/Invoices")
        for (bad in listOf("", "/abs", "\\abs", "C:x", "a/../b", ".", "./.")) assertFailsWith<SorterException>(bad) { Tags.validateDestination(bad) }
        assertEquals(0xE4572E, Tags.parseColor("#E4572E"))
        assertEquals(0xAABBCC, Tags.parseColor("abc"))
        assertFailsWith<SorterException> { Tags.parseColor("#12345") }
        assertFailsWith<SorterException> { Tags.parseColor("#GGGGGG") }
    }

    @Test
    fun byShortcutIgnoresDisabled() {
        val ts = listOf(Tag(1, "A", "1"), Tag(2, "B", "2", enabled = false))
        assertEquals(1, Tags.byShortcut(ts, "1")?.id)
        assertEquals(null, Tags.byShortcut(ts, "2"))
    }
}
