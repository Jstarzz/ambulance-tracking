package kn.org.ndhis.ambulancetracker

import android.app.Activity
import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.Paint
import android.os.Handler
import android.os.Looper
import okhttp3.Call
import okhttp3.Callback
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import org.json.JSONObject
import org.maplibre.android.annotations.Icon
import org.maplibre.android.annotations.IconFactory
import org.maplibre.android.annotations.Marker
import org.maplibre.android.annotations.MarkerOptions
import org.maplibre.android.geometry.LatLng
import org.maplibre.android.maps.MapLibreMap
import java.io.IOException
import java.time.Duration
import java.time.Instant
import kotlin.math.roundToInt

/**
 * Foreground-only peer ambulance overlay for the tracker map.
 *
 * It deliberately uses the normal short-lived device session instead of exposing
 * dispatcher credentials to the phone. Polling stops with the activity, so this
 * does not create background radio/battery churn while the tracker service keeps
 * doing its independent location upload work.
 */
class FleetMapPeers(
    private val activity: Activity,
    private val client: OkHttpClient,
    private val mapProvider: () -> MapLibreMap?,
) {
    companion object {
        private const val REFRESH_MS = 5_000L
        private const val AUTH_RETRY_MS = 30_000L
        private const val MAX_PEER_AGE_SECONDS = 15 * 60L
    }

    private val handler = Handler(Looper.getMainLooper())
    private val markers = mutableMapOf<String, Marker>()
    private var running = false
    private var requestInFlight = false
    private var accessToken: String? = null
    private var authRetryAtMs = 0L

    private val refreshRunnable = object : Runnable {
        override fun run() {
            if (!running) return
            refreshNow()
            handler.postDelayed(this, REFRESH_MS)
        }
    }

    fun start() {
        if (running) return
        running = true
        handler.removeCallbacks(refreshRunnable)
        refreshNow()
        handler.postDelayed(refreshRunnable, REFRESH_MS)
    }

    fun stop() {
        running = false
        handler.removeCallbacks(refreshRunnable)
    }

    fun close() {
        stop()
        clearMarkers()
        accessToken = null
    }

    fun resetAuth() {
        accessToken = null
        authRetryAtMs = 0L
        clearMarkers()
        if (running) refreshNow()
    }

    fun onMapStyleReloaded() {
        // Style replacement drops annotation backing data. Forget the old handles
        // and repopulate them from the next fleet snapshot.
        markers.clear()
        if (running) refreshNow()
    }

    fun refreshNow() {
        if (requestInFlight || System.currentTimeMillis() < authRetryAtMs) return
        val config = SecureConfig.load(activity) ?: run {
            clearMarkers()
            return
        }
        val token = accessToken
        if (token.isNullOrBlank()) {
            authenticate(config)
        } else {
            fetchFleet(config, token)
        }
    }

    private fun authenticate(config: TrackerConfig) {
        requestInFlight = true
        val body = JSONObject()
            .put("vehicle_code", config.vehicleCode)
            .put("device_key", config.deviceKey)
            .toString()
            .toRequestBody("application/json".toMediaType())
        val request = Request.Builder()
            .url("${config.serverUrl}/api/v1/device/session")
            .post(body)
            .build()

        client.newCall(request).enqueue(object : Callback {
            override fun onFailure(call: Call, e: IOException) {
                activity.runOnUiThread {
                    requestInFlight = false
                    authRetryAtMs = System.currentTimeMillis() + AUTH_RETRY_MS
                }
            }

            override fun onResponse(call: Call, response: Response) {
                response.use {
                    val bodyText = response.body?.string().orEmpty()
                    val token = if (response.isSuccessful) {
                        runCatching { JSONObject(bodyText).optString("access_token") }.getOrNull()
                    } else {
                        null
                    }
                    activity.runOnUiThread {
                        requestInFlight = false
                        if (token.isNullOrBlank()) {
                            accessToken = null
                            authRetryAtMs = System.currentTimeMillis() + AUTH_RETRY_MS
                        } else {
                            accessToken = token
                            authRetryAtMs = 0L
                            if (running) fetchFleet(config, token)
                        }
                    }
                }
            }
        })
    }

    private fun fetchFleet(config: TrackerConfig, token: String) {
        if (requestInFlight) return
        requestInFlight = true
        val request = Request.Builder()
            .url("${config.serverUrl}/api/v1/device/fleet")
            .header("Authorization", "Bearer $token")
            .get()
            .build()

        client.newCall(request).enqueue(object : Callback {
            override fun onFailure(call: Call, e: IOException) {
                activity.runOnUiThread { requestInFlight = false }
            }

            override fun onResponse(call: Call, response: Response) {
                response.use {
                    val status = response.code
                    val bodyText = response.body?.string().orEmpty()
                    val payload = if (response.isSuccessful) {
                        runCatching { JSONObject(bodyText) }.getOrNull()
                    } else {
                        null
                    }
                    activity.runOnUiThread {
                        requestInFlight = false
                        if (status == 401) {
                            accessToken = null
                            return@runOnUiThread
                        }
                        if (payload != null) renderFleet(payload, config.vehicleCode)
                    }
                }
            }
        })
    }

    private fun renderFleet(payload: JSONObject, selfVehicleCode: String) {
        val readyMap = mapProvider() ?: return
        val vehicles = payload.optJSONArray("vehicles") ?: return
        val visible = mutableSetOf<String>()
        val now = Instant.now()

        for (index in 0 until vehicles.length()) {
            val vehicle = vehicles.optJSONObject(index) ?: continue
            val vehicleId = vehicle.optString("vehicle_id")
            val code = vehicle.optString("vehicle_code")
            if (vehicleId.isBlank() || code.isBlank() || code == selfVehicleCode) continue

            val location = vehicle.optJSONObject("location") ?: continue
            val latitude = location.optDouble("latitude", Double.NaN)
            val longitude = location.optDouble("longitude", Double.NaN)
            if (!latitude.isFinite() || !longitude.isFinite()) continue

            val recordedAt = runCatching { Instant.parse(location.optString("recorded_at")) }.getOrNull()
            val ageSeconds = recordedAt?.let { Duration.between(it, now).seconds.coerceAtLeast(0L) }
            if (ageSeconds != null && ageSeconds > MAX_PEER_AGE_SECONDS) continue

            visible += vehicleId
            val point = LatLng(latitude, longitude)
            val label = vehicle.optString("label").takeIf { it.isNotBlank() && it != code }
            val subtitle = buildString {
                if (label != null) append(label)
                if (ageSeconds != null) {
                    if (isNotEmpty()) append(" · ")
                    append(if (ageSeconds < 5) "Live" else "${ageSeconds}s ago")
                }
            }.ifBlank { "Other ambulance" }

            val existing = markers[vehicleId]
            if (existing == null) {
                markers[vehicleId] = readyMap.addMarker(
                    MarkerOptions()
                        .position(point)
                        .icon(createPeerIcon())
                        .title(code)
                        .snippet(subtitle),
                )
            } else {
                existing.position = point
                existing.title = code
                existing.snippet = subtitle
                readyMap.updateMarker(existing)
            }
        }

        val removed = markers.keys.filterNot { it in visible }
        for (vehicleId in removed) {
            markers.remove(vehicleId)?.let { readyMap.removeMarker(it) }
        }
    }

    private fun clearMarkers() {
        val readyMap = mapProvider()
        if (readyMap != null) {
            markers.values.forEach { marker -> runCatching { readyMap.removeMarker(marker) } }
        }
        markers.clear()
    }

    private fun createPeerIcon(): Icon {
        val density = activity.resources.displayMetrics.density
        val size = (18f * density).roundToInt().coerceAtLeast(18)
        val outline = (2f * density).coerceAtLeast(2f)
        val radius = size / 2f - outline
        val bitmap = Bitmap.createBitmap(size, size, Bitmap.Config.ARGB_8888)
        val canvas = Canvas(bitmap)
        val paint = Paint(Paint.ANTI_ALIAS_FLAG)

        paint.color = activity.getColor(R.color.app_panel)
        canvas.drawCircle(size / 2f, size / 2f, radius + outline, paint)
        paint.color = activity.getColor(R.color.app_green)
        canvas.drawCircle(size / 2f, size / 2f, radius, paint)

        return IconFactory.getInstance(activity).fromBitmap(bitmap)
    }
}
