package kn.org.ndhis.ambulancetracker

import android.Manifest
import android.app.Activity
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.content.pm.PackageManager
import android.os.Build
import android.os.Bundle
import android.widget.Button
import android.widget.EditText
import android.widget.TextView
import android.widget.Toast
import java.util.Locale

class MainActivity : Activity() {
    companion object {
        private const val PERMISSION_REQUEST = 100
    }

    private lateinit var serverUrl: EditText
    private lateinit var vehicleCode: EditText
    private lateinit var deviceKey: EditText
    private lateinit var statusText: TextView
    private lateinit var speedText: TextView
    private lateinit var accuracyText: TextView
    private lateinit var coordsText: TextView
    private lateinit var networkText: TextView
    private lateinit var bufferText: TextView
    private lateinit var batteryText: TextView

    private var pendingStart = false
    private var receiverRegistered = false

    private val telemetryReceiver = object : BroadcastReceiver() {
        override fun onReceive(context: Context?, intent: Intent?) {
            if (intent?.action != TrackingService.ACTION_TELEMETRY) return
            statusText.text = "Status: ${intent.getStringExtra(TrackingService.EXTRA_STATUS) ?: "unknown"}"
            networkText.text = "Network: ${intent.getStringExtra(TrackingService.EXTRA_NETWORK) ?: "—"}"
            bufferText.text = "Buffered records: ${intent.getLongExtra(TrackingService.EXTRA_BUFFERED, 0L)}"

            if (intent.hasExtra(TrackingService.EXTRA_SPEED_MPS)) {
                val speed = intent.getFloatExtra(TrackingService.EXTRA_SPEED_MPS, 0f) * 3.6f
                speedText.text = String.format(Locale.US, "Speed: %.0f km/h", speed)
            }
            if (intent.hasExtra(TrackingService.EXTRA_ACCURACY_M)) {
                val accuracy = intent.getFloatExtra(TrackingService.EXTRA_ACCURACY_M, 0f)
                accuracyText.text = String.format(Locale.US, "Accuracy: ±%.1f m", accuracy)
            }
            if (intent.hasExtra(TrackingService.EXTRA_LATITUDE) && intent.hasExtra(TrackingService.EXTRA_LONGITUDE)) {
                val latitude = intent.getDoubleExtra(TrackingService.EXTRA_LATITUDE, 0.0)
                val longitude = intent.getDoubleExtra(TrackingService.EXTRA_LONGITUDE, 0.0)
                coordsText.text = String.format(Locale.US, "Coordinates: %.6f, %.6f", latitude, longitude)
            }
            if (intent.hasExtra(TrackingService.EXTRA_BATTERY)) {
                batteryText.text = "Battery: ${intent.getIntExtra(TrackingService.EXTRA_BATTERY, 0)}%"
            }
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_main)

        serverUrl = findViewById(R.id.serverUrl)
        vehicleCode = findViewById(R.id.vehicleCode)
        deviceKey = findViewById(R.id.deviceKey)
        statusText = findViewById(R.id.statusText)
        speedText = findViewById(R.id.speedText)
        accuracyText = findViewById(R.id.accuracyText)
        coordsText = findViewById(R.id.coordsText)
        networkText = findViewById(R.id.networkText)
        bufferText = findViewById(R.id.bufferText)
        batteryText = findViewById(R.id.batteryText)

        SecureConfig.load(this)?.let { config ->
            serverUrl.setText(config.serverUrl)
            vehicleCode.setText(config.vehicleCode)
            deviceKey.setText(config.deviceKey)
        }

        findViewById<Button>(R.id.startButton).setOnClickListener { prepareStart() }
        findViewById<Button>(R.id.stopButton).setOnClickListener {
            startService(Intent(this, TrackingService::class.java).setAction(TrackingService.ACTION_STOP))
            statusText.text = "Status: stopped"
        }
    }

    override fun onStart() {
        super.onStart()
        val filter = IntentFilter(TrackingService.ACTION_TELEMETRY)
        if (Build.VERSION.SDK_INT >= 33) {
            registerReceiver(telemetryReceiver, filter, Context.RECEIVER_NOT_EXPORTED)
        } else {
            @Suppress("DEPRECATION")
            registerReceiver(telemetryReceiver, filter)
        }
        receiverRegistered = true
    }

    override fun onStop() {
        if (receiverRegistered) {
            unregisterReceiver(telemetryReceiver)
            receiverRegistered = false
        }
        super.onStop()
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
            Toast.makeText(this, "Vehicle code and a valid device key are required.", Toast.LENGTH_LONG).show()
            return
        }

        SecureConfig.save(this, TrackerConfig(url, code, key))
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
        statusText.text = "Status: starting"
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
