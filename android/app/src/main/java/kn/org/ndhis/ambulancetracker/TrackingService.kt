package kn.org.ndhis.ambulancetracker

import android.Manifest
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Intent
import android.content.IntentFilter
import android.content.pm.PackageManager
import android.hardware.Sensor
import android.hardware.SensorEvent
import android.hardware.SensorEventListener
import android.hardware.SensorManager
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
import java.io.ByteArrayOutputStream
import java.io.IOException
import java.time.Instant
import java.util.UUID
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.zip.GZIPOutputStream
import kotlin.math.max
import kotlin.math.min
import kotlin.math.sqrt

class TrackingService : Service(), LocationListener, SensorEventListener {
    companion object {
        const val ACTION_START = "kn.org.ndhis.ambulancetracker.START"
        const val ACTION_STOP = "kn.org.ndhis.ambulancetracker.STOP"
        const val ACTION_TELEMETRY = "kn.org.ndhis.ambulancetracker.TELEMETRY"

        const val EXTRA_STATUS = "status"
        const val EXTRA_SPEED_MPS = "speed_mps"
        const val EXTRA_ACCURACY_M = "accuracy_m"
        const val EXTRA_LATITUDE = "latitude"
        const val EXTRA_LONGITUDE = "longitude"
        const val EXTRA_NETWORK = "network"
        const val EXTRA_BUFFERED = "buffered"
        const val EXTRA_BATTERY = "battery"
        const val EXTRA_SAFETY = "safety"

        private const val NOTIFICATION_CHANNEL = "tracking"
        private const val NOTIFICATION_ID = 1001

        private const val MIN_FIX_INTERVAL_MS = 800L
        private const val STATIONARY_HEARTBEAT_MS = 15_000L
        private const val MAX_ACCEPTABLE_ACCURACY_M = 50f
        private const val STATIONARY_SPEED_MPS = 1.5f
        private const val MIN_STATIONARY_RADIUS_M = 8f
        private const val MAX_STATIONARY_RADIUS_M = 30f
        private const val MAX_PLAUSIBLE_SPEED_MPS = 70f

        // Advisory phone-sensor crash detector. It intentionally requires both a
        // significant acceleration impulse and a moving vehicle. It is not an
        // automatic emergency declaration; dispatchers must verify the alert.
        private const val CRASH_G_THRESHOLD = 3.0f
        private const val CRASH_CRITICAL_G = 4.5f
        private const val CRASH_MIN_SPEED_MPS = 8.0f
        private const val CRASH_COOLDOWN_MS = 60_000L
        private const val STANDARD_GRAVITY = 9.80665f
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
    private lateinit var locationHandler: Handler
    private lateinit var locationManager: LocationManager
    private lateinit var sensorManager: SensorManager
    private lateinit var pendingStore: PendingLocationStore

    private val trackingSessionId = UUID.randomUUID().toString()
    private var sequenceNumber = 0L
    private var lastAcceptedElapsedMs = 0L
    private var lastAcceptedLocation: Location? = null
    private var lastKnownSpeedMps = 0f
    private var reconnectDelayMs = 1_000L
    private var reconnectScheduled = false
    private var started = false

    private var motionSensor: Sensor? = null
    private var usingLinearAcceleration = false
    private var gravityInitialized = false
    private val gravity = FloatArray(3)
    private var lastCrashElapsedMs = 0L

    @Volatile private var config: TrackerConfig? = null
    @Volatile private var accessToken: String? = null
    @Volatile private var socket: WebSocket? = null
    @Volatile private var connecting = false
    @Volatile private var safetyState = "Crash detection starting"

    override fun onCreate() {
        super.onCreate()
        pendingStore = PendingLocationStore(applicationContext)
        pendingStore.trimOlderThan(7)
        locationManager = getSystemService(LocationManager::class.java)
        sensorManager = getSystemService(SensorManager::class.java)
        locationThread = HandlerThread("ambulance-location").apply { start() }
        locationHandler = Handler(locationThread.looper)
        createNotificationChannel()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) {
            stopSelf()
            return START_NOT_STICKY
        }

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
            startCrashDetection()
            authenticateAndConnect()
            publishTelemetry("Acquiring GPS")
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
        lastKnownSpeedMps = if (accepted.hasSpeed()) accepted.speed else 0f

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

    private fun startCrashDetection() {
        val linear = sensorManager.getDefaultSensor(Sensor.TYPE_LINEAR_ACCELERATION)
        val accelerometer = sensorManager.getDefaultSensor(Sensor.TYPE_ACCELEROMETER)
        motionSensor = linear ?: accelerometer
        usingLinearAcceleration = linear != null
        val sensor = motionSensor
        if (sensor == null) {
            safetyState = "Crash detection unavailable · no motion sensor"
            publishTelemetry(if (socket != null) "Live" else "Acquiring GPS")
            return
        }
        val registered = sensorManager.registerListener(this, sensor, SensorManager.SENSOR_DELAY_GAME, locationHandler)
        safetyState = if (registered) {
            "Crash detection armed · dispatcher verification required"
        } else {
            "Crash detection unavailable"
        }
    }

    override fun onAccuracyChanged(sensor: Sensor?, accuracy: Int) = Unit

    override fun onSensorChanged(event: SensorEvent) {
        if (!started || event.sensor != motionSensor) return
        val x: Float
        val y: Float
        val z: Float
        if (usingLinearAcceleration) {
            x = event.values[0]
            y = event.values[1]
            z = event.values[2]
        } else {
            if (!gravityInitialized) {
                gravity[0] = event.values[0]
                gravity[1] = event.values[1]
                gravity[2] = event.values[2]
                gravityInitialized = true
                return
            }
            val alpha = 0.85f
            for (i in 0..2) gravity[i] = alpha * gravity[i] + (1f - alpha) * event.values[i]
            x = event.values[0] - gravity[0]
            y = event.values[1] - gravity[1]
            z = event.values[2] - gravity[2]
        }

        val gForce = sqrt(x * x + y * y + z * z) / STANDARD_GRAVITY
        val nowElapsed = SystemClock.elapsedRealtime()
        if (gForce < CRASH_G_THRESHOLD || lastKnownSpeedMps < CRASH_MIN_SPEED_MPS) return
        if (nowElapsed - lastCrashElapsedMs < CRASH_COOLDOWN_MS) return
        lastCrashElapsedMs = nowElapsed
        queueCrashCandidate(gForce)
    }

    private fun queueCrashCandidate(gForce: Float) {
        val currentLocation = lastAcceptedLocation?.let { Location(it) }
        val eventID = UUID.randomUUID().toString()
        val severity = if (gForce >= CRASH_CRITICAL_G) "critical" else "warning"
        val metadata = JSONObject()
            .put("detector", "android_motion_v1")
            .put("g_force", gForce.toDouble())
            .put("speed_kph", (lastKnownSpeedMps * 3.6f).toDouble())
            .put("requires_human_verification", true)
        currentLocation?.takeIf { it.hasAccuracy() }?.let { metadata.put("accuracy_m", it.accuracy.toDouble()) }

        val payload = JSONObject()
            .put("id", eventID)
            .put("tracking_session_id", trackingSessionId)
            .put("event_type", "CRASH_SUSPECTED")
            .put("severity", severity)
            .put("recorded_at", Instant.now().toString())
            .put("metadata", metadata)
        currentLocation?.let {
            payload.put("latitude", it.latitude)
            payload.put("longitude", it.longitude)
        }

        ioExecutor.execute {
            pendingStore.enqueueEvent(eventID, payload.toString())
            safetyState = "Possible crash detected · alert queued for dispatcher"
            publishTelemetry(if (socket != null) "Live" else "Offline — buffering", currentLocation)
            accessToken?.let { flushEvents(it) }
        }
        mainHandler.postDelayed({
            if (safetyState.startsWith("Possible crash")) {
                safetyState = "Crash detection armed · dispatcher verification required"
                publishTelemetry(if (socket != null) "Live" else "Offline — buffering", lastAcceptedLocation)
            }
        }, 30_000L)
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
                ioExecutor.execute {
                    flushBacklog(token)
                    flushEvents(token)
                }
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

    private fun gzip(bytes: ByteArray): ByteArray {
        val output = ByteArrayOutputStream(bytes.size / 2)
        GZIPOutputStream(output).use { it.write(bytes) }
        return output.toByteArray()
    }

    private fun flushBacklog(token: String) {
        val currentConfig = config ?: return
        while (true) {
            val rows = pendingStore.list(400)
            if (rows.isEmpty()) break

            val locations = JSONArray()
            for (row in rows) runCatching { locations.put(JSONObject(row.payload)) }
            if (locations.length() == 0) break

            val raw = JSONObject().put("locations", locations).toString().toByteArray(Charsets.UTF_8)
            val compressed = gzip(raw)
            val body = compressed.toRequestBody("application/json".toMediaType())
            val request = Request.Builder()
                .url("${currentConfig.serverUrl}/api/v1/tracker/history")
                .header("Authorization", "Bearer $token")
                .header("Content-Encoding", "gzip")
                .header("X-Uncompressed-Bytes", raw.size.toString())
                .post(body)
                .build()

            try {
                client.newCall(request).execute().use { response ->
                    if (!response.isSuccessful) return
                    pendingStore.deleteIds(rows.map { it.id })
                }
            } catch (_: IOException) {
                return
            }
        }
        publishTelemetry(if (socket != null) "Live" else "Offline — buffering")
    }

    private fun flushEvents(token: String) {
        val currentConfig = config ?: return
        for (row in pendingStore.listEvents(20)) {
            val body = row.payload.toRequestBody("application/json".toMediaType())
            val request = Request.Builder()
                .url("${currentConfig.serverUrl}/api/v1/tracker/events")
                .header("Authorization", "Bearer $token")
                .post(body)
                .build()
            try {
                client.newCall(request).execute().use { response ->
                    if (!response.isSuccessful) return
                    pendingStore.deleteEvent(row.id)
                }
            } catch (_: IOException) {
                return
            }
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

    private fun publishTelemetry(status: String, location: Location? = null) {
        val intent = Intent(ACTION_TELEMETRY)
            .setPackage(packageName)
            .putExtra(EXTRA_STATUS, status)
            .putExtra(EXTRA_NETWORK, networkType())
            .putExtra(EXTRA_BUFFERED, runCatching { pendingStore.count() }.getOrDefault(0L))
            .putExtra(EXTRA_SAFETY, safetyState)
        batteryPercent()?.let { intent.putExtra(EXTRA_BATTERY, it) }
        if (location != null) {
            if (location.hasSpeed()) intent.putExtra(EXTRA_SPEED_MPS, location.speed)
            if (location.hasAccuracy()) intent.putExtra(EXTRA_ACCURACY_M, location.accuracy)
            intent.putExtra(EXTRA_LATITUDE, location.latitude)
            intent.putExtra(EXTRA_LONGITUDE, location.longitude)
        }
        sendBroadcast(intent)
        val manager = getSystemService(NotificationManager::class.java)
        manager.notify(NOTIFICATION_ID, buildNotification(status))
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
        started = false
        mainHandler.removeCallbacksAndMessages(null)
        sensorManager.unregisterListener(this)
        runCatching { locationManager.removeUpdates(this) }
        socket?.close(1000, "tracking stopped")
        socket = null
        accessToken = null
        lastAcceptedLocation = null
        locationThread.quitSafely()
        ioExecutor.shutdown()
        pendingStore.close()
        super.onDestroy()
    }
}
