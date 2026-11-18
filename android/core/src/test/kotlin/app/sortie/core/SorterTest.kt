package app.sortie.core

import java.io.File
import java.nio.file.Files
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

class ScannerTest {
    private val tmp = Files.createTempDirectory("scantest").toFile()

    @AfterTest
    fun cleanup() {
        tmp.deleteRecursively()
    }

    @Test
    fun scanRules() {
        File(tmp, "b.txt").writeText("b")
        File(tmp, "A.png").writeBytes(byteArrayOf(0x89.toByte(), 0x50, 0x4E, 0x47, 1, 2))
        File(tmp, ".hidden").writeText("h")
        File(tmp, ".sortie-1.tmp").writeText("leftover")
        File(tmp, "x.part").writeText("p")
        File(tmp, "sub/deep").mkdirs()
        File(tmp, "sub/c.pdf").writeText("")
        File(tmp, "sub/deep/d.jpg").writeText("not really")
        File(tmp, ".git").mkdirs()
        File(tmp, ".git/config").writeText("x")
        Files.createSymbolicLink(File(tmp, "link.txt").toPath(), File(tmp, "b.txt").toPath())

        val flat = Scanner.scan(tmp, ScanOptions(), Cancel()).files
        assertEquals(listOf(".hidden", "A.png", "b.txt", "x.part"), flat.map { it.name })
        assertEquals("image/png", flat[1].mime)
        assertEquals(".png", flat[1].ext)
        assertEquals(6, flat[1].size)
        assertTrue(flat[1].modTimeMs > 0)

        val all = Scanner.scan(tmp, ScanOptions(recursive = true, skipHidden = true, exclude = listOf("*.part")), Cancel()).files
        assertEquals(listOf("A.png", "b.txt", "c.pdf", "d.jpg"), all.map { it.name })
        assertEquals("application/pdf", all[2].mime)
        assertEquals("image/jpeg", all[3].mime)

        assertFailsWith<SorterException> { Scanner.scan(File(tmp, "missing"), ScanOptions(), Cancel()) }
        assertFailsWith<CancelledException> { Scanner.scan(tmp, ScanOptions(), Cancel().apply { cancel() }) }
    }

    @Test
    fun extensions() {
        val cases = mapOf(
            "photo.JPG" to ".jpg", "archive.tar.gz" to ".gz", "noext" to "", ".bashrc" to "", "/home/u/.profile" to "",
            "/dir.d/noext" to "", ".hidden.txt" to ".txt", "trailing." to ".",
        )
        for ((n, want) in cases) assertEquals(want, FileEntry.extensionOf(n), n)
    }

    @Test
    fun mimeByContentThenExtension() {
        fun mime(name: String, content: ByteArray) = Mime.detect(File(tmp, name).apply { writeBytes(content) })
        assertEquals("image/png", mime("liar.jpg", byteArrayOf(0x89.toByte(), 0x50, 0x4E, 0x47)))
        assertEquals("text/plain", mime("b.txt", "hello".toByteArray()))
        assertEquals(Mime.UNKNOWN, mime("c.weird", byteArrayOf(0, 1, 2, 3)))
        assertEquals("application/pdf", mime("empty.pdf", ByteArray(0)))
        assertEquals("application/vnd.openxmlformats-officedocument.wordprocessingml.document", mime("d.docx", "PK\u0003\u0004rest".toByteArray()))
        assertEquals("application/zip", mime("e.unknownzip", "PK\u0003\u0004rest".toByteArray()))
        assertEquals("application/x-thing", Mime.detect(File(tmp, "f.thing").apply { writeText("") }) { "application/x-thing" })
        assertEquals(Mime.UNKNOWN, Mime.detect(File(tmp, "missing")))
    }
}

class FileSyncTest {
    private fun e(path: String, size: Long = 10, mtime: Long = 1000) = FileEntry(path = path, name = path.substringAfterLast('/'), size = size, modTimeMs = mtime)

