package kn.org.ndhis.ambulancetracker

import android.Manifest
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.content.pm.PackageManager
import android.location.Location
import android.location.LocationListener
import android.location.LocationManager
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.os.BatteryManager
import android.os.Handler
import android.os.HandlerThread
import android.os.IBinder
import android.os.Looper
import android.os.SystemClock
import okhttp3.Call
import okhttp3.Callback
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import org.json.JSONArray
import org.json.JSONObject
import java.io.IOException
import java.time.Instant
import java.util.UUID
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import kotlin.math.max
import kotlin.math.min

class TrackingService : Service(), LocationListener {
    companion object {
        const val ACTION_START = "kn.org.ndhis.ambulancetracker.START"
        const val ACTION_STOP = "kn.org.ndhis.ambulancetracker.STOP"
        const val ACTION_STATUS = "kn.org.ndhis.ambulancetracker.STATUS"
        const val ACTION_TELEMETRY = "kn.org.ndhis.ambulancetracker.TELEMETRY"

        const val EXTRA_STATUS = "status"
        const val EXTRA_SPEED_MPS = "speed_mps"
        const val EXTRA_ACCURACY_M = "accuracy_m"
        const val EXTRA_LATITUDE = "latitude"
        const val EXTRA_LONGITUDE = "longitude"
        const val EXTRA_NETWORK = "network"
        const val EXTRA_BUFFERED = "buffered"
        const val EXTRA_BATTERY = "battery"

        private const val NOTIFICATION_CHANNEL = "tracking"
        private const val NOTIFICATION_ID = 1001

        // Vehicle tracking should follow real movement without drawing the normal
        // 10-30 m GNSS wander seen while a phone is stationary.
        private const val MIN_FIX_INTERVAL_MS = 800L
        private const val STATIONARY_HEARTBEAT_MS = 15_000L
        private const val MAX_ACCEPTABLE_ACCURACY_M = 50f
        private const val STATIONARY_SPEED_MPS = 1.5f
        private const val MIN_STATIONARY_RADIUS_M = 8f
        private const val MAX_STATIONARY_RADIUS_M = 30f
        private const val MAX_PLAUSIBLE_SPEED_MPS = 70f
    }

    private val client = OkHttpClient.Builder()
        .connectTimeout(10, TimeUnit.SECONDS)
        .readTimeout(20, TimeUnit.SECONDS)
        .writeTimeout(10, TimeUnit.SECONDS)
        .pingInterval(20, TimeUnit.SECONDS)
        .build()

    private val ioExecutor = Executors.newSingleThreadExecutor()
    private val mainHandler = Handler(Looper.getMainLooper())
    private lateinit var locationThread: HandlerThread
    private lateinit var locationManager: LocationManager
    private lateinit var pendingStore: PendingLocationStore

    private val trackingSessionId = UUID.randomUUID().toString()
    private var sequenceNumber = 0L
    private var lastAcceptedElapsedMs = 0L
    private var lastAcceptedLocation: Location? = null
    private var reconnectDelayMs = 1_000L
    private var reconnectScheduled = false
    private var started = false
    private var userRequestedStop = false
    private var lastStatus = "Starting"

    @Volatile private var config: TrackerConfig? = null
    @Volatile private var accessToken: String? = null
    @Volatile private var socket: WebSocket? = null
    @Volatile private var connecting = false

    override fun onCreate() {
        super.onCreate()
        pendingStore = PendingLocationStore(applicationContext)
        pendingStore.trimOlderThan(7)
        locationManager = getSystemService(LocationManager::class.java)
        locationThread = HandlerThread("ambulance-location").apply { start() }
        createNotificationChannel()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_STOP -> {
                userRequestedStop = true
                started = false
                publishTelemetry("Stopped", lastAcceptedLocation, notify = false)
                stopForeground(STOP_FOREGROUND_REMOVE)
                stopSelf()
                return START_NOT_STICKY
            }

            ACTION_STATUS -> {
                if (started) {
                    publishTelemetry(lastStatus, lastAcceptedLocation)
                } else {
                    publishTelemetry("Stopped", lastAcceptedLocation, notify = false)
                    stopSelf(startId)
                }
                return if (started) START_STICKY else START_NOT_STICKY
            }
        }

