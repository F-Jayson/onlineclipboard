plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

android {
    namespace = "com.onlineclipboard.app"
    compileSdk = 36

    defaultConfig {
        applicationId = "com.onlineclipboard.app"
        minSdk = 29
        targetSdk = 36
        versionCode = 1
        versionName = "0.1.0-skeleton"
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions { jvmTarget = "17" }
}
