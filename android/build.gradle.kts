// Top-level build file. Plugin versions are declared in gradle/libs.versions.toml
// and applied in app/build.gradle.kts.
// AGP 9 ships built-in Kotlin support, so there is deliberately no
// org.jetbrains.kotlin.android plugin here - applying it is a hard error.
plugins {
    alias(libs.plugins.android.application) apply false
    alias(libs.plugins.kotlin.compose) apply false
    alias(libs.plugins.kotlin.serialization) apply false
}
