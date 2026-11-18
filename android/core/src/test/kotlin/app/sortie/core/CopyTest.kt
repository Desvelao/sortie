package app.sortie.core

import java.io.File
import java.nio.file.Files
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class CopyTest {
    private val tmp = Files.createTempDirectory("copytest").toFile()
    private val src = File(tmp, "src").apply { mkdirs() }
    private val root = File(tmp, "dst").apply { mkdirs() }
    private val store = MemoryStore()
    private val checker = Checker()
    private val engine = CopyEngine(checker, store)
    private val resolver = Resolver(root.path, "{year}/{tag}/{filename}", listOf("year"))
    private val work = Tag(1, "Work")

    @AfterTest
    fun cleanup() {
        tmp.deleteRecursively()
    }

    private fun file(name: String, content: String): File = File(src, name).apply { writeText(content) }

    private fun job(f: File, tag: Tag = work, year: String? = "2020", id: Long = 1) =
        CopyJob(id, f.path, f.name, f.length(), f.lastModified(), if (year == null) emptyMap() else mapOf("year" to year), tag)

    private fun run(jobs: List<CopyJob>, policy: ConflictPolicy = ConflictPolicy.SKIP, confirm: Boolean = false, cancel: Cancel = Cancel()) =
        engine.run(resolver, jobs, CopyOptions(policy, confirm), cancel)

    private fun leftovers() = root.walkTopDown().filter { CopyEngine.isTempFile(it.path) }.toList()

    @Test
    fun resolvePaths() {
        assertEquals(File(root, "2020/Work/a.txt").path, resolver.resolve("a.txt", mapOf("year" to "2020"), work))
        assertEquals(File(root, "2020/Work/Inv/a.txt").path, resolver.resolve("a.txt", mapOf("year" to "2020"), work.copy(destination = "Work\\Inv")))
        assertEquals(File(root, "Own/a-2020.txt").path, resolver.resolve("a.txt", mapOf("year" to "2020"), work.copy(destinationTemplate = "Own/{name}-{year}{ext}")))
        assertEquals(File(root, "2020/Work/.bashrc").path, resolver.resolve(".bashrc", mapOf("year" to "2020"), work))
        assertFailsWith<UnresolvedException> { resolver.resolve("a.txt", emptyMap(), work) }
        assertFailsWith<SorterException> { resolver.resolve("a/b", mapOf("year" to "2020"), work) }
        assertFailsWith<SorterException> { resolver.resolve("a.txt", mapOf("year" to "2020"), work.copy(destination = "../x")) }
        assertFailsWith<SorterException> { Resolver("", "{tag}/{filename}").resolve("a.txt", emptyMap(), work) }
        assertFailsWith<SorterException> { Resolver("relative", "{tag}/{filename}").resolve("a.txt", emptyMap(), work) }
    }

    @Test
    fun symlinkOutsideRootIsRejected() {
        val outside = File(tmp, "outside").apply { mkdirs() }
        Files.createSymbolicLink(File(root, "2020").toPath(), outside.toPath())
        val e = assertFailsWith<SorterException> { resolver.resolve("a.txt", mapOf("year" to "2020"), work) }
        assertTrue(e.message!!.contains("symbolic link"))
    }

    @Test
    fun checkStatuses() {
        val f = file("a.txt", "hello")
        fun check(tag: Tag = work, vars: Map<String, String> = mapOf("year" to "2020")) =
            checker.check(resolver, f.path, f.name, vars, listOf(tag), Cancel())[0]
        assertEquals(DestStatus.READY, check().status)
        assertEquals(DestStatus.UNRESOLVED_VAR, check(vars = emptyMap()).status)
        assertEquals(DestStatus.INVALID_PATH, check(work.copy(destination = "../x")).status)
        val dst = File(root, "2020/Work/a.txt").apply { parentFile.mkdirs() }
        dst.writeText("hello")
        check().let {
            assertEquals(DestStatus.ALREADY_COPIED, it.status)
            assertEquals(64, it.srcHash.length)
        }
        dst.writeText("HELLO")
        dst.setLastModified(dst.lastModified() + 5000) // hashes are cached per (path, size, mtime)
        assertEquals(DestStatus.CONFLICT, check().status)
        dst.writeText("longer content")
        assertEquals(DestStatus.CONFLICT, check().status)
        dst.delete()
        dst.mkdirs()
        assertEquals(DestStatus.CONFLICT, check().status)
        dst.delete()
        File(root, "2020/Work").deleteRecursively()
        File(root, "2020/Work").writeText("a file where a folder should be")
        assertEquals(DestStatus.INVALID_PATH, check().status)
        // the destination is the source itself
        val self = Resolver(src.path, "{filename}")
        assertEquals(DestStatus.INVALID_PATH, checker.check(self, f.path, f.name, emptyMap(), listOf(work), Cancel())[0].status)
    }

    @Test
    fun copiesVerifiesAndResumes() {
        val f = file("a.txt", "hello world")
        val events = ArrayList<CopyEvent>()
        val sum = engine.run(resolver, listOf(job(f)), CopyOptions(onEvent = { events += it }), Cancel())
        val r = sum.results[0]
        assertEquals(CopyStatus.COPIED, r.status)
        val dst = File(root, "2020/Work/a.txt")
        assertEquals("hello world", dst.readText())
        assertEquals(Hasher.sha256(f, Cancel()), r.dstHash)
        assertEquals(11, r.bytes)
        assertEquals(f.lastModified(), dst.lastModified())
        assertEquals("hello world", f.readText(), "the source is never modified")
        assertTrue(events.first() is CopyEvent.Start && events.last() is CopyEvent.Done)
        assertEquals(1, (events.last() as CopyEvent.Done).done)
        assertTrue(leftovers().isEmpty())
        assertEquals(listOf("COPIED"), store.history(1).map { it.status })
        // running again resumes: nothing is written twice
        assertEquals(CopyStatus.ALREADY_COPIED, run(listOf(job(f))).results[0].status)
    }

    @Test
    fun conflictPolicies() {
        val f = file("a.txt", "new content")
        val dst = File(root, "2020/Work/a.txt").apply { parentFile.mkdirs(); writeText("old") }

        assertEquals(CopyStatus.CONFLICT_SKIPPED, run(listOf(job(f))).results[0].status)
        assertEquals("old", dst.readText())

        val renamed = run(listOf(job(f)), ConflictPolicy.RENAME).results[0]
        assertEquals(CopyStatus.RENAMED, renamed.status)
        assertEquals(File(root, "2020/Work/a (1).txt").path, renamed.destPath)
        assertEquals("old", dst.readText())
        // the identical alternative from the previous run is reused
        val again = run(listOf(job(f)), ConflictPolicy.RENAME).results[0]
        assertEquals(CopyStatus.ALREADY_COPIED, again.status)
        assertEquals(renamed.destPath, again.destPath)
        assertFalse(File(root, "2020/Work/a (2).txt").exists())

        assertFailsWith<SorterException> { run(listOf(job(f)), ConflictPolicy.REPLACE) }
        assertEquals("old", dst.readText())
        assertEquals(CopyStatus.REPLACED, run(listOf(job(f)), ConflictPolicy.REPLACE, confirm = true).results[0].status)
        assertEquals("new content", dst.readText())
        assertTrue(leftovers().isEmpty())
    }

    @Test
    fun replaceNeverOverwritesAFileOfTheSameBatch() {
        val a = file("a.txt", "first")
        val other = File(src, "sub").apply { mkdirs() }
        val b = File(other, "a.txt").apply { writeText("second!") }
        val jobs = listOf(job(a, id = 1), job(b, id = 2))
        val plan = engine.plan(resolver, jobs, Cancel())
        assertEquals(listOf(DestStatus.READY, DestStatus.READY), plan.map { it.status })
        assertTrue(plan[1].detail.contains("same destination"))
        val sum = engine.run(resolver, jobs, CopyOptions(ConflictPolicy.REPLACE, confirmReplace = true, workers = 1), Cancel())
        assertEquals(listOf(CopyStatus.COPIED, CopyStatus.CONFLICT_SKIPPED), sum.results.map { it.status })
        assertEquals("first", File(root, "2020/Work/a.txt").readText())
        assertTrue(leftovers().isEmpty())
    }

    @Test
    fun invalidJobsAreSkippedAndBatchContinues() {
        val a = file("a.txt", "a")
        val b = file("b.txt", "b")
        val gone = file("gone.txt", "x")
        val goneJob = job(gone, id = 3)
        gone.delete()
        val sum = run(listOf(job(a, year = null), job(b, id = 2), goneJob))
        assertEquals(listOf(CopyStatus.SKIPPED_INVALID, CopyStatus.COPIED, CopyStatus.FAILED), sum.results.map { it.status })
        assertEquals(1, sum.count(CopyStatus.COPIED))
        assertEquals(1, sum.bytes)
    }

    @Test
    fun sourceChangedSinceScanFails() {
        val f = file("a.txt", "hello")
        val j = job(f)
        f.writeText("hello, changed")
        val r = run(listOf(j)).results[0]
        assertEquals(CopyStatus.FAILED, r.status)
        assertTrue(r.detail.contains("changed since it was scanned"))
        assertFalse(File(root, "2020/Work/a.txt").exists())
    }

    @Test
    fun cancelledRunWritesNothing() {
        val f = file("a.txt", "hello")
        val cancel = Cancel().apply { cancel() }
        val sum = run(listOf(job(f)), cancel = cancel)
        assertEquals(CopyStatus.CANCELLED, sum.results[0].status)
        assertFalse(File(root, "2020/Work/a.txt").exists())
        assertTrue(leftovers().isEmpty())
    }

    @Test
    fun cancelDuringCopyRemovesTheTemporaryFile() {
        val big = File(src, "big.bin").apply { writeBytes(ByteArray(8 * Hasher.CHUNK) { it.toByte() }) }
        val cancel = Cancel()
        store.onNote = { cancel.cancel() } // the temporary file exists now
        val sum = engine.run(resolver, listOf(job(big)), CopyOptions(), cancel)
        assertEquals(CopyStatus.CANCELLED, sum.results[0].status)
        assertFalse(File(root, "2020/Work/big.bin").exists())
        assertTrue(leftovers().isEmpty())
        assertEquals(listOf("CANCELLED"), store.history(1).map { it.status })
    }

    @Test
    fun missingRootStopsTheRun() {
        val f = file("a.txt", "x")
        assertFailsWith<SorterException> { engine.run(Resolver(File(tmp, "nope").path), listOf(job(f)), CopyOptions(), Cancel()) }
        assertFailsWith<SorterException> { engine.run(Resolver(""), listOf(job(f)), CopyOptions(), Cancel()) }
    }

    @Test
    fun allBlockedOnlyWhenEveryCheckIsAnInvalidPath() {
        fun c(s: DestStatus) = DestCheck(status = s)
        assertFalse(emptyList<DestCheck>().allBlocked())
        assertTrue(listOf(c(DestStatus.INVALID_PATH), c(DestStatus.INVALID_PATH)).allBlocked())
        assertFalse(listOf(c(DestStatus.INVALID_PATH), c(DestStatus.READY)).allBlocked())
        assertFalse(listOf(c(DestStatus.INVALID_PATH), c(DestStatus.CONFLICT)).allBlocked()) // the policy can still replace it
        assertTrue(listOf(c(DestStatus.UNRESOLVED_VAR)).allBlocked())
        assertTrue(listOf(c(DestStatus.INVALID_PATH), c(DestStatus.UNRESOLVED_VAR)).allBlocked())
        assertFalse(listOf(c(DestStatus.UNRESOLVED_VAR), c(DestStatus.READY)).allBlocked())
        assertFalse(listOf(c(DestStatus.ERROR)).allBlocked()) // may be temporary
    }

    /** A destination the pre-copy check calls ready must not then fail on its root: same rule, same message. */
    @Test
    fun checkAgreesWithTheCopyOnTheRoot() {
        val f = file("a.txt", "x")
        val notDir = File(tmp, "afile").apply { writeText("i am a file") }
        val cases = listOf(
            Resolver(File(tmp, "nope").path, "{year}/{tag}/{filename}", listOf("year")) to "not available",
            Resolver(notDir.path, "{year}/{tag}/{filename}", listOf("year")) to "not a directory",
            Resolver("", "{year}/{tag}/{filename}", listOf("year")) to "not configured",
        )
        for ((r, expected) in cases) {
            val chk = checker.check(r, f.path, f.name, mapOf("year" to "2020"), listOf(work), Cancel())[0]
            assertEquals(DestStatus.INVALID_PATH, chk.status, r.root)
            assertFalse(chk.canCopy)
            assertTrue(expected in chk.detail, chk.detail)
            val e = assertFailsWith<SorterException> { engine.run(r, listOf(job(f)), CopyOptions(), Cancel()) }
            assertEquals(e.message, chk.detail)
        }
        assertEquals(DestStatus.READY, checker.check(resolver, f.path, f.name, mapOf("year" to "2020"), listOf(work), Cancel())[0].status)
    }

    @Test
    fun noReplaceMoveAndAlternativeNames() {
        val a = file("m1", "one")
        val b = File(root, "target").apply { writeText("two") }
        assertFalse(CopyEngine.moveNoReplace(a, b))
        assertEquals("two", b.readText())
        assertTrue(a.exists())
        assertTrue(CopyEngine.moveNoReplace(a, File(root, "free")))
        assertFalse(a.exists())
        assertEquals("/d/photo (2).jpg", CopyEngine.alternativeName("/d/photo.jpg", 2))
        assertEquals("/d/backup (1).tar.gz", CopyEngine.alternativeName("/d/backup.tar.gz", 1))
        assertEquals("/d/.bashrc (1)", CopyEngine.alternativeName("/d/.bashrc", 1))
        assertTrue(CopyEngine.isTempFile("/x/.sortie-123.tmp"))
        assertFailsWith<SorterException> { CopyEngine.removeTempFile("/etc/passwd") }
    }

    @Test
    fun interruptedCopiesAreReported() {
        val f = file("a.txt", "x")
        val id = store.begin(job(f), File(root, "2020/Work/a.txt").path)
        val temp = File(root, ".sortie-1.tmp").apply { writeText("partial") }
        store.note(id, temp.path)
        val sorter = Sorter(store, tmp.path)
        assertEquals(1, sorter.interruptedAtStart)
        assertEquals(listOf(temp.path), sorter.leftoverTempFiles())
        assertEquals(1, sorter.removeLeftoverTempFiles())
        assertFalse(temp.exists())
    }
}
