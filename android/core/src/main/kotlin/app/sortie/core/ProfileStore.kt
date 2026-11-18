package app.sortie.core

import java.io.File
import java.io.FileOutputStream
import java.nio.file.Files
import java.nio.file.StandardCopyOption
import java.time.LocalDateTime
import java.time.format.DateTimeFormatter

/** One profile file found in the profiles folder. */
class ProfileEntry(
    val id: String, // file name without extension; also the database key
    val file: File,
    val name: String, // display name from the file; the id if the file is invalid
    val profile: Profile?,
    val error: String?, // non-null if the file could not be loaded
)

/** The folder of profile files (`*.yaml` / `*.yml`): list, create, duplicate, rename, delete, import, export. */
class ProfileStore(val dir: File) {
    /**
     * Profiles sorted by display name. Invalid files are listed with [ProfileEntry.error] set so the
     * UI can show and fix them instead of hiding them.
     */
    fun list(): List<ProfileEntry> {
        val files = dir.listFiles()?.sortedBy { it.name } ?: return emptyList()
        val owners = HashMap<String, String>()
        val out = ArrayList<ProfileEntry>()
        for (f in files) {
            val ext = f.extension.lowercase()
            if (f.isDirectory || f.name.startsWith(".") || (ext != "yaml" && ext != "yml")) continue
            val id = f.nameWithoutExtension
            val owner = owners[id]
            if (owner != null) {
                out += ProfileEntry(id, f, id, null, "another file ($owner) already provides the profile id \"$id\"; remove or rename one of them")
                continue
            }
            owners[id] = f.name
            out += try {
                val p = ProfileYaml.parse(f.readText())
                ProfileEntry(id, f, p.name, p, null)
            } catch (e: Exception) {
                ProfileEntry(id, f, id, null, e.message ?: e.toString())
            }
        }
        return out.sortedBy { it.name.lowercase() }
    }

    fun pathFor(id: String) = File(dir, "$id.yaml")

    /** The file of an existing profile (.yaml or .yml); throws when there is none. */
    fun fileFor(id: String): File {
        validId(id)
        return listOf("yaml", "yml").map { File(dir, "$id.$it") }.firstOrNull { it.isFile }
            ?: throw SorterException("profile \"$id\" not found")
    }

    fun exists(id: String) = listOf("yaml", "yml").any { File(dir, "$id.$it").exists() }

    fun read(id: String): String = fileFor(id).readText()

    fun load(id: String): Profile = ProfileYaml.parse(read(id))

    /** Returns base, or base-2, base-3... so that no profile file with that id exists. */
    fun uniqueId(base: String): String {
        var id = base
        var n = 2
        while (exists(id)) id = "$base-${n++}"
        return id
    }

    /** Writes a starter profile named [name] and returns its id. */
    fun create(name: String, template: Profile = Profile.example(name.trim())): String {
        val n = name.trim()
        if (n.isEmpty()) throw SorterException("the profile name is empty")
        val id = uniqueId(slug(n))
        save(id, template.copy(name = n))
        return id
    }

    /** Validates and writes a profile atomically. */
    fun save(id: String, p: Profile) {
        validId(id)
        p.validate()
        writeAtomic(if (exists(id)) fileFor(id) else pathFor(id), ProfileYaml.marshal(p))
    }

    /** Replaces the text of a profile after checking that it is a valid profile. */
    fun saveText(id: String, text: String) {
        validId(id)
        ProfileYaml.parse(text)
        writeAtomic(if (exists(id)) fileFor(id) else pathFor(id), text)
    }

    /** Copies a profile file (comments included) under a new display name; returns the new id. */
    fun duplicate(fromId: String, newName: String): String {
        val n = newName.trim()
        if (n.isEmpty()) throw SorterException("the profile name is empty")
        val text = ProfileYaml.rename(read(fromId), n)
        val id = uniqueId(slug(n))
        writeAtomic(pathFor(id), text)
        return id
    }

    /** Changes the display name; the file name (id) stays. */
    fun rename(id: String, newName: String) {
        val f = fileFor(id)
        writeAtomic(f, ProfileYaml.rename(f.readText(), newName))
    }

    /** Moves the profile file into `.trash/` (nothing is erased) and returns where it went. */
    fun delete(id: String): File {
        val f = fileFor(id)
        val trash = File(dir, ".trash")
        if (!trash.isDirectory && !trash.mkdirs()) throw SorterException("cannot create ${trash.path}")
        val stamp = LocalDateTime.now().format(DateTimeFormatter.ofPattern("yyyyMMdd-HHmmss"))
        var dst = File(trash, "$id-$stamp.${f.extension}")
        var n = 2
        while (dst.exists()) dst = File(trash, "$id-$stamp-${n++}.${f.extension}")
        Files.move(f.toPath(), dst.toPath())
        return dst
    }

    /** Validates [text] and stores it as a new profile; never replaces an existing one. */
    fun import(fileName: String, text: String): String {
        try {
            ProfileYaml.parse(text)
        } catch (e: SorterException) {
            throw SorterException("$fileName: ${e.message}")
        }
        val id = uniqueId(slug(fileName.substringAfterLast('/').substringBeforeLast('.')))
        writeAtomic(pathFor(id), text)
        return id
    }

    private fun writeAtomic(target: File, text: String) {
        target.parentFile?.mkdirs()
        val tmp = File.createTempFile(".profile-", ".tmp", target.parentFile)
        try {
            FileOutputStream(tmp).use {
                it.write(text.toByteArray(Charsets.UTF_8))
                it.fd.sync() // otherwise a crash can leave an empty file after the rename
            }
            try {
                Files.move(tmp.toPath(), target.toPath(), StandardCopyOption.ATOMIC_MOVE, StandardCopyOption.REPLACE_EXISTING)
            } catch (_: java.nio.file.AtomicMoveNotSupportedException) {
                Files.move(tmp.toPath(), target.toPath(), StandardCopyOption.REPLACE_EXISTING)
            }
        } finally {
            tmp.delete()
        }
    }

    companion object {
        /** A file-name-safe id from a display name: lower-case ASCII letters and digits joined by dashes. */
        fun slug(name: String): String = PathTemplate.slug(name, unicode = false, empty = "profile")

        fun validId(id: String) {
            if (id.isBlank() || id == "." || id == ".." || id.any { it == '/' || it == '\\' || it == '\u0000' } || id.startsWith(".")) {
                throw SorterException("\"$id\" is not a valid profile name")
            }
        }
    }
}