    @Test
    fun addModifyRemoveRename() {
        val store = MemoryStore()
        val (first, r1) = store.syncFiles("/in", listOf(e("/in/a"), e("/in/b", 20, 2000), e("/in/c", 30, 3000)), false)
        assertEquals(SyncResult(added = 3), r1)
        assertEquals(listOf(1L, 2L, 3L), first.map { it.id })

        // a modified, b renamed to z (unique size+mtime), c gone, n new
        val (second, r2) = store.syncFiles("/in", listOf(e("/in/a", 11), e("/in/n", 5, 5), e("/in/z", 20, 2000)), false)
        assertEquals(SyncResult(added = 1, modified = 1, removed = 1, renamed = 1), r2)
        assertEquals(listOf(1L, 4L, 2L), second.map { it.id })
        assertTrue(store.rows.first { it.id == 3L }.missing)

        // c comes back: same id, no longer missing
        val (third, r3) = store.syncFiles("/in", listOf(e("/in/a", 11), e("/in/c", 30, 3000), e("/in/n", 5, 5), e("/in/z", 20, 2000)), false)
        assertEquals(SyncResult(), r3)
        assertEquals(3L, third[1].id)
        assertFalse(store.rows.first { it.id == 3L }.missing)
    }

    @Test
    fun ambiguousRenameIsAddPlusRemove() {
        val store = MemoryStore()
        store.syncFiles("/in", listOf(e("/in/a"), e("/in/b")), false)
        val (files, r) = store.syncFiles("/in", listOf(e("/in/x"), e("/in/y")), false)
        assertEquals(SyncResult(added = 2, removed = 2), r)
        assertEquals(listOf(3L, 4L), files.map { it.id })
    }

    @Test
    fun scopeOfAFlatScan() {
        val store = MemoryStore()
        store.syncFiles("/in", listOf(e("/in/a"), e("/in/sub/b", 20, 2)), true)
        // a flat rescan must not mark files of sub-folders as missing
        val (_, r) = store.syncFiles("/in", listOf(e("/in/a")), false)
        assertEquals(SyncResult(), r)
        assertTrue(FileSync.inScope("/in/sub/b", "/in", true))
        assertFalse(FileSync.inScope("/in/sub/b", "/in/", false))
        assertFalse(FileSync.inScope("/input/a", "/in", true))
    }
}

class SorterTest {
    private val tmp = Files.createTempDirectory("sortertest").toFile()
    private val input = File(tmp, "in").apply { mkdirs() }
    private val root = File(tmp, "out").apply { mkdirs() }
    private val store = MemoryStore()
    private val sorter = Sorter(store, tmp.path) { path -> if (path.endsWith("meta.txt")) mapOf("year" to "2011") else emptyMap() }

    private val profile = Profile(
        name = "Test", inputDirectory = "~/in", destinationRoot = "~/out", destinationTemplate = "{year}/{tag}/{filename}",
        variables = listOf(
            VarDefinition("year", listOf(VarSource("manual"), VarSource("filename", pattern = "(20[0-9]{2})"), VarSource("metadata", field = "year")), VarValidation(min = 1990, max = 2100)),
        ),
        tags = listOf(ProfileTag("Work", "1"), ProfileTag("Home", "2"), ProfileTag("Off", "3", enabled = false)),
    )

    @AfterTest
    fun cleanup() {
        tmp.deleteRecursively()
    }

    private fun scan() {
        val files = Scanner.scan(input, ScanOptions(recursive = true), Cancel()).files
        sorter.applyScan(input.path, files, true)
    }

    private fun setup(vararg names: String) {
        for (n in names) File(input, n).apply { parentFile.mkdirs() }.writeText("content of $n")
        sorter.applyProfile("test", profile)
        scan()
    }

    private fun tag(name: String) = sorter.tags().first { it.name == name }.id

