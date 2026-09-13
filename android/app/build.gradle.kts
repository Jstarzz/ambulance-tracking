plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

val releaseStoreFile = System.getenv("ANDROID_KEYSTORE_FILE")
val releaseStorePassword = System.getenv("ANDROID_KEYSTORE_PASSWORD")
val releaseKeyAlias = System.getenv("ANDROID_KEY_ALIAS")
val releaseKeyPassword = System.getenv("ANDROID_KEY_PASSWORD")
val releaseSigningConfigured = listOf(
    releaseStoreFile,
    releaseStorePassword,
    releaseKeyAlias,
    releaseKeyPassword,
).all { !it.isNullOrBlank() }

val configuredVersionCode = System.getenv("VERSION_CODE")?.toIntOrNull() ?: 1
val configuredVersionName = System.getenv("VERSION_NAME") ?: "0.1.0"
val defaultServerUrl = System.getenv("TRACKER_DEFAULT_SERVER_URL")
    ?.trim()
    ?.trimEnd('/')
    ?.takeIf { it.startsWith("https://") }
    ?: "https://tracking.itsjosiahdavis.dev"

android {
    namespace = "kn.org.ndhis.ambulancetracker"
    compileSdk = 35

    buildFeatures {
        buildConfig = true
    }

    defaultConfig {
        applicationId = "kn.org.ndhis.ambulancetracker"
        minSdk = 28
        targetSdk = 35
        versionCode = configuredVersionCode
        versionName = configuredVersionName
        buildConfigField("String", "DEFAULT_SERVER_URL", "\"$defaultServerUrl\"")
    }

    signingConfigs {
        if (releaseSigningConfigured) {
            create("production") {
                storeFile = file(releaseStoreFile!!)
                storePassword = releaseStorePassword
                keyAlias = releaseKeyAlias
                keyPassword = releaseKeyPassword
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            if (releaseSigningConfigured) {
                signingConfig = signingConfigs.getByName("production")
            }
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions {
        jvmTarget = "17"
    }
}

dependencies {
    implementation("com.squareup.okhttp3:okhttp:4.12.0")
    implementation("org.maplibre.gl:android-sdk-opengl:13.6.1")
}
