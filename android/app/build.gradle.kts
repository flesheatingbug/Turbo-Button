plugins {
    id("com.android.application")
}

// Optional stable signing key (so updates install over each other).
// Set TURBO_KEYSTORE (path) and TURBO_KEYSTORE_PASS; otherwise the debug key is used.
val keystorePath: String? = System.getenv("TURBO_KEYSTORE")

android {
    namespace = "com.turbobutton.app"
    compileSdk = 35

    defaultConfig {
        applicationId = "com.turbobutton.app"
        minSdk = 26
        targetSdk = 35
        // CI build number = version code, so the in-app updater can tell which build is newer.
        versionCode = System.getenv("GITHUB_RUN_NUMBER")?.toIntOrNull() ?: 1
        versionName = "0.10.12." + (System.getenv("GITHUB_RUN_NUMBER") ?: "0")
    }

    signingConfigs {
        if (keystorePath != null) {
            create("stable") {
                storeFile = file(keystorePath)
                storePassword = System.getenv("TURBO_KEYSTORE_PASS")
                keyAlias = "turbobutton"
                keyPassword = System.getenv("TURBO_KEYSTORE_PASS")
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            signingConfig = if (keystorePath != null) signingConfigs.getByName("stable") else signingConfigs.getByName("debug")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    // The control page is shared with the Windows agent: one source of truth.
    sourceSets {
        getByName("main") {
            assets.srcDir("../../agent/web")
        }
    }
}

dependencies {
    // Align kotlin-stdlib-jdk7/jdk8 pulled in by older libraries with kotlin-stdlib (avoids duplicate classes)
    implementation(platform("org.jetbrains.kotlin:kotlin-bom:1.8.22"))
    implementation("androidx.webkit:webkit:1.12.1")
    implementation("androidx.core:core:1.13.1")
    implementation("com.google.android.gms:play-services-code-scanner:16.1.0")
}