    @Test
    fun profileAndVariables() {
        setup("a_2020.txt", "b.txt", "meta.txt")
        assertEquals(input.path, sorter.inputDir())
        assertEquals(root.path, sorter.resolver().root)
        assertEquals(listOf("Work", "Home", "Off"), sorter.tags().map { it.name })
        assertEquals(3, sorter.total())
        val a = sorter.current()!!
        assertEquals("a_2020.txt", a.name)
        assertEquals("2020", sorter.varsOf(a)[0].value.value)
        sorter.next()
        val b = sorter.current()!!
        assertFalse(sorter.varsOf(b)[0].value.ok)
        assertFailsWith<SorterException> { sorter.setManualVar(b, "year", "1800") }
        assertFailsWith<SorterException> { sorter.setManualVar(b, "nope", "x") }
        sorter.setManualVar(b, "year", "1999")
        sorter.varsOf(b)[0].let {
            assertEquals("1999", it.value.value)
            assertTrue(it.manual)
        }
        sorter.setManualVar(b, "year", "")
        assertFalse(sorter.varsOf(b)[0].value.ok)
        sorter.next()
        assertEquals("2011", sorter.varsOf(sorter.current()!!)[0].value.value)
    }

    @Test
    fun invalidProfileChangesNothing() {
        setup("a_2020.txt")
        assertFailsWith<ProfileException> { sorter.applyProfile("test", profile.copy(destinationTemplate = "{nope}/{filename}")) }
        assertFailsWith<ProfileException> { sorter.updateProfile { it.copy(tags = emptyList()) } }
        assertEquals(profile, sorter.profile())
        assertEquals(3, sorter.tags().size)
    }

    @Test
    fun taggingNavigationAndFilters() {
        setup("a_2020.txt", "b_2021.txt", "c_2022.txt", "d.txt")
        assertEquals(1 to 4, sorter.position())
        assertTrue(sorter.toggleTag(tag("Work")))
        assertTrue(sorter.toggleByShortcut("2"))
        assertFalse(sorter.toggleByShortcut("9"))
        assertFailsWith<SorterException> { sorter.toggleTag(tag("Off")) }
        assertEquals(setOf(tag("Work"), tag("Home")), sorter.tagsOf(sorter.current()!!))
        assertFalse(sorter.toggleTag(tag("Home")))
        assertEquals(1, sorter.taggedCount())

        sorter.setFilter(Filter.UNTAGGED)
        // the current file no longer matches: the nearest matching one becomes current
        assertEquals("b_2021.txt", sorter.current()!!.name)
        assertEquals(1 to 3, sorter.position())
        // tagging in the Untagged view keeps the file visible until the user moves away
        sorter.toggleTag(tag("Work"))
        assertEquals("b_2021.txt", sorter.current()!!.name)
        assertEquals(1 to 3, sorter.position())
        sorter.next()
        assertEquals("c_2022.txt", sorter.current()!!.name)
        assertEquals(1 to 2, sorter.position())
        sorter.prev()
        assertEquals("c_2022.txt", sorter.current()!!.name, "nothing untagged before it")
        sorter.last()
        assertEquals("d.txt", sorter.current()!!.name)

        sorter.setFilter(Filter.TAGGED)
        assertEquals(2, sorter.count())
        assertEquals("b_2021.txt", sorter.current()!!.name)
        sorter.first()
        assertEquals("a_2020.txt", sorter.current()!!.name)
        sorter.setFilter(Filter.UNRESOLVED)
        assertEquals(listOf("d.txt"), sorter.neighbors(3, 3).first.map { it.name })
        sorter.setFilter(Filter.ALL)
        assertTrue(sorter.goTo(File(input, "c_2022.txt").path))
        val (around, at) = sorter.neighbors(1, 1)
        assertEquals(listOf("b_2021.txt", "c_2022.txt", "d.txt"), around.map { it.name })
        assertEquals(1, at)
        assertFalse(sorter.goTo("/nope"))
    }

