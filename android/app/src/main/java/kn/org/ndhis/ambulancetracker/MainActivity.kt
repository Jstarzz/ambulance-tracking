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
import android.graphics.Color
import android.graphics.Paint
import android.os.Build
import android.os.Bundle
import android.view.MotionEvent
import android.view.View
import android.view.WindowInsets
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
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
import java.util.concurrent.TimeUnit
import kotlin.math.roundToInt

class MainActivity : Activity() {
    companion object {
        private const val PERMISSION_REQUEST = 100
        private const val MAP_STYLE = "https://tiles.openfreemap.org/styles/dark"
        private const val MAX_TRAIL_POINTS = 300
    }

    private val setupClient = OkHttpClient.Builder()
        .connectTimeout(10, TimeUnit.SECONDS)
        .readTimeout(15, TimeUnit.SECONDS)
        .build()

    private lateinit var serverUrl: EditText
    private lateinit var vehicleCode: EditText
    private lateinit var deviceKey: EditText
    private lateinit var enrollmentCode: EditText
    private lateinit var enrollmentRow: LinearLayout
    private lateinit var advancedFields: LinearLayout
    private lateinit var enrollButton: Button
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
    private lateinit var safetyText: TextView
    private lateinit var configToggleButton: Button
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
    private var configExpanded = false
    private var provisioned = false

