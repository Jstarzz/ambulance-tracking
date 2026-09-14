@file:Suppress("DEPRECATION")

package kn.org.ndhis.ambulancetracker

import android.Manifest
import android.app.Activity
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.content.pm.PackageManager
import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.Paint
import android.os.Build
import android.os.Bundle
import android.view.MotionEvent
import android.view.View
import android.view.WindowInsets
import android.widget.Button
import android.widget.EditText
import android.widget.TextView
import android.widget.Toast
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
import java.util.ArrayDeque
import java.util.Locale
import kotlin.math.roundToInt

class MainActivity : Activity() {
    companion object {
        private const val PERMISSION_REQUEST = 100
        private const val MAP_STYLE = "https://tiles.openfreemap.org/styles/dark"
        private const val MAX_TRAIL_POINTS = 300
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
    private lateinit var liveDot: View
    private lateinit var operationalSheet: View
    private lateinit var setupSheet: View
    private lateinit var configToggleButton: Button
    private lateinit var saveSetupButton: Button
    private lateinit var cancelSetupButton: Button
    private lateinit var startButton: Button
    private lateinit var stopButton: Button
    private lateinit var recenterButton: Button
    private lateinit var mapView: MapView

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

    private val telemetryReceiver = object : BroadcastReceiver() {
        override fun onReceive(context: Context?, intent: Intent?) {
            if (intent?.action != TrackingService.ACTION_TELEMETRY) return

            val status = intent.getStringExtra(TrackingService.EXTRA_STATUS) ?: "Unknown"
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
        liveDot = findViewById(R.id.liveDot)
        operationalSheet = findViewById(R.id.operationalSheet)
        setupSheet = findViewById(R.id.setupSheet)
        configToggleButton = findViewById(R.id.configToggleButton)
        saveSetupButton = findViewById(R.id.saveSetupButton)
        cancelSetupButton = findViewById(R.id.cancelSetupButton)
        startButton = findViewById(R.id.startButton)
        stopButton = findViewById(R.id.stopButton)
        recenterButton = findViewById(R.id.recenterButton)
        mapView = findViewById(R.id.mapView)

        configureMap(savedInstanceState)

        val savedConfig = SecureConfig.load(this)
        hasSavedConfig = savedConfig != null
        if (savedConfig != null) {
            applyConfigToUi(savedConfig)
            showOperationalSurface()
        } else {
            unitTitle.text = "Ambulance"
            showSetupSurface(firstRun = true)
        }

        configToggleButton.setOnClickListener {
            if (setupSheet.visibility == View.VISIBLE && hasSavedConfig) {
                showOperationalSurface()
            } else {
                showSetupSurface(firstRun = !hasSavedConfig)
            }
        }
        saveSetupButton.setOnClickListener { saveConfiguration() }
        cancelSetupButton.setOnClickListener { if (hasSavedConfig) showOperationalSurface() }
        startButton.setOnClickListener {
            if (!hasSavedConfig) showSetupSurface(firstRun = true) else prepareStart()
        }
        stopButton.setOnClickListener {
            startService(Intent(this, TrackingService::class.java).setAction(TrackingService.ACTION_STOP))
            renderStatus("Stopped")
        }

        renderStatus("Stopped")
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

    private fun showOperationalSurface() {
        setupSheet.visibility = View.GONE
        operationalSheet.visibility = View.VISIBLE
        recenterButton.visibility = View.VISIBLE
    }

    private fun showSetupSurface(firstRun: Boolean) {
        operationalSheet.visibility = View.GONE
        setupSheet.visibility = View.VISIBLE
        recenterButton.visibility = View.GONE
        cancelSetupButton.visibility = if (firstRun) View.GONE else View.VISIBLE
        connectionHint.text = if (firstRun) {
            "Connect this phone to its ambulance unit."
        } else {
            "Update the secure provisioning details for this tracker."
        }
    }

    private fun applyConfigToUi(config: TrackerConfig) {
        serverUrl.setText(config.serverUrl)
        vehicleCode.setText(config.vehicleCode)
        deviceKey.setText(config.deviceKey)
        unitTitle.text = config.vehicleCode
        serverStateText.text = "Connected"
    }

    private fun saveConfiguration() {
        val url = serverUrl.text.toString().trim().trimEnd('/')
        val code = vehicleCode.text.toString().trim().uppercase(Locale.US)
        val key = deviceKey.text.toString()

        if (!url.startsWith("https://")) {
            Toast.makeText(this, "Server URL must use HTTPS.", Toast.LENGTH_LONG).show()
            return
        }
        if (code.isBlank() || key.length < 16) {
            Toast.makeText(this, "Ambulance code and a valid device key are required.", Toast.LENGTH_LONG).show()
            return
        }

        val config = TrackerConfig(url, code, key)
        SecureConfig.save(this, config)
        hasSavedConfig = true
        applyConfigToUi(config)
        showOperationalSurface()
        renderStatus("Stopped")
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
        val normalized = status.lowercase(Locale.US)
        val live = normalized == "live" || normalized == "tracking"
        val starting = normalized == "starting" || normalized == "acquiring gps"
        val offline = normalized.contains("offline") || normalized.contains("buffer")
        val unavailable = normalized.contains("missing") || normalized.contains("unavailable") || normalized == "unknown"

        when {
            live -> {
                statusText.text = "Tracking live"
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

        paint.color = getColor(R.color.app_bg)
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
    }

    override fun onResume() {
        super.onResume()
        mapView.onResume()
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
        applyConfigToUi(config)

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
