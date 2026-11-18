package app.sortie.data

import androidx.test.core.app.ApplicationProvider
import app.sortie.core.CopyJob
import app.sortie.core.CopyResult
import app.sortie.core.CopyStatus
import app.sortie.core.FileEntry
import app.sortie.core.Store
import app.sortie.core.Tag
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

@RunWith(RobolectricTestRunner::class)
class RoomStoreTest {
    private lateinit var db: AppDatabase
    private lateinit var store: RoomStore

    @Before
    fun setUp() {
        db = AppDatabase.inMemory(ApplicationProvider.getApplicationContext())
        store = RoomStore(db)
    }

    @After
    fun tearDown() = db.close()

    private fun file(name: String, size: Long = 10, mtime: Long = 1000, dir: String = "/in") =
        FileEntry(path = "$dir/$name", name = name, size = size, modTimeMs = mtime)

    private fun sync(vararg files: FileEntry, recursive: Boolean = false) = store.syncFiles("/in", files.toList(), recursive)

    // ---- sync ----

    @Test
    fun firstScanInsertsFilesWithIds() {
        val (files, result) = sync(file("a.jpg"), file("b.jpg", size = 20))
        assertEquals(2, result.added)
        assertTrue(files.all { it.id > 0 })
        assertEquals(2, files.map { it.id }.toSet().size)
    }

    @Test
    fun rescanKeepsIdsAndCountsChanges() {
        val first = sync(file("a.jpg"), file("b.jpg", size = 20)).first
        val (second, result) = sync(file("a.jpg"), file("b.jpg", size = 99), file("c.jpg", size = 5, mtime = 7))
        assertEquals(1, result.added)
        assertEquals(1, result.modified)
        assertEquals(0, result.removed)
        assertEquals(first.map { it.id }, second.take(2).map { it.id })
    }

    @Test
    fun vanishedFileIsMarkedMissingNotDeleted() {
        val id = sync(file("a.jpg"), file("b.jpg", size = 20)).first[1].id
        val (_, result) = sync(file("a.jpg"))
        assertEquals(1, result.removed)
        // it comes back: same row, same id
        val back = sync(file("a.jpg"), file("b.jpg", size = 20)).first
        assertEquals(id, back[1].id)
    }

    @Test
    fun renamedFileKeepsItsIdTagsAndManualValues() {
        val a = sync(file("old.jpg", size = 42, mtime = 555)).first[0]
        val tag = store.syncProfileTags("p", listOf(Tag(name = "Family")))["Family"]!!
        store.toggleTag(a.id, tag)
        store.setFileVar(a.id, "p", "event", "Beach")

        val (after, result) = sync(file("new.jpg", size = 42, mtime = 555))
        assertEquals(1, result.renamed)
        assertEquals(0, result.added)
        assertEquals(a.id, after[0].id)
        assertEquals(setOf(tag), store.loadAssignments("p")[a.id])
        assertEquals("Beach", store.loadFileVars("p")[a.id]?.get("event"))
    }

    @Test
    fun ambiguousRenameIsNotGuessed() {
        // two files vanish and two new ones have the same size and time: they cannot be told apart
        sync(file("a1.jpg", 10, 1), file("a2.jpg", 10, 1))
        val (after, result) = sync(file("b1.jpg", 10, 1), file("b2.jpg", 10, 1))
        assertEquals(0, result.renamed)
        assertEquals(2, result.added)
        assertEquals(2, result.removed)
        assertTrue(after.all { it.id > 0 })
    }

    @Test
    fun nonRecursiveSyncLeavesSubfoldersAlone() {
        sync(file("a.jpg"), recursive = false)
        val sub = FileEntry(path = "/in/sub/x.jpg", name = "x.jpg", size = 3, modTimeMs = 3)
        sync(file("a.jpg"), sub, recursive = true)
        // a flat rescan does not report the sub folder file as removed
        val (_, result) = sync(file("a.jpg"), recursive = false)
        assertEquals(0, result.removed)
    }

    // ---- session ----

    @Test
    fun sessionRoundTrips() {
        assertNull(store.loadSession())
        store.saveSession("/in", 7)
        assertEquals("/in" to 7L, store.loadSession())
        store.saveSession("/other", 9)
        assertEquals("/other" to 9L, store.loadSession())
    }

    // ---- tags ----

    @Test
    fun tagsSyncByNameAndDropRemovedOnes() {
        val ids = store.syncProfileTags("p", listOf(Tag(name = "A"), Tag(name = "B")))
        val f = sync(file("a.jpg")).first[0]
        store.toggleTag(f.id, ids["B"]!!)

        val again = store.syncProfileTags("p", listOf(Tag(name = "A", shortcut = "1"), Tag(name = "C")))
        assertEquals(ids["A"], again["A"]) // kept
        assertFalse("B" in again)
        assertNotEquals(ids["B"], again["C"])
        assertTrue(store.loadAssignments("p").isEmpty()) // B's assignment went with it
    }

