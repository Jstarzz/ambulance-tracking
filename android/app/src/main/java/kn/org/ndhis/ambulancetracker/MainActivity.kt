@file:Suppress("DEPRECATION")

package kn.org.ndhis.ambulancetracker

import android.Manifest
import android.app.Activity
import android.content.ActivityNotFoundException
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.content.pm.PackageManager
import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.Paint
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.PowerManager
import android.provider.Settings
import android.view.MotionEvent
import android.view.View
import android.view.WindowInsets
import android.view.inputmethod.EditorInfo
import android.widget.Button
import android.widget.EditText
import android.widget.TextView
import android.widget.Toast
import okhttp3.Call
import okhttp3.Callback
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import org.json.JSONObject
import org.maplibre.android.MapLibre
import org.maplibre.android.annotations.Icon
import org.maplibre.android.annotations.IconFactory
import org.maplibre.android.annotations.Marker
import org.maplibre.android.annotations.MarkerOptions
import org.maplibre.android.annotations.Polyline
import org.maplibre.android.annotations.PolylineOptions
import org.maplibre.android.camera.CameraPosition
import org.maplibre.android.camera.CameraUpdateFactory
import org.maplibre.android.geometry.LatLng
import org.maplibre.android.maps.MapLibreMap
import org.maplibre.android.maps.MapView
import java.io.IOException
import java.util.ArrayDeque
import java.util.Locale
import kotlin.math.abs
import kotlin.math.roundToInt

class MainActivity : Activity() {
    companion object {
        private const val PERMISSION_REQUEST = 100
        private const val MAP_STYLE = "https://tiles.openfreemap.org/styles/liberty"
        private const val MAX_TRAIL_POINTS = 300
        private const val SHEET_PEEK_DP = 112f
    }

    private lateinit var serverUrl: EditText
    private lateinit var vehicleCode: EditText
    private lateinit var deviceKey: EditText
    private lateinit var unitTitle: TextView
    private lateinit var statusText: TextView
    private lateinit var speedText: TextView
    private lateinit var accuracyText: TextView
    private lateinit var coordsText: TextView
    private lateinit var networkText: TextView
    private lateinit var bufferText: TextView
    private lateinit var batteryText: TextView
    private lateinit var serverStateText: TextView
    private lateinit var mapUpdateText: TextView
    private lateinit var connectionHint: TextView
    private lateinit var backgroundModeText: TextView
    private lateinit var registeredUnitText: TextView
    private lateinit var liveDot: View
    private lateinit var operationalSheet: View
    private lateinit var settingsSheet: View
    private lateinit var setupSheet: View
    private lateinit var sheetHandle: View
    private lateinit var configToggleButton: Button
    private lateinit var openReregisterButton: Button
    private lateinit var closeSettingsButton: Button
    private lateinit var saveSetupButton: Button
    private lateinit var cancelSetupButton: Button
    private lateinit var startButton: Button
    private lateinit var stopButton: Button
    private lateinit var recenterButton: Button
    private lateinit var backgroundSettingsButton: Button
    private lateinit var mapView: MapView

    private val httpClient = OkHttpClient()
    private var registrationInFlight = false
    private var map: MapLibreMap? = null
    private var vehicleMarker: Marker? = null
    private var routeLine: Polyline? = null
    private val trailPoints = ArrayDeque<LatLng>()
    private var firstMapFix = true
    private var followLocation = true
    private var lastMapPoint: LatLng? = null
    private var pendingStart = false
    private var receiverRegistered = false
    private var hasSavedConfig = false
    private var statusResolved = false
    private var sheetCollapsed = false
    private var sheetDragStartY = 0f
    private var sheetDragStartTranslation = 0f

