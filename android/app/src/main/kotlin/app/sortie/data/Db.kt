package app.sortie.data

import android.content.Context
import androidx.room.ColumnInfo
import androidx.room.Dao
import androidx.room.Database
import androidx.room.Entity
import androidx.room.Index
import androidx.room.Insert
import androidx.room.OnConflictStrategy
import androidx.room.PrimaryKey
import androidx.room.Query
import androidx.room.Room
import androidx.room.RoomDatabase

// The tables mirror the desktop Sortie's SQLite schema (inventory, per-profile tags,
// assignments and manual values, copy history, session). Times are epoch milliseconds.

@Entity(tableName = "files", indices = [Index(value = ["source_path"], unique = true)])
data class FileRowEntity(
    @PrimaryKey(autoGenerate = true) val id: Long = 0,
    @ColumnInfo(name = "source_path") val sourcePath: String,
    val filename: String,
    val extension: String,
    @ColumnInfo(name = "mime_type") val mimeType: String,
    @ColumnInfo(name = "file_size") val fileSize: Long,
    @ColumnInfo(name = "modified_time") val modifiedTime: Long,
    val status: String,
)

@Entity(tableName = "tags", indices = [Index(value = ["profile", "name"], unique = true)])
data class TagEntity(
    @PrimaryKey(autoGenerate = true) val id: Long = 0,
    val profile: String,
    val name: String,
    val shortcut: String,
    val destination: String,
    val enabled: Boolean,
    @ColumnInfo(name = "sort_order") val sortOrder: Int,
)

@Entity(tableName = "file_tags", primaryKeys = ["file_id", "tag_id"], indices = [Index("tag_id")])
data class FileTagEntity(
    @ColumnInfo(name = "file_id") val fileId: Long,
    @ColumnInfo(name = "tag_id") val tagId: Long,
    @ColumnInfo(name = "assigned_at") val assignedAt: Long,
)

@Entity(tableName = "file_vars", primaryKeys = ["file_id", "profile", "name"])
data class FileVarEntity(
    @ColumnInfo(name = "file_id") val fileId: Long,
    val profile: String,
    val name: String,
    val value: String,
)

@Entity(tableName = "copy_history", indices = [Index("file_id"), Index("status")])
data class CopyHistoryEntity(
    @PrimaryKey(autoGenerate = true) val id: Long = 0,
    @ColumnInfo(name = "file_id") val fileId: Long,
    @ColumnInfo(name = "tag_id") val tagId: Long?,
    @ColumnInfo(name = "tag_name") val tagName: String,
    @ColumnInfo(name = "source_path") val sourcePath: String,
    @ColumnInfo(name = "destination_path") val destinationPath: String,
    @ColumnInfo(name = "source_hash") val sourceHash: String?,
    @ColumnInfo(name = "destination_hash") val destinationHash: String?,
    val status: String,
    @ColumnInfo(name = "copied_at") val copiedAt: Long?,
    @ColumnInfo(name = "error_message") val errorMessage: String?,
)

@Entity(tableName = "session")
data class SessionEntity(
    @PrimaryKey val id: Int = 1,
    @ColumnInfo(name = "input_directory") val inputDirectory: String,
    @ColumnInfo(name = "current_file_id") val currentFileId: Long,
)

data class FileSyncRow(
    val id: Long,
    @ColumnInfo(name = "source_path") val sourcePath: String,
    @ColumnInfo(name = "file_size") val fileSize: Long,
    @ColumnInfo(name = "modified_time") val modifiedTime: Long,
    val status: String,
)

data class Assignment(@ColumnInfo(name = "file_id") val fileId: Long, @ColumnInfo(name = "tag_id") val tagId: Long)

@Dao
interface SorterDao {
    @Query("SELECT id, source_path, file_size, modified_time, status FROM files WHERE substr(source_path, 1, :len) = :prefix")
    fun filesUnder(prefix: String, len: Int): List<FileSyncRow>

    @Insert
    fun insertFile(f: FileRowEntity): Long

    @Query(
        "UPDATE files SET source_path = :path, filename = :name, extension = :ext, mime_type = :mime, file_size = :size, " +
            "modified_time = :mtime, status = 'PENDING' WHERE id = :id",
    )
    fun updateFile(id: Long, path: String, name: String, ext: String, mime: String, size: Long, mtime: Long)

    @Query("UPDATE files SET status = 'MISSING' WHERE id = :id")
    fun markMissing(id: Long)