    @Test
    fun toggleTagAssignsThenUnassigns() {
        val f = sync(file("a.jpg")).first[0]
        val t = store.syncProfileTags("p", listOf(Tag(name = "A")))["A"]!!
        assertTrue(store.toggleTag(f.id, t))
        assertEquals(1, store.countTaggedFiles("p"))
        assertFalse(store.toggleTag(f.id, t))
        assertEquals(0, store.countTaggedFiles("p"))
    }

    @Test
    fun profilesAreIndependentAndDeleteRemovesOnlyTheirData() {
        val f = sync(file("a.jpg")).first[0]
        val p = store.syncProfileTags("p", listOf(Tag(name = "A")))["A"]!!
        val q = store.syncProfileTags("q", listOf(Tag(name = "A")))["A"]!!
        assertNotEquals(p, q)
        store.toggleTag(f.id, p)
        store.toggleTag(f.id, q)
        store.setFileVar(f.id, "p", "x", "1")
        store.setFileVar(f.id, "q", "x", "2")

        store.deleteProfile("p")
        assertEquals(0, store.countTaggedFiles("p"))
        assertTrue(store.loadFileVars("p").isEmpty())
        assertEquals(1, store.countTaggedFiles("q"))
        assertEquals("2", store.loadFileVars("q")[f.id]?.get("x"))
    }

    // ---- manual values ----

    @Test
    fun emptyManualValueClearsIt() {
        val f = sync(file("a.jpg")).first[0]
        store.setFileVar(f.id, "p", "event", " Beach ")
        assertEquals("Beach", store.loadFileVars("p")[f.id]?.get("event"))
        store.setFileVar(f.id, "p", "event", "  ")
        assertTrue(store.loadFileVars("p").isEmpty())
    }

    @Test
    fun pruneKeepsOnlyListedVariables() {
        val f = sync(file("a.jpg")).first[0]
        store.setFileVar(f.id, "p", "keep", "1")
        store.setFileVar(f.id, "p", "drop", "2")
        store.pruneFileVars("p", listOf("keep"))
        assertEquals(mapOf("keep" to "1"), store.loadFileVars("p")[f.id])
        store.pruneFileVars("p", emptyList())
        assertTrue(store.loadFileVars("p").isEmpty())
    }

    // ---- history ----

    private fun job(f: FileEntry, tag: Tag) = CopyJob(f.id, f.path, f.name, f.size, f.modTimeMs, emptyMap(), tag)

    @Test
    fun historyRecordsBeginAndFinish() {
        val f = sync(file("a.jpg")).first[0]
        val tag = Tag(id = 1, name = "Family")
        val j = job(f, tag)
        val id = store.begin(j, "/dest/a.jpg")
        assertEquals(Store.IN_PROGRESS, store.history(f.id).single().status)
        assertEquals(0L, store.history(f.id).single().copiedAtMs)

        store.finish(id, CopyResult(j, CopyStatus.COPIED, "/dest/a.jpg", srcHash = "s", dstHash = "s"))
        val h = store.history(f.id).single()
        assertEquals("COPIED", h.status)
        assertEquals("/dest/a.jpg", h.destPath)
        assertEquals("s", h.sourceHash)
        assertTrue(h.copiedAtMs > 0)
    }

    @Test
    fun runsThatWereKilledBecomeInterruptedAndReportTheirTempFiles() {
        val f = sync(file("a.jpg")).first[0]
        val tag = Tag(id = 1, name = "Family")
        val killed = store.begin(job(f, tag), "/dest/a.jpg")
        store.note(killed, "/dest/.sortie-123.tmp") // the engine notes the bare path of its temp file
        val done = store.begin(job(f, tag), "/dest/b.jpg")
        store.finish(done, CopyResult(job(f, tag), CopyStatus.COPIED, "/dest/b.jpg"))
        val stillRunning = store.begin(job(f, tag), "/dest/c.jpg") // no note: no temp file known

        assertEquals(2, store.markInterrupted())
        assertEquals(listOf("/dest/.sortie-123.tmp"), store.interruptedTempFiles())
        val statuses = store.history(f.id).associate { it.id to it.status }
        assertEquals(Store.INTERRUPTED, statuses[killed])
        assertEquals(Store.INTERRUPTED, statuses[stillRunning])
        assertEquals("COPIED", statuses[done])
        assertEquals(0, store.markInterrupted()) // nothing left to mark
    }
}