    private val telemetryReceiver = object : BroadcastReceiver() {
        override fun onReceive(context: Context?, intent: Intent?) {
            if (intent?.action != TrackingService.ACTION_TELEMETRY) return
            applyTelemetryIntent(intent)
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        MapLibre.getInstance(this)
        setContentView(R.layout.activity_main)
        applySystemBarInsets()

        serverUrl = findViewById(R.id.serverUrl)
        vehicleCode = findViewById(R.id.vehicleCode)
        deviceKey = findViewById(R.id.deviceKey)
        unitTitle = findViewById(R.id.unitTitle)
        statusText = findViewById(R.id.statusText)
        speedText = findViewById(R.id.speedText)
        accuracyText = findViewById(R.id.accuracyText)
        coordsText = findViewById(R.id.coordsText)
        networkText = findViewById(R.id.networkText)
        bufferText = findViewById(R.id.bufferText)
        batteryText = findViewById(R.id.batteryText)
        serverStateText = findViewById(R.id.serverStateText)
        mapUpdateText = findViewById(R.id.mapUpdateText)
        connectionHint = findViewById(R.id.connectionHint)
        backgroundModeText = findViewById(R.id.backgroundModeText)
        registeredUnitText = findViewById(R.id.registeredUnitText)
        liveDot = findViewById(R.id.liveDot)
        operationalSheet = findViewById(R.id.operationalSheet)
        settingsSheet = findViewById(R.id.settingsSheet)
        setupSheet = findViewById(R.id.setupSheet)
        sheetHandle = findViewById(R.id.sheetHandle)
        configToggleButton = findViewById(R.id.configToggleButton)
        openReregisterButton = findViewById(R.id.openReregisterButton)
        closeSettingsButton = findViewById(R.id.closeSettingsButton)
        saveSetupButton = findViewById(R.id.saveSetupButton)
        cancelSetupButton = findViewById(R.id.cancelSetupButton)
        startButton = findViewById(R.id.startButton)
        stopButton = findViewById(R.id.stopButton)
        recenterButton = findViewById(R.id.recenterButton)
        backgroundSettingsButton = findViewById(R.id.backgroundSettingsButton)
        mapView = findViewById(R.id.mapView)

        serverUrl.setText(BuildConfig.EMS_API_BASE_URL)
        configureMap(savedInstanceState)
        configureOperationalSheet()
        configureBackgroundReliability()

        val loaded = SecureConfig.load(this)
        hasSavedConfig = loaded != null
        if (loaded != null) {
            val config = loaded.copy(serverUrl = BuildConfig.EMS_API_BASE_URL)
            if (config.serverUrl != loaded.serverUrl) SecureConfig.save(this, config)
            applyConfigToUi(config)
            showOperationalSurface()
            restoreTrackerState()
        } else {
            unitTitle.text = "Ambulance"
            mapUpdateText.text = "Not registered"
            showSetupSurface(firstRun = true)
        }

        configToggleButton.setOnClickListener {
            if (!hasSavedConfig) return@setOnClickListener
            if (settingsSheet.visibility == View.VISIBLE) {
                showOperationalSurface()
            } else {
                showSettingsSurface()
            }
        }
        openReregisterButton.setOnClickListener { showSetupSurface(firstRun = false) }
        closeSettingsButton.setOnClickListener { showOperationalSurface() }
        saveSetupButton.setOnClickListener { registerDevice() }
        cancelSetupButton.setOnClickListener {
            if (hasSavedConfig) showSettingsSurface()
        }
        deviceKey.setOnEditorActionListener { _, actionId, _ ->
            if (actionId == EditorInfo.IME_ACTION_DONE) {
                registerDevice()
                true
            } else {
                false
            }
        }
        startButton.setOnClickListener {
            if (!hasSavedConfig) {
                showSetupSurface(firstRun = true)
            } else if (statusResolved) {
                prepareStart()
            }
        }
        stopButton.setOnClickListener {
            TrackerStateStore.markStopped(this)
            startService(Intent(this, TrackingService::class.java).setAction(TrackingService.ACTION_STOP))
            renderStatus("Stopped")
        }
    }

    private fun configureMap(savedInstanceState: Bundle?) {
        mapView.onCreate(savedInstanceState)
        mapView.setOnTouchListener { view, event ->
            when (event.actionMasked) {
                MotionEvent.ACTION_DOWN,
                MotionEvent.ACTION_POINTER_DOWN,
                MotionEvent.ACTION_MOVE -> {
                    view.parent?.requestDisallowInterceptTouchEvent(true)
                    followLocation = false
                    if (lastMapPoint != null) mapUpdateText.text = "Browsing map"
                }
                MotionEvent.ACTION_UP,
                MotionEvent.ACTION_CANCEL -> view.parent?.requestDisallowInterceptTouchEvent(false)
            }
            false
        }
        mapView.getMapAsync { readyMap ->
            map = readyMap
            readyMap.uiSettings.isCompassEnabled = false
            readyMap.uiSettings.isRotateGesturesEnabled = false
            readyMap.uiSettings.isScrollGesturesEnabled = true
            readyMap.uiSettings.isZoomGesturesEnabled = true
            readyMap.cameraPosition = CameraPosition.Builder()
                .target(LatLng(17.31, -62.75))
                .zoom(10.5)
                .build()
            readyMap.setStyle(MAP_STYLE) { redrawTrail() }
        }

        recenterButton.setOnClickListener {
            followLocation = true
            val point = lastMapPoint
            if (point != null) {
                map?.easeCamera(CameraUpdateFactory.newLatLng(point), 250)
                mapUpdateText.text = "Following ambulance"
            } else {
                mapUpdateText.text = "Waiting for GPS"
            }
        }
    }

    private fun configureOperationalSheet() {
        sheetHandle.setOnClickListener { settleSheet(!sheetCollapsed) }
        sheetHandle.setOnTouchListener { view, event ->
            when (event.actionMasked) {
                MotionEvent.ACTION_DOWN -> {
                    sheetDragStartY = event.rawY
                    sheetDragStartTranslation = operationalSheet.translationY
                    view.parent?.requestDisallowInterceptTouchEvent(true)
                    true
                }

                MotionEvent.ACTION_MOVE -> {
                    val next = (sheetDragStartTranslation + event.rawY - sheetDragStartY)
                        .coerceIn(0f, maxSheetTranslation())
                    setSheetTranslation(next)
                    true
                }

                MotionEvent.ACTION_UP, MotionEvent.ACTION_CANCEL -> {
                    view.parent?.requestDisallowInterceptTouchEvent(false)
                    val moved = abs(event.rawY - sheetDragStartY)
                    if (moved < dp(8f)) {
                        view.performClick()
                    } else {
                        settleSheet(operationalSheet.translationY > maxSheetTranslation() * 0.42f)
                    }
                    true
                }

                else -> false
            }
        }
    }

    private fun maxSheetTranslation(): Float =
        (operationalSheet.height - dp(SHEET_PEEK_DP)).coerceAtLeast(0f)

    private fun setSheetTranslation(value: Float) {
        operationalSheet.translationY = value
        recenterButton.translationY = value
    }

    private fun settleSheet(collapsed: Boolean) {
        sheetCollapsed = collapsed
        val target = if (collapsed) maxSheetTranslation() else 0f
        operationalSheet.animate().translationY(target).setDuration(180).start()
        recenterButton.animate().translationY(target).setDuration(180).start()
    }

    private fun dp(value: Float): Float = value * resources.displayMetrics.density

    private fun configureBackgroundReliability() {
        backgroundSettingsButton.setOnClickListener {
            try {
                startActivity(Intent(Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS))
            } catch (_: ActivityNotFoundException) {
                startActivity(
                    Intent(
                        Settings.ACTION_APPLICATION_DETAILS_SETTINGS,
                        Uri.parse("package:$packageName"),
                    ),
                )
            }
        }
        updateBackgroundReliability()
    }

    private fun updateBackgroundReliability() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.M) {
            backgroundModeText.text = "Background · unrestricted"
            backgroundModeText.setTextColor(getColor(R.color.app_green))
            backgroundSettingsButton.visibility = View.GONE
            return
        }

