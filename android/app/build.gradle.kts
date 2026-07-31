plugins {
    // AGP 9 applies Kotlin itself; org.jetbrains.kotlin.android must NOT be added.
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.compose)
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "chat.bounce"
    // 37 because androidx.core 1.19 and lifecycle 2.11 refuse to be consumed by
    // anything older. targetSdk deliberately stays at 36: compiling against
    // newer APIs is independent of opting in to their runtime behaviour.
    compileSdk = 37

    defaultConfig {
        // MUST stay chat.bounce: config/directory.go hardcodes the engine's data
        // directory to /data/data/chat.bounce/bounce. Changing the applicationId
        // points the Go engine at a path this process cannot write.
        applicationId = "chat.bounce"
        minSdk = 29
        targetSdk = 36
        // Matched to the Fyne client this replaces, which ships versionCode 1 /
        // versionName "1.0". Both clients share the applicationId, so installing
        // this one over that one is an in-place update rather than a fresh
        // install - and the platform refuses an update whose versionCode goes
        // backwards. Equal is accepted, lower is INSTALL_FAILED_VERSION_DOWNGRADE.
        //
        // That refusal matters more here than in most apps: the only way past it
        // is to uninstall, which deletes /data/data/chat.bounce/bounce and with
        // it the database and the Tor keys that are this device's identity. A
        // user who takes that route comes back as a new device and has to pair
        // again.
        //
        // So this is not the app's real version, it is the Fyne app's, held here
        // until development catches up past it. Do not "correct" it downwards.
        // The next release must go up, not to 0.x.
        versionCode = 1
        versionName = "1.0"

        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"

        ndk {
            // Must match NATIVE_ABIS in the Makefile. Other dependencies (CameraX)
            // ship armeabi-v7a and x86 libraries, so without this the APK would
            // advertise ABIs that libgojni.so was never built for - it would
            // install on a 32-bit device and then die in System.loadLibrary.
            abiFilters += listOf("arm64-v8a", "x86_64")
        }
    }

    buildTypes {
        debug {
            isMinifyEnabled = false
        }
        release {
            // The Go AAR's JNI entry points are reached reflectively by gomobile's
            // generated stubs; shrinking is off until proguard rules are proven.
            isMinifyEnabled = false
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro",
            )
        }
    }

    buildFeatures {
        compose = true
        buildConfig = true
    }

    packaging {
        resources {
            excludes += "/META-INF/{AL2.0,LGPL2.1}"
        }
        jniLibs {
            // The Tor/OpenSSL .so shipped inside the gomobile AAR must stay
            // uncompressed and page-aligned so it can be mapped directly.
            useLegacyPackaging = false
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}

kotlin {
    jvmToolchain(21)
}

dependencies {
    // Produced by `make android-bind` (gomobile bind of android/goengine).
    // fileTree rather than files(...) so the project still configures before the
    // first bind run.
    implementation(fileTree(mapOf("dir" to "libs", "include" to listOf("*.aar", "*.jar"))))

    implementation(libs.core.ktx)
    implementation(libs.activity.compose)
    implementation(libs.lifecycle.runtime.ktx)
    implementation(libs.lifecycle.runtime.compose)
    implementation(libs.lifecycle.viewmodel.compose)
    implementation(libs.lifecycle.service)
    implementation(libs.navigation.compose)
    implementation(libs.coroutines.android)
    implementation(libs.serialization.json)
    implementation(libs.documentfile)

    val composeBom = platform(libs.compose.bom)
    implementation(composeBom)
    androidTestImplementation(composeBom)
    implementation(libs.compose.ui)
    implementation(libs.compose.ui.graphics)
    implementation(libs.compose.ui.tooling.preview)
    implementation(libs.compose.material3)
    implementation(libs.compose.material.icons.extended)
    debugImplementation(libs.compose.ui.tooling)

    implementation(libs.camera.core)
    implementation(libs.camera.camera2)
    implementation(libs.camera.lifecycle)
    implementation(libs.camera.view)
    implementation(libs.zxing.core)

    testImplementation(libs.junit)
    androidTestImplementation(libs.androidx.junit)
    androidTestImplementation(libs.espresso.core)
}