    @Query("SELECT * FROM session WHERE id = 1")
    fun session(): SessionEntity?

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    fun saveSession(s: SessionEntity)

    @Query("SELECT * FROM tags WHERE profile = :profile")
    fun tags(profile: String): List<TagEntity>

    @Insert
    fun insertTag(t: TagEntity): Long

    @Query("UPDATE tags SET shortcut = :shortcut, destination = :destination, enabled = :enabled, sort_order = :sortOrder WHERE id = :id")
    fun updateTag(id: Long, shortcut: String, destination: String, enabled: Boolean, sortOrder: Int)

    @Query("DELETE FROM tags WHERE id = :id")
    fun deleteTag(id: Long)

    @Query("DELETE FROM file_tags WHERE tag_id = :tagId")
    fun deleteAssignmentsOfTag(tagId: Long)

    @Query("SELECT ft.file_id, ft.tag_id FROM file_tags ft JOIN tags t ON t.id = ft.tag_id WHERE t.profile = :profile")
    fun assignments(profile: String): List<Assignment>

    @Query("DELETE FROM file_tags WHERE file_id = :fileId AND tag_id = :tagId")
    fun unassign(fileId: Long, tagId: Long): Int

    @Insert
    fun assign(a: FileTagEntity)

    @Query("SELECT COUNT(DISTINCT ft.file_id) FROM file_tags ft JOIN tags t ON t.id = ft.tag_id WHERE t.profile = :profile")
    fun countTaggedFiles(profile: String): Int

    @Query("DELETE FROM file_tags WHERE tag_id IN (SELECT id FROM tags WHERE profile = :profile)")
    fun deleteAssignmentsOfProfile(profile: String)

    @Query("DELETE FROM tags WHERE profile = :profile")
    fun deleteTagsOfProfile(profile: String)

    @Query("DELETE FROM file_vars WHERE profile = :profile")
    fun deleteVarsOfProfile(profile: String)

    @Query("SELECT * FROM file_vars WHERE profile = :profile")
    fun fileVars(profile: String): List<FileVarEntity>

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    fun setFileVar(v: FileVarEntity)

    @Query("DELETE FROM file_vars WHERE file_id = :fileId AND profile = :profile AND name = :name")
    fun deleteFileVar(fileId: Long, profile: String, name: String)

    @Query("DELETE FROM file_vars WHERE profile = :profile AND name NOT IN (:keep)")
    fun pruneFileVars(profile: String, keep: List<String>)

    @Insert
    fun insertHistory(h: CopyHistoryEntity): Long

    @Query("UPDATE copy_history SET error_message = :note WHERE id = :id")
    fun noteHistory(id: Long, note: String)

    @Query(
        "UPDATE copy_history SET status = :status, source_hash = :srcHash, destination_hash = :dstHash, " +
            "error_message = :message, copied_at = :at WHERE id = :id",
    )
    fun finishHistory(id: Long, status: String, srcHash: String?, dstHash: String?, message: String?, at: Long)

    @Query(
        "UPDATE copy_history SET status = 'INTERRUPTED', error_message = 'interrupted' || " +
            "CASE WHEN error_message IS NULL OR error_message = '' THEN '' ELSE ' (temporary file: ' || error_message || ')' END " +
            "WHERE status = 'IN_PROGRESS'",
    )
    fun markInterrupted(): Int

    @Query("SELECT error_message FROM copy_history WHERE status = 'INTERRUPTED' AND error_message LIKE '%(temporary file: %)'")
    fun interruptedMessages(): List<String>

    @Query("SELECT * FROM copy_history WHERE file_id = :fileId ORDER BY id DESC")
    fun history(fileId: Long): List<CopyHistoryEntity>
}

@Database(
    entities = [FileRowEntity::class, TagEntity::class, FileTagEntity::class, FileVarEntity::class, CopyHistoryEntity::class, SessionEntity::class],
    version = 1,
    exportSchema = true,
)
abstract class AppDatabase : RoomDatabase() {
    abstract fun dao(): SorterDao

    companion object {
        fun open(context: Context): AppDatabase =
            Room.databaseBuilder(context, AppDatabase::class.java, "sortie.db").build()

        fun inMemory(context: Context): AppDatabase =
            Room.inMemoryDatabaseBuilder(context, AppDatabase::class.java).allowMainThreadQueries().build()
    }
}
