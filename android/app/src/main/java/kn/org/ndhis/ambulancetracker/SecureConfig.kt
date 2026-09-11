package kn.org.ndhis.ambulancetracker

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

data class TrackerConfig(
    val serverUrl: String,
    val vehicleCode: String,
    val deviceKey: String,
)

object SecureConfig {
    private const val PREFS = "tracker_config"
    private const val KEY_ALIAS = "ambulance_tracker_device_key"
    private const val SERVER_URL = "server_url"
    private const val VEHICLE_CODE = "vehicle_code"
    private const val DEVICE_KEY_CIPHERTEXT = "device_key_ciphertext"
    private const val DEVICE_KEY_IV = "device_key_iv"

    fun save(context: Context, config: TrackerConfig) {
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.ENCRYPT_MODE, getOrCreateKey())
        val ciphertext = cipher.doFinal(config.deviceKey.toByteArray(Charsets.UTF_8))

        context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
            .edit()
            .putString(SERVER_URL, config.serverUrl.trimEnd('/'))
            .putString(VEHICLE_CODE, config.vehicleCode.trim())
            .putString(DEVICE_KEY_CIPHERTEXT, Base64.encodeToString(ciphertext, Base64.NO_WRAP))
            .putString(DEVICE_KEY_IV, Base64.encodeToString(cipher.iv, Base64.NO_WRAP))
            .apply()
    }

    fun load(context: Context): TrackerConfig? {
        val prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        val serverUrl = prefs.getString(SERVER_URL, null) ?: return null
        val vehicleCode = prefs.getString(VEHICLE_CODE, null) ?: return null
        val ciphertextText = prefs.getString(DEVICE_KEY_CIPHERTEXT, null) ?: return null
        val ivText = prefs.getString(DEVICE_KEY_IV, null) ?: return null

        return try {
            val cipher = Cipher.getInstance("AES/GCM/NoPadding")
            val iv = Base64.decode(ivText, Base64.NO_WRAP)
            cipher.init(Cipher.DECRYPT_MODE, getOrCreateKey(), GCMParameterSpec(128, iv))
            val plaintext = cipher.doFinal(Base64.decode(ciphertextText, Base64.NO_WRAP))
            TrackerConfig(serverUrl, vehicleCode, plaintext.toString(Charsets.UTF_8))
        } catch (_: Exception) {
            null
        }
    }

    private fun getOrCreateKey(): SecretKey {
        val keyStore = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        val existing = keyStore.getKey(KEY_ALIAS, null)
        if (existing is SecretKey) return existing

        val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore")
        val spec = KeyGenParameterSpec.Builder(
            KEY_ALIAS,
            KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT,
        )
            .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
            .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
            .setKeySize(256)
            .build()
        generator.init(spec)
        return generator.generateKey()
    }
}