    @Test
    fun checksCopyAndStatusFilters() {
        setup("a_2020.txt", "b_2021.txt", "d.txt")
        sorter.toggleTag(tag("Work"))
        sorter.toggleTag(tag("Home"))
        val a = sorter.current()!!
        val checks = sorter.checks(a, Cancel())
        assertEquals(listOf("Work", "Home"), checks.map { it.tagName })
        assertEquals(File(root, "2020/Work/a_2020.txt").path, checks[0].path)
        assertTrue(checks.all { it.status == DestStatus.READY })
        sorter.last()
        sorter.toggleTag(tag("Work")) // d.txt: year unresolved
        assertEquals(DestStatus.UNRESOLVED_VAR, sorter.checks(sorter.current()!!, Cancel())[0].status)

        sorter.setFilter(Filter.READY)
        assertFalse(sorter.statusesKnown())
        sorter.refreshStatuses(Cancel())
        assertTrue(sorter.statusesKnown())
        assertEquals(1, sorter.count())
        assertEquals("a_2020.txt", sorter.current()!!.name)

        assertEquals(2, sorter.copyJobs(false).size)
        val jobs = sorter.copyJobs(true)
        assertEquals(3, jobs.size)
        assertEquals("in", jobs[0].vars["parent"])
        assertEquals(listOf(DestStatus.READY, DestStatus.READY, DestStatus.UNRESOLVED_VAR), sorter.planCopy(jobs, Cancel()).map { it.status })
        val sum = sorter.runCopy(jobs, CopyOptions(), Cancel())
        assertEquals(2, sum.count(CopyStatus.COPIED))
        assertEquals(1, sum.count(CopyStatus.SKIPPED_INVALID))
        assertTrue(File(root, "2020/Home/a_2020.txt").isFile)
        assertEquals(2, sorter.history(a).size)

        sorter.setFilter(Filter.ALREADY_COPIED)
        assertEquals(1, sorter.count())
        sorter.setFilter(Filter.READY)
        assertEquals(0, sorter.count())
        assertNull(sorter.current())
        assertEquals(0 to 0, sorter.position())

        // a different file at a destination is a conflict after a refresh
        File(root, "2020/Work/a_2020.txt").writeText("something else")
        sorter.setFilter(Filter.ALL)
        scan() // invalidates statuses
        sorter.refreshStatuses(Cancel())
        sorter.setFilter(Filter.CONFLICTS)
        assertEquals(listOf("a_2020.txt"), sorter.neighbors(5, 5).first.map { it.name })
    }

    @Test
    fun rescanKeepsClassificationAndSession() {
        setup("a_2020.txt", "b_2021.txt")
        sorter.next()
        sorter.toggleTag(tag("Work"))
        val b = sorter.current()!!
        sorter.setManualVar(b, "year", "2005")
        // renamed on disk: same size and time
        val renamed = File(input, "renamed.txt")
        val t = File(b.path).lastModified()
        assertTrue(File(b.path).renameTo(renamed))
        renamed.setLastModified(t)
        scan()
        val now = sorter.neighbors(5, 5).first.first { it.name == "renamed.txt" }
        assertEquals(b.id, now.id)
        assertEquals(setOf(tag("Work")), sorter.tagsOf(now))
        assertEquals("2005", sorter.varsOf(now)[0].value.value)

        // a new session restores the profile state and the position
        sorter.goTo(renamed.path)
        val again = Sorter(store, tmp.path)
        again.applyProfile("test", profile)
        again.applyScan(input.path, Scanner.scan(input, ScanOptions(), Cancel()).files, true)
        assertEquals("renamed.txt", again.current()!!.name)
        assertEquals(1, again.taggedCount())
    }

    @Test
    fun profilesKeepSeparateTagging() {
        setup("a_2020.txt")
        sorter.toggleTag(tag("Work"))
        sorter.applyProfile("other", profile.copy(name = "Other", tags = listOf(ProfileTag("Work"))))
        assertEquals(0, sorter.taggedCount())
        assertEquals(1, sorter.taggedFileCount("test"))
        assertFailsWith<SorterException> { sorter.deleteProfileData("other") }
        sorter.deleteProfileData("test")
        assertEquals(0, sorter.taggedFileCount("test"))
        // removing a tag from a profile removes its assignments
        sorter.toggleTag(tag("Work"))
        sorter.updateProfile { it.copy(tags = listOf(ProfileTag("New"))) }
        assertEquals(0, sorter.taggedCount())
    }
}