        userRequestedStop = false
        startForeground(NOTIFICATION_ID, buildNotification("Starting tracking"))
        if (!started) {
            val loaded = SecureConfig.load(applicationContext)
            if (loaded == null) {
                publishTelemetry("Configuration unavailable")
                stopSelf()
                return START_NOT_STICKY
            }
            config = loaded
            started = true
            startLocationUpdates()
            authenticateAndConnect()
            publishTelemetry("Acquiring GPS")
        } else {
            publishTelemetry(lastStatus, lastAcceptedLocation)
        }
        return START_STICKY
    }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onLocationChanged(location: Location) {
        val nowElapsed = SystemClock.elapsedRealtime()
        if (nowElapsed - lastAcceptedElapsedMs < MIN_FIX_INTERVAL_MS) return
        if (location.hasAccuracy() && location.accuracy > MAX_ACCEPTABLE_ACCURACY_M) return

        val accepted = stabilizeLocation(location, nowElapsed) ?: return
        lastAcceptedElapsedMs = nowElapsed
        lastAcceptedLocation = Location(accepted)

        val sequence = sequenceNumber++
        ioExecutor.execute {
            val payload = buildLocationPayload(accepted, sequence)
            pendingStore.enqueue(trackingSessionId, sequence, payload.toString())
            socket?.send(payload.toString())
            publishTelemetry(
                status = if (socket != null) "Live" else "Offline — buffering",
                location = accepted,
            )
        }
    }

    private fun stabilizeLocation(candidate: Location, nowElapsed: Long): Location? {
        val previous = lastAcceptedLocation ?: return Location(candidate)
        val elapsedSeconds = ((nowElapsed - lastAcceptedElapsedMs).coerceAtLeast(1L)) / 1000f
        val distanceM = previous.distanceTo(candidate)
        val impliedSpeedMps = distanceM / elapsedSeconds

        if (impliedSpeedMps > MAX_PLAUSIBLE_SPEED_MPS &&
            (!candidate.hasSpeed() || candidate.speed < MAX_PLAUSIBLE_SPEED_MPS * 0.75f)
        ) {
            return null
        }

        val previousAccuracy = if (previous.hasAccuracy()) previous.accuracy else MIN_STATIONARY_RADIUS_M
        val candidateAccuracy = if (candidate.hasAccuracy()) candidate.accuracy else MIN_STATIONARY_RADIUS_M
        val stationaryRadius = min(
            MAX_STATIONARY_RADIUS_M,
            max(MIN_STATIONARY_RADIUS_M, max(previousAccuracy, candidateAccuracy)),
        )
        val reportedSpeed = if (candidate.hasSpeed()) candidate.speed else impliedSpeedMps
        val looksStationary = reportedSpeed < STATIONARY_SPEED_MPS && distanceM <= stationaryRadius

        if (!looksStationary) return Location(candidate)
        if (nowElapsed - lastAcceptedElapsedMs < STATIONARY_HEARTBEAT_MS) return null

        return Location(candidate).apply {
            latitude = previous.latitude
            longitude = previous.longitude
            speed = 0f
            if (previous.hasBearing()) bearing = previous.bearing
        }
    }

    private fun startLocationUpdates() {
        if (checkSelfPermission(Manifest.permission.ACCESS_FINE_LOCATION) != PackageManager.PERMISSION_GRANTED) {
            publishTelemetry("Location permission missing")
            stopSelf()
            return
        }

        val looper = locationThread.looper
        try {
            val gpsEnabled = locationManager.isProviderEnabled(LocationManager.GPS_PROVIDER)
            val networkEnabled = locationManager.isProviderEnabled(LocationManager.NETWORK_PROVIDER)

            if (gpsEnabled) {
                locationManager.requestLocationUpdates(LocationManager.GPS_PROVIDER, 1_000L, 0f, this, looper)
            } else if (networkEnabled) {
                locationManager.requestLocationUpdates(LocationManager.NETWORK_PROVIDER, 3_000L, 0f, this, looper)
            } else {
                publishTelemetry("Location provider unavailable")
            }
        } catch (_: SecurityException) {
            publishTelemetry("Location permission missing")
            stopSelf()
        } catch (_: IllegalArgumentException) {
            publishTelemetry("Location provider unavailable")
        }
    }

    private fun buildLocationPayload(location: Location, sequence: Long): JSONObject {
        return JSONObject().apply {
            put("tracking_session_id", trackingSessionId)
            put("sequence_number", sequence)
            put("recorded_at", Instant.ofEpochMilli(location.time).toString())
            put("latitude", location.latitude)
            put("longitude", location.longitude)
            if (location.hasAccuracy()) put("accuracy_m", location.accuracy.toDouble())
            if (location.hasSpeed()) put("speed_mps", location.speed.toDouble())
            if (location.hasBearing()) put("bearing_deg", location.bearing.toDouble())
            if (location.hasAltitude()) put("altitude_m", location.altitude)
            batteryPercent()?.let { put("battery_pct", it) }
            put("network_type", networkType())
        }
    }

    private fun authenticateAndConnect() {
        val currentConfig = config ?: return
        if (connecting || socket != null) return
        connecting = true

        val body = JSONObject()
            .put("vehicle_code", currentConfig.vehicleCode)
            .put("device_key", currentConfig.deviceKey)
            .toString()
            .toRequestBody("application/json".toMediaType())

        val request = Request.Builder()
            .url("${currentConfig.serverUrl}/api/v1/device/session")
            .post(body)
            .build()

        client.newCall(request).enqueue(object : Callback {
            override fun onFailure(call: Call, e: IOException) {
                connecting = false
                publishTelemetry("Server unavailable — buffering")
                scheduleReconnect()
            }

            override fun onResponse(call: Call, response: Response) {
                response.use {
                    if (!response.isSuccessful) {
                        connecting = false
                        publishTelemetry("Device authentication failed")
                        scheduleReconnect()
                        return
                    }
                    val responseBody = response.body?.string().orEmpty()
                    val token = runCatching { JSONObject(responseBody).getString("access_token") }.getOrNull()
                    if (token.isNullOrBlank()) {
                        connecting = false
                        publishTelemetry("Invalid server response")
                        scheduleReconnect()
                        return
                    }
                    accessToken = token
                    openWebSocket(currentConfig, token)
                }
            }
        })
    }

    private fun openWebSocket(currentConfig: TrackerConfig, token: String) {
        val wsBase = when {
            currentConfig.serverUrl.startsWith("https://") -> currentConfig.serverUrl.replaceFirst("https://", "wss://")
            currentConfig.serverUrl.startsWith("http://") -> currentConfig.serverUrl.replaceFirst("http://", "ws://")
            else -> currentConfig.serverUrl
        }
        val request = Request.Builder()
            .url("$wsBase/api/v1/tracker/ws")
            .header("Authorization", "Bearer $token")
            .build()

        client.newWebSocket(request, object : WebSocketListener() {
            override fun onOpen(webSocket: WebSocket, response: Response) {
                socket = webSocket
                connecting = false
                reconnectDelayMs = 1_000L
                reconnectScheduled = false
                publishTelemetry("Live")
                ioExecutor.execute { flushBacklog(token) }
            }

            override fun onMessage(webSocket: WebSocket, text: String) {
                val message = runCatching { JSONObject(text) }.getOrNull() ?: return
                if (message.optString("type") != "ack") return
                val sequence = message.optLong("sequence_number", -1L)
                if (sequence < 0) return
                ioExecutor.execute {
                    pendingStore.delete(trackingSessionId, sequence)
                    publishTelemetry("Live")
                }
            }

            override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
                webSocket.close(code, reason)
            }

            override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                if (socket === webSocket) socket = null
                connecting = false
                publishTelemetry("Disconnected — buffering")
                scheduleReconnect()
            }

            override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                if (socket === webSocket) socket = null
                connecting = false
                publishTelemetry("Connection lost — buffering")
                scheduleReconnect()
            }
        })
    }

    private fun flushBacklog(token: String) {
        val currentConfig = config ?: return
        val rows = pendingStore.list(200)
        if (rows.isEmpty()) return

        val locations = JSONArray()
        for (row in rows) {
            runCatching { locations.put(JSONObject(row.payload)) }
        }
        if (locations.length() == 0) return

        val body = JSONObject()
            .put("locations", locations)
            .toString()
            .toRequestBody("application/json".toMediaType())
        val request = Request.Builder()
            .url("${currentConfig.serverUrl}/api/v1/tracker/history")
            .header("Authorization", "Bearer $token")
            .post(body)
            .build()

        try {
            client.newCall(request).execute().use { response ->
                if (!response.isSuccessful) return
                pendingStore.deleteIds(rows.map { it.id })
            }
            if (pendingStore.count() > 0) flushBacklog(token)
            publishTelemetry(if (socket != null) "Live" else "Offline — buffering")
        } catch (_: IOException) {
            // Keep the rows. A later reconnect will retry the same immutable records.
        }
    }

    private fun scheduleReconnect() {
        if (!started || reconnectScheduled) return
        reconnectScheduled = true
        val delay = reconnectDelayMs
        reconnectDelayMs = (reconnectDelayMs * 2).coerceAtMost(10_000L)
        mainHandler.postDelayed({
            reconnectScheduled = false
            if (started && socket == null) authenticateAndConnect()
        }, delay)
    }

    private fun networkType(): String {
        val manager = getSystemService(ConnectivityManager::class.java)
        val network = manager.activeNetwork ?: return "NONE"
        val capabilities = manager.getNetworkCapabilities(network) ?: return "NONE"
        return when {
            capabilities.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR) -> "CELLULAR"
            capabilities.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) -> "WIFI"
            capabilities.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET) -> "ETHERNET"
            else -> "OTHER"
        }
    }

    private fun batteryPercent(): Int? {
        val status = registerReceiver(null, IntentFilter(Intent.ACTION_BATTERY_CHANGED)) ?: return null
        val level = status.getIntExtra(BatteryManager.EXTRA_LEVEL, -1)
        val scale = status.getIntExtra(BatteryManager.EXTRA_SCALE, -1)
        if (level < 0 || scale <= 0) return null
        return ((level * 100f) / scale).toInt().coerceIn(0, 100)
    }

    private fun publishTelemetry(status: String, location: Location? = null, notify: Boolean = true) {
        lastStatus = status
        val network = networkType()
        val buffered = runCatching { pendingStore.count() }.getOrDefault(0L)
        val battery = batteryPercent()
        val snapshotLocation = location ?: lastAcceptedLocation

        TrackerStateStore.save(
            applicationContext,
            status = status,
            active = started,
            network = network,
            buffered = buffered,
            battery = battery,
            location = snapshotLocation,
        )

        val intent = Intent(ACTION_TELEMETRY)
            .setPackage(packageName)
            .putExtra(EXTRA_STATUS, status)
            .putExtra(EXTRA_NETWORK, network)
            .putExtra(EXTRA_BUFFERED, buffered)
        battery?.let { intent.putExtra(EXTRA_BATTERY, it) }
        if (snapshotLocation != null) {
            if (snapshotLocation.hasSpeed()) intent.putExtra(EXTRA_SPEED_MPS, snapshotLocation.speed)
            if (snapshotLocation.hasAccuracy()) intent.putExtra(EXTRA_ACCURACY_M, snapshotLocation.accuracy)
            intent.putExtra(EXTRA_LATITUDE, snapshotLocation.latitude)
            intent.putExtra(EXTRA_LONGITUDE, snapshotLocation.longitude)
        }
        sendBroadcast(intent)

        if (notify && started) {
            val manager = getSystemService(NotificationManager::class.java)
            manager.notify(NOTIFICATION_ID, buildNotification(status))
        }
    }

    private fun createNotificationChannel() {
        val manager = getSystemService(NotificationManager::class.java)
        manager.createNotificationChannel(
            NotificationChannel(
                NOTIFICATION_CHANNEL,
                "Ambulance tracking",
                NotificationManager.IMPORTANCE_LOW,
            ).apply {
                description = "Visible while vehicle location tracking is active"
                setShowBadge(false)
            },
        )
    }

    private fun buildNotification(status: String): android.app.Notification {
        val openIntent = Intent(this, MainActivity::class.java)
        val pendingIntent = PendingIntent.getActivity(
            this,
            0,
            openIntent,
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        return android.app.Notification.Builder(this, NOTIFICATION_CHANNEL)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle("EMS Tracker")
            .setContentText(status)
            .setContentIntent(pendingIntent)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .build()
    }

    override fun onDestroy() {
        val wasRunning = started
        started = false
        mainHandler.removeCallbacksAndMessages(null)
        runCatching { locationManager.removeUpdates(this) }
        socket?.close(1000, "tracking stopped")
        socket = null
        accessToken = null
        lastAcceptedLocation = null
        locationThread.quitSafely()
        ioExecutor.shutdown()
        pendingStore.close()
        if (userRequestedStop || !wasRunning) {
            TrackerStateStore.markStopped(applicationContext)
        }
        super.onDestroy()
    }
}
