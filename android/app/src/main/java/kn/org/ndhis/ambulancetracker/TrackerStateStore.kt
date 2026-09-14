package kn.org.ndhis.ambulancetracker

import android.content.Context
import android.location.Location

data class TrackerUiSnapshot(
    val status: String,
    val active: Boolean,
    val updatedAtMs: Long,
    val network: String,
    val buffered: Long,
    val battery: Int?,
    val speedMps: Float?,
    val accuracyM: Float?,
    val latitude: Double?,
    val longitude: Double?,
)

object TrackerStateStore {
    private const val PREFS = "tracker_runtime_state"

    private const val KEY_STATUS = "status"
    private const val KEY_ACTIVE = "active"
    private const val KEY_UPDATED_AT = "updated_at"
    private const val KEY_NETWORK = "network"
    private const val KEY_BUFFERED = "buffered"
    private const val KEY_BATTERY = "battery"
    private const val KEY_SPEED = "speed"
    private const val KEY_ACCURACY = "accuracy"
    private const val KEY_LATITUDE = "latitude"
    private const val KEY_LONGITUDE = "longitude"

    fun save(
        context: Context,
        status: String,
        active: Boolean,
        network: String,
        buffered: Long,
        battery: Int?,
        location: Location?,
    ) {
        val prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        val editor = prefs.edit()
            .putString(KEY_STATUS, status)
            .putBoolean(KEY_ACTIVE, active)
            .putLong(KEY_UPDATED_AT, System.currentTimeMillis())
            .putString(KEY_NETWORK, network)
            .putLong(KEY_BUFFERED, buffered)

        if (battery != null) editor.putInt(KEY_BATTERY, battery) else editor.remove(KEY_BATTERY)
        if (location != null) {
            editor.putLong(KEY_LATITUDE, location.latitude.toBits())
            editor.putLong(KEY_LONGITUDE, location.longitude.toBits())
            if (location.hasSpeed()) editor.putInt(KEY_SPEED, location.speed.toBits()) else editor.remove(KEY_SPEED)
            if (location.hasAccuracy()) editor.putInt(KEY_ACCURACY, location.accuracy.toBits()) else editor.remove(KEY_ACCURACY)
        }
        editor.apply()
    }

    fun markStopped(context: Context) {
        val previous = load(context)
        context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit()
            .putString(KEY_STATUS, "Stopped")
            .putBoolean(KEY_ACTIVE, false)
            .putLong(KEY_UPDATED_AT, System.currentTimeMillis())
            .putString(KEY_NETWORK, previous?.network ?: "NONE")
            .putLong(KEY_BUFFERED, previous?.buffered ?: 0L)
            .apply()
    }

    fun load(context: Context): TrackerUiSnapshot? {
        val prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        if (!prefs.contains(KEY_UPDATED_AT)) return null

        return TrackerUiSnapshot(
            status = prefs.getString(KEY_STATUS, "Stopped") ?: "Stopped",
            active = prefs.getBoolean(KEY_ACTIVE, false),
            updatedAtMs = prefs.getLong(KEY_UPDATED_AT, 0L),
            network = prefs.getString(KEY_NETWORK, "NONE") ?: "NONE",
            buffered = prefs.getLong(KEY_BUFFERED, 0L),
            battery = if (prefs.contains(KEY_BATTERY)) prefs.getInt(KEY_BATTERY, 0) else null,
            speedMps = if (prefs.contains(KEY_SPEED)) Float.fromBits(prefs.getInt(KEY_SPEED, 0)) else null,
            accuracyM = if (prefs.contains(KEY_ACCURACY)) Float.fromBits(prefs.getInt(KEY_ACCURACY, 0)) else null,
            latitude = if (prefs.contains(KEY_LATITUDE)) Double.fromBits(prefs.getLong(KEY_LATITUDE, 0L)) else null,
            longitude = if (prefs.contains(KEY_LONGITUDE)) Double.fromBits(prefs.getLong(KEY_LONGITUDE, 0L)) else null,
        )
    }
}
