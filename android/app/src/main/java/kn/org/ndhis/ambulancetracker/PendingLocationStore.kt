package kn.org.ndhis.ambulancetracker

import android.content.ContentValues
import android.content.Context
import android.database.sqlite.SQLiteDatabase
import android.database.sqlite.SQLiteOpenHelper

data class PendingLocation(
    val id: Long,
    val sessionId: String,
    val sequenceNumber: Long,
    val payload: String,
)

class PendingLocationStore(context: Context) : SQLiteOpenHelper(context, "pending_locations.db", null, 1) {
    override fun onCreate(db: SQLiteDatabase) {
        db.execSQL(
            """
            CREATE TABLE pending_locations (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                session_id TEXT NOT NULL,
                sequence_number INTEGER NOT NULL,
                payload TEXT NOT NULL,
                created_at INTEGER NOT NULL,
                UNIQUE(session_id, sequence_number)
            )
            """.trimIndent(),
        )
        db.execSQL("CREATE INDEX pending_locations_created_idx ON pending_locations(created_at)")
    }

    override fun onUpgrade(db: SQLiteDatabase, oldVersion: Int, newVersion: Int) = Unit

    fun enqueue(sessionId: String, sequenceNumber: Long, payload: String) {
        val values = ContentValues().apply {
            put("session_id", sessionId)
            put("sequence_number", sequenceNumber)
            put("payload", payload)
            put("created_at", System.currentTimeMillis())
        }
        writableDatabase.insertWithOnConflict(
            "pending_locations",
            null,
            values,
            SQLiteDatabase.CONFLICT_IGNORE,
        )
    }

    fun list(limit: Int = 200): List<PendingLocation> {
        val rows = mutableListOf<PendingLocation>()
        readableDatabase.query(
            "pending_locations",
            arrayOf("id", "session_id", "sequence_number", "payload"),
            null,
            null,
            null,
            null,
            "id ASC",
            limit.coerceIn(1, 500).toString(),
        ).use { cursor ->
            while (cursor.moveToNext()) {
                rows += PendingLocation(
                    id = cursor.getLong(0),
                    sessionId = cursor.getString(1),
                    sequenceNumber = cursor.getLong(2),
                    payload = cursor.getString(3),
                )
            }
        }
        return rows
    }

    fun delete(sessionId: String, sequenceNumber: Long) {
        writableDatabase.delete(
            "pending_locations",
            "session_id=? AND sequence_number=?",
            arrayOf(sessionId, sequenceNumber.toString()),
        )
    }

    fun deleteIds(ids: List<Long>) {
        if (ids.isEmpty()) return
        val placeholders = ids.joinToString(",") { "?" }
        writableDatabase.delete(
            "pending_locations",
            "id IN ($placeholders)",
            ids.map(Long::toString).toTypedArray(),
        )
    }

    fun count(): Long = readableDatabase.rawQuery("SELECT count(*) FROM pending_locations", null).use { cursor ->
        cursor.moveToFirst()
        cursor.getLong(0)
    }

    fun trimOlderThan(days: Int = 7) {
        val cutoff = System.currentTimeMillis() - days.coerceAtLeast(1) * 24L * 60L * 60L * 1000L
        writableDatabase.delete("pending_locations", "created_at < ?", arrayOf(cutoff.toString()))
    }
}
