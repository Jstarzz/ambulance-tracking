package kn.org.ndhis.ambulancetracker

import android.app.Activity
import android.app.Application
import android.os.Bundle
import okhttp3.OkHttpClient
import org.maplibre.android.maps.MapLibreMap
import org.maplibre.android.maps.MapView
import java.util.WeakHashMap
import java.util.concurrent.TimeUnit

/**
 * Wires the foreground tracker map to the read-only fleet feed without coupling
 * fleet visibility to the location-upload service. The upload service remains
 * single-purpose and continues running independently when the UI is backgrounded.
 */
class TrackerApplication : Application(), Application.ActivityLifecycleCallbacks {
    private val peerBindings = WeakHashMap<Activity, FleetMapPeers>()

    override fun onCreate() {
        super.onCreate()
        registerActivityLifecycleCallbacks(this)
    }

    override fun onActivityStarted(activity: Activity) {
        if (activity !is MainActivity) return
        val peers = peerBindings[activity] ?: createPeerBinding(activity).also {
            peerBindings[activity] = it
        }
        peers.start()
    }

    override fun onActivityStopped(activity: Activity) {
        peerBindings[activity]?.stop()
    }

    override fun onActivityDestroyed(activity: Activity) {
        peerBindings.remove(activity)?.close()
    }

    private fun createPeerBinding(activity: MainActivity): FleetMapPeers {
        var map: MapLibreMap? = null
        val client = OkHttpClient.Builder()
            .connectTimeout(8, TimeUnit.SECONDS)
            .readTimeout(8, TimeUnit.SECONDS)
            .writeTimeout(8, TimeUnit.SECONDS)
            .build()
        val peers = FleetMapPeers(activity, client) { map }
        val mapView = activity.findViewById<MapView>(R.id.mapView)
        mapView.getMapAsync { readyMap ->
            map = readyMap
            peers.onMapStyleReloaded()
        }
        return peers
    }

    override fun onActivityCreated(activity: Activity, savedInstanceState: Bundle?) = Unit
    override fun onActivityResumed(activity: Activity) = Unit
    override fun onActivityPaused(activity: Activity) = Unit
    override fun onActivitySaveInstanceState(activity: Activity, outState: Bundle) = Unit
}