    private val telemetryReceiver = object : BroadcastReceiver() {
        override fun onReceive(context: Context?, intent: Intent?) {
            if (intent?.action != TrackingService.ACTION_TELEMETRY) return

            val status = intent.getStringExtra(TrackingService.EXTRA_STATUS) ?: "Unknown"
            renderStatus(status)
            networkText.text = intent.getStringExtra(TrackingService.EXTRA_NETWORK) ?: "—"
            bufferText.text = "${intent.getLongExtra(TrackingService.EXTRA_BUFFERED, 0L)} records"
            intent.getStringExtra(TrackingService.EXTRA_SAFETY)?.let { safetyText.text = it }

            if (intent.hasExtra(TrackingService.EXTRA_SPEED_MPS)) {
                val speed = intent.getFloatExtra(TrackingService.EXTRA_SPEED_MPS, 0f) * 3.6f
                speedText.text = String.format(Locale.US, "%.0f km/h", speed)
            }
            if (intent.hasExtra(TrackingService.EXTRA_ACCURACY_M)) {
                val accuracy = intent.getFloatExtra(TrackingService.EXTRA_ACCURACY_M, 0f)
                accuracyText.text = String.format(Locale.US, "±%.1f m", accuracy)
            }
            if (intent.hasExtra(TrackingService.EXTRA_LATITUDE) && intent.hasExtra(TrackingService.EXTRA_LONGITUDE)) {
                val latitude = intent.getDoubleExtra(TrackingService.EXTRA_LATITUDE, 0.0)
                val longitude = intent.getDoubleExtra(TrackingService.EXTRA_LONGITUDE, 0.0)
                coordsText.text = String.format(Locale.US, "%.5f, %.5f", latitude, longitude)
                mapUpdateText.text = if (followLocation) "Following location" else "Browsing map"
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
        enrollmentCode = findViewById(R.id.enrollmentCode)
        enrollmentRow = findViewById(R.id.enrollmentRow)
        advancedFields = findViewById(R.id.advancedFields)
        enrollButton = findViewById(R.id.enrollButton)
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
        safetyText = findViewById(R.id.safetyText)
        configToggleButton = findViewById(R.id.configToggleButton)
        recenterButton = findViewById(R.id.recenterButton)
        mapView = findViewById(R.id.mapView)

        serverUrl.setText(BuildConfig.DEFAULT_SERVER_URL)
        serverStateText.text = BuildConfig.DEFAULT_SERVER_URL.removePrefix("https://")
        setConfigExpanded(false)

        configureMap(savedInstanceState)

        val savedConfig = SecureConfig.load(this)
        if (savedConfig != null) {
            applyConfig(savedConfig)
            setProvisioned(true)
        } else {
            setProvisioned(false)
        }

        enrollButton.setOnClickListener { enrollDevice() }
        configToggleButton.setOnClickListener { setConfigExpanded(!configExpanded) }
        findViewById<Button>(R.id.startButton).setOnClickListener { prepareStart() }
        findViewById<Button>(R.id.stopButton).setOnClickListener {
            startService(Intent(this, TrackingService::class.java).setAction(TrackingService.ACTION_STOP))
            renderStatus("Stopped")
            safetyText.text = "Crash detection arms while tracking"
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
                mapUpdateText.text = "Following location"
            } else {
                mapUpdateText.text = "Waiting for GPS"
            }
        }
    }

    private fun enrollDevice() {
        val url = serverUrl.text.toString().trim().trimEnd('/')
        val code = enrollmentCode.text.toString().trim().uppercase(Locale.US)
        if (!url.startsWith("https://")) {
            Toast.makeText(this, "Server URL must use HTTPS.", Toast.LENGTH_LONG).show()
            return
        }
        if (code.length !in 8..9) {
            Toast.makeText(this, "Enter the one-time enrollment code.", Toast.LENGTH_LONG).show()
            return
        }

        enrollButton.isEnabled = false
        enrollButton.text = "Registering…"
        val body = JSONObject()
            .put("enrollment_code", code)
            .toString()
            .toRequestBody("application/json".toMediaType())
        val request = Request.Builder()
            .url("$url/api/v1/device/enroll")
            .post(body)
            .build()

        setupClient.newCall(request).enqueue(object : Callback {
            override fun onFailure(call: Call, e: IOException) {
                runOnUiThread {
                    enrollButton.isEnabled = true
                    enrollButton.text = "Register this device"
                    Toast.makeText(this@MainActivity, "Could not reach the tracking server.", Toast.LENGTH_LONG).show()
                }
            }

            override fun onResponse(call: Call, response: Response) {
                response.use {
                    val payload = response.body?.string().orEmpty()
                    if (!response.isSuccessful) {
                        runOnUiThread {
                            enrollButton.isEnabled = true
                            enrollButton.text = "Register this device"
                            Toast.makeText(this@MainActivity, "Enrollment code is invalid, expired, or already used.", Toast.LENGTH_LONG).show()
                        }
                        return
                    }
                    val json = runCatching { JSONObject(payload) }.getOrNull()
                    val device = json?.optJSONObject("device")
                    val returnedCode = device?.optString("vehicle_code").orEmpty()
                    val returnedKey = json?.optString("device_key").orEmpty()
                    if (returnedCode.isBlank() || returnedKey.length < 16) {
                        runOnUiThread {
                            enrollButton.isEnabled = true
                            enrollButton.text = "Register this device"
                            Toast.makeText(this@MainActivity, "Server returned an invalid enrollment response.", Toast.LENGTH_LONG).show()
                        }
                        return
                    }
                    val config = TrackerConfig(url, returnedCode, returnedKey)
                    SecureConfig.save(applicationContext, config)
                    runOnUiThread {
                        applyConfig(config)
                        enrollmentCode.text.clear()
                        enrollButton.isEnabled = true
                        enrollButton.text = "Register this device"
                        setProvisioned(true)
                        Toast.makeText(this@MainActivity, "$returnedCode registered. Ready to track.", Toast.LENGTH_LONG).show()
                    }
                }
            }
        })
    }

    private fun applyConfig(config: TrackerConfig) {
        serverUrl.setText(config.serverUrl)
        vehicleCode.setText(config.vehicleCode)
        deviceKey.setText(config.deviceKey)
        unitTitle.text = config.vehicleCode
        serverStateText.text = config.serverUrl.removePrefix("https://").removePrefix("http://")
    }

    private fun setProvisioned(value: Boolean) {
        provisioned = value
        enrollmentRow.visibility = if (value) View.GONE else View.VISIBLE
        val host = serverUrl.text.toString().removePrefix("https://").removePrefix("http://").trimEnd('/')
        val code = vehicleCode.text.toString().trim()
        if (value) {
            connectionHint.text = "$code is securely registered · $host"
            unitTitle.text = code.ifBlank { "Ambulance unit" }
        } else {
            connectionHint.text = "Enter the one-time enrollment code from the dispatcher admin panel."
            unitTitle.text = "Register this ambulance"
        }
    }

    private fun setConfigExpanded(expanded: Boolean) {
        configExpanded = expanded
        advancedFields.visibility = if (expanded) View.VISIBLE else View.GONE
        configToggleButton.text = if (expanded) "Hide advanced settings" else "Advanced settings"
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
        statusText.text = status
        val live = status.equals("Live", ignoreCase = true)
        statusText.setBackgroundResource(if (live) R.drawable.bg_status_live else R.drawable.bg_status_idle)
        statusText.setTextColor(getColor(if (live) R.color.app_teal else R.color.app_muted))
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
        paint.color = getColor(R.color.app_teal)
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
                PolylineOptions().addAll(points).color(Color.rgb(85, 184, 175)).width(4f),
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
        setupClient.dispatcher.executorService.shutdown()
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
        val url = serverUrl.text.toString().trim().trimEnd('/')
        val code = vehicleCode.text.toString().trim().uppercase(Locale.US)
        val key = deviceKey.text.toString()

        if (!url.startsWith("https://")) {
            Toast.makeText(this, "Production tracker URL must use HTTPS.", Toast.LENGTH_LONG).show()
            return
        }
        if (code.isBlank() || key.length < 16) {
            Toast.makeText(this, "Register this device first, or enter manual credentials in Advanced settings.", Toast.LENGTH_LONG).show()
            return
        }

        val config = TrackerConfig(url, code, key)
        SecureConfig.save(this, config)
        applyConfig(config)
        setProvisioned(true)
        setConfigExpanded(false)
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
        val intent = Intent(this, TrackingService::class.java).setAction(TrackingService.ACTION_START)
        startForegroundService(intent)
        renderStatus("Starting")
        safetyText.text = "Crash detection armed · alerts require dispatcher verification"
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

    override fun onRequestPermissionsResult(requestCode: Int, permissions: Array<out String>, grantResults: IntArray) {
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