        val powerManager = getSystemService(PowerManager::class.java)
        val unrestricted = powerManager.isIgnoringBatteryOptimizations(packageName)
        if (unrestricted) {
            backgroundModeText.text = "Background · unrestricted"
            backgroundModeText.setTextColor(getColor(R.color.app_green))
            backgroundSettingsButton.visibility = View.GONE
        } else {
            backgroundModeText.text = "Background · battery optimized"
            backgroundModeText.setTextColor(getColor(R.color.app_amber))
            backgroundSettingsButton.visibility = View.VISIBLE
        }
    }

    private fun showOperationalSurface() {
        setupSheet.visibility = View.GONE
        settingsSheet.visibility = View.GONE
        operationalSheet.visibility = View.VISIBLE
        recenterButton.visibility = View.VISIBLE
        configToggleButton.visibility = View.VISIBLE
        operationalSheet.post { setSheetTranslation(if (sheetCollapsed) maxSheetTranslation() else 0f) }
    }

    private fun showSettingsSurface() {
        setupSheet.visibility = View.GONE
        operationalSheet.visibility = View.GONE
        settingsSheet.visibility = View.VISIBLE
        recenterButton.visibility = View.GONE
        configToggleButton.visibility = View.VISIBLE
        registeredUnitText.text = vehicleCode.text.toString().ifBlank { "Registered tracker" }
    }

    private fun showSetupSurface(firstRun: Boolean) {
        operationalSheet.visibility = View.GONE
        settingsSheet.visibility = View.GONE
        setupSheet.visibility = View.VISIBLE
        recenterButton.visibility = View.GONE
        configToggleButton.visibility = if (firstRun) View.GONE else View.VISIBLE
        cancelSetupButton.visibility = if (firstRun) View.GONE else View.VISIBLE
        deviceKey.setText("")
        connectionHint.text = if (firstRun) {
            "Enter the 8-character registration code shown by dispatch."
        } else {
            "This phone is registered as ${vehicleCode.text}. Enter a new code only when dispatch tells you to re-register it."
        }
    }

    private fun applyConfigToUi(config: TrackerConfig) {
        serverUrl.setText(BuildConfig.EMS_API_BASE_URL)
        vehicleCode.setText(config.vehicleCode)
        registeredUnitText.text = config.vehicleCode
        deviceKey.setText("")
        unitTitle.text = config.vehicleCode
        serverStateText.text = "Registered"
        if (lastMapPoint == null) mapUpdateText.text = "Waiting for GPS"
    }

    private fun restoreTrackerState() {
        val snapshot = TrackerStateStore.load(this)
        if (snapshot == null) {
            renderCheckingStatus()
            return
        }
        renderSnapshot(snapshot)
    }

    private fun renderSnapshot(snapshot: TrackerUiSnapshot) {
        statusResolved = true
        renderStatus(snapshot.status)
        networkText.text = snapshot.network
        bufferText.text = "${snapshot.buffered} pending"
        snapshot.battery?.let { batteryText.text = "$it%" }
        snapshot.speedMps?.let { speedText.text = String.format(Locale.US, "%.0f km/h", it * 3.6f) }
        snapshot.accuracyM?.let { accuracyText.text = String.format(Locale.US, "±%.0f m", it) }
        if (snapshot.latitude != null && snapshot.longitude != null) {
            coordsText.text = "Location acquired"
            updateMap(LatLng(snapshot.latitude, snapshot.longitude))
        }
    }

    private fun renderCheckingStatus() {
        statusResolved = false
        statusText.text = "Checking tracker…"
        statusText.setTextColor(getColor(R.color.app_muted))
        liveDot.visibility = View.INVISIBLE
        startButton.visibility = View.VISIBLE
        startButton.isEnabled = false
        startButton.text = "Checking tracker…"
        stopButton.visibility = View.GONE
    }

    private fun requestTrackerStatus() {
        if (!hasSavedConfig) return
        runCatching {
            startService(Intent(this, TrackingService::class.java).setAction(TrackingService.ACTION_STATUS))
        }
    }

    private fun applyTelemetryIntent(intent: Intent) {
        val status = intent.getStringExtra(TrackingService.EXTRA_STATUS) ?: "Unknown"
        statusResolved = true
        renderStatus(status)
        networkText.text = intent.getStringExtra(TrackingService.EXTRA_NETWORK) ?: "—"
        bufferText.text = "${intent.getLongExtra(TrackingService.EXTRA_BUFFERED, 0L)} pending"

        if (intent.hasExtra(TrackingService.EXTRA_SPEED_MPS)) {
            val speed = intent.getFloatExtra(TrackingService.EXTRA_SPEED_MPS, 0f) * 3.6f
            speedText.text = String.format(Locale.US, "%.0f km/h", speed)
        }
        if (intent.hasExtra(TrackingService.EXTRA_ACCURACY_M)) {
            val accuracy = intent.getFloatExtra(TrackingService.EXTRA_ACCURACY_M, 0f)
            accuracyText.text = String.format(Locale.US, "±%.0f m", accuracy)
        }
        if (intent.hasExtra(TrackingService.EXTRA_LATITUDE) && intent.hasExtra(TrackingService.EXTRA_LONGITUDE)) {
            val latitude = intent.getDoubleExtra(TrackingService.EXTRA_LATITUDE, 0.0)
            val longitude = intent.getDoubleExtra(TrackingService.EXTRA_LONGITUDE, 0.0)
            coordsText.text = "Location acquired"
            mapUpdateText.text = if (followLocation) "Following ambulance" else "Browsing map"
            updateMap(LatLng(latitude, longitude))
        }
        if (intent.hasExtra(TrackingService.EXTRA_BATTERY)) {
            batteryText.text = "${intent.getIntExtra(TrackingService.EXTRA_BATTERY, 0)}%"
        }
    }

    private fun registerDevice() {
        if (registrationInFlight) return
        val enrollmentCode = deviceKey.text.toString().trim().uppercase(Locale.US)
        if (enrollmentCode.length != 8) {
            Toast.makeText(this, "Enter the 8-character registration code from dispatch.", Toast.LENGTH_LONG).show()
            return
        }

        registrationInFlight = true
        saveSetupButton.isEnabled = false
        saveSetupButton.text = "Registering…"

        val deviceName = listOf(Build.MANUFACTURER, Build.MODEL)
            .filter { it.isNotBlank() }
            .joinToString(" ")
            .ifBlank { "Android tracker" }
        val payload = JSONObject()
            .put("code", enrollmentCode)
            .put("device_name", deviceName)
            .toString()
            .toRequestBody("application/json".toMediaType())
        val request = Request.Builder()
            .url("${BuildConfig.EMS_API_BASE_URL}/api/v1/device/enroll")
            .post(payload)
            .build()

        httpClient.newCall(request).enqueue(object : Callback {
            override fun onFailure(call: Call, e: IOException) {
                runOnUiThread {
                    finishRegistrationError("Could not reach dispatch. Check the phone's connection and try again.")
                }
            }

            override fun onResponse(call: Call, response: Response) {
                response.use {
                    val body = response.body?.string().orEmpty()
                    if (!response.isSuccessful) {
                        val message = runCatching { JSONObject(body).optString("error") }.getOrNull()
                            ?.takeIf { it.isNotBlank() }
                            ?: "Registration code was not accepted."
                        runOnUiThread { finishRegistrationError(message) }
                        return
                    }

                    val json = runCatching { JSONObject(body) }.getOrNull()
                    val code = json?.optString("vehicle_code").orEmpty()
                    val key = json?.optString("device_key").orEmpty()
                    if (code.isBlank() || key.isBlank()) {
                        runOnUiThread {
                            finishRegistrationError("Dispatch returned an invalid registration response.")
                        }
                        return
                    }

                    runOnUiThread {
                        val config = TrackerConfig(BuildConfig.EMS_API_BASE_URL, code, key)
                        SecureConfig.save(this@MainActivity, config)
                        TrackerStateStore.markStopped(this@MainActivity)
                        hasSavedConfig = true
                        registrationInFlight = false
                        saveSetupButton.isEnabled = true
                        saveSetupButton.text = "Register device"
                        applyConfigToUi(config)
                        showOperationalSurface()
                        renderStatus("Stopped")
                        Toast.makeText(
                            this@MainActivity,
                            "$code registered on this phone.",
                            Toast.LENGTH_SHORT,
                        ).show()
                    }
                }
            }
        })
    }

    private fun finishRegistrationError(message: String) {
        registrationInFlight = false
        saveSetupButton.isEnabled = true
        saveSetupButton.text = "Register device"
        Toast.makeText(this, message, Toast.LENGTH_LONG).show()
    }

    private fun applySystemBarInsets() {
        val root = findViewById<View>(R.id.rootScroll)
        val baseLeft = root.paddingLeft
        val baseTop = root.paddingTop
        val baseRight = root.paddingRight
        val baseBottom = root.paddingBottom

        root.setOnApplyWindowInsetsListener { view, insets ->
            val top: Int
            val bottom: Int
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
                val bars = insets.getInsets(WindowInsets.Type.systemBars())
                top = bars.top
                bottom = bars.bottom
            } else {
                top = insets.systemWindowInsetTop
                bottom = insets.systemWindowInsetBottom
            }
            view.setPadding(baseLeft, baseTop + top, baseRight, baseBottom + bottom)
            insets
        }
        root.requestApplyInsets()
    }

    private fun renderStatus(status: String) {
        statusResolved = true
        val normalized = status.lowercase(Locale.US)
        val live = normalized == "live" || normalized == "tracking"
        val starting = normalized == "starting" || normalized == "acquiring gps"
        val offline = normalized.contains("offline") || normalized.contains("buffer")
        val unavailable = normalized.contains("missing") ||
            normalized.contains("unavailable") ||
            normalized == "unknown"

        when {
            live -> {
                statusText.text = "Tracking active"
                statusText.setTextColor(getColor(R.color.app_green))
                liveDot.visibility = View.VISIBLE
                mapUpdateText.text = if (lastMapPoint == null) "Waiting for GPS" else mapUpdateText.text
            }
            starting -> {
                statusText.text = if (normalized.contains("gps")) "Finding GPS" else "Starting tracking…"
                statusText.setTextColor(getColor(R.color.app_amber))
                liveDot.visibility = View.INVISIBLE
            }
            offline -> {
                statusText.text = "Offline · saving locally"
                statusText.setTextColor(getColor(R.color.app_amber))
                liveDot.visibility = View.INVISIBLE
            }
            unavailable -> {
                statusText.text = "Tracking unavailable"
                statusText.setTextColor(getColor(R.color.app_red))
                liveDot.visibility = View.INVISIBLE
            }
            else -> {
                statusText.text = "Tracking off"
                statusText.setTextColor(getColor(R.color.app_text))
                liveDot.visibility = View.INVISIBLE
            }
        }

        val active = live || starting || offline
        startButton.isEnabled = !active
        startButton.text = "Start tracking"
        startButton.visibility = if (active) View.GONE else View.VISIBLE
        stopButton.visibility = if (active) View.VISIBLE else View.GONE
    }

    private fun updateMap(point: LatLng) {
        lastMapPoint = point
        trailPoints.addLast(point)
        while (trailPoints.size > MAX_TRAIL_POINTS) trailPoints.removeFirst()
        redrawTrail()

        val readyMap = map ?: return
        if (firstMapFix) {
            firstMapFix = false
            followLocation = true
            readyMap.easeCamera(CameraUpdateFactory.newLatLngZoom(point, 15.0), 350)
        } else if (followLocation) {
            readyMap.easeCamera(CameraUpdateFactory.newLatLng(point), 250)
        }
    }

    private fun createVehicleIcon(): Icon {
        val density = resources.displayMetrics.density
        val size = (18f * density).roundToInt().coerceAtLeast(18)
        val outline = (2f * density).coerceAtLeast(2f)
        val radius = size / 2f - outline
        val bitmap = Bitmap.createBitmap(size, size, Bitmap.Config.ARGB_8888)
        val canvas = Canvas(bitmap)
        val paint = Paint(Paint.ANTI_ALIAS_FLAG)

        paint.color = getColor(R.color.app_panel)
        canvas.drawCircle(size / 2f, size / 2f, radius + outline, paint)
        paint.color = getColor(R.color.app_blue)
        canvas.drawCircle(size / 2f, size / 2f, radius, paint)

        return IconFactory.getInstance(this).fromBitmap(bitmap)
    }

    private fun redrawTrail() {
        val readyMap = map ?: return
        val points = trailPoints.toList()
        if (points.isEmpty()) return

        val marker = vehicleMarker
        if (marker == null) {
            vehicleMarker = readyMap.addMarker(
                MarkerOptions()
                    .position(points.last())
                    .icon(createVehicleIcon())
                    .title(vehicleCode.text.toString().ifBlank { "Ambulance" }),
            )
        } else {
            marker.position = points.last()
            readyMap.updateMarker(marker)
        }

        if (points.size < 2) return
        val line = routeLine
        if (line == null) {
            routeLine = readyMap.addPolyline(
                PolylineOptions()
                    .addAll(points)
                    .color(getColor(R.color.app_blue))
                    .width(4f),
            )
        } else {
            line.points = points
            readyMap.updatePolyline(line)
        }
    }

    override fun onStart() {
        super.onStart()
        mapView.onStart()
        val filter = IntentFilter(TrackingService.ACTION_TELEMETRY)
        if (Build.VERSION.SDK_INT >= 33) {
            registerReceiver(telemetryReceiver, filter, Context.RECEIVER_NOT_EXPORTED)
        } else {
            registerReceiver(telemetryReceiver, filter)
        }
        receiverRegistered = true
        requestTrackerStatus()
    }

    override fun onResume() {
        super.onResume()
        mapView.onResume()
        updateBackgroundReliability()
    }

    override fun onPause() {
        mapView.onPause()
        super.onPause()
    }

    override fun onStop() {
        if (receiverRegistered) {
            unregisterReceiver(telemetryReceiver)
            receiverRegistered = false
        }
        mapView.onStop()
        super.onStop()
    }

    override fun onDestroy() {
        httpClient.dispatcher.cancelAll()
        mapView.onDestroy()
        super.onDestroy()
    }

    override fun onLowMemory() {
        super.onLowMemory()
        mapView.onLowMemory()
    }

    override fun onSaveInstanceState(outState: Bundle) {
        super.onSaveInstanceState(outState)
        mapView.onSaveInstanceState(outState)
    }

    private fun prepareStart() {
        val config = SecureConfig.load(this)
        if (config == null) {
            hasSavedConfig = false
            showSetupSurface(firstRun = true)
            return
        }

        val deployedConfig = config.copy(serverUrl = BuildConfig.EMS_API_BASE_URL)
        if (deployedConfig.serverUrl != config.serverUrl) SecureConfig.save(this, deployedConfig)
        applyConfigToUi(deployedConfig)

        if (!hasFineLocation()) {
            pendingStart = true
            requestRuntimePermissions()
            return
        }

        requestNotificationPermissionIfNeeded()
        startTrackingService()
    }

    private fun startTrackingService() {
        pendingStart = false
        startForegroundService(Intent(this, TrackingService::class.java).setAction(TrackingService.ACTION_START))
        renderStatus("Starting")
    }

    private fun hasFineLocation(): Boolean =
        checkSelfPermission(Manifest.permission.ACCESS_FINE_LOCATION) == PackageManager.PERMISSION_GRANTED

    private fun requestRuntimePermissions() {
        val permissions = mutableListOf(Manifest.permission.ACCESS_FINE_LOCATION)
        if (Build.VERSION.SDK_INT >= 33 &&
            checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            permissions += Manifest.permission.POST_NOTIFICATIONS
        }
        requestPermissions(permissions.toTypedArray(), PERMISSION_REQUEST)
    }

    private fun requestNotificationPermissionIfNeeded() {
        if (Build.VERSION.SDK_INT >= 33 &&
            checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), PERMISSION_REQUEST)
        }
    }

    override fun onRequestPermissionsResult(
        requestCode: Int,
        permissions: Array<out String>,
        grantResults: IntArray,
    ) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults)
        if (requestCode != PERMISSION_REQUEST || !pendingStart) return
        if (hasFineLocation()) {
            startTrackingService()
        } else {
            pendingStart = false
            Toast.makeText(this, "Precise location permission is required for tracking.", Toast.LENGTH_LONG).show()
        }
    }
}
