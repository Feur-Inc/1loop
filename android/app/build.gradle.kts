plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.plugin.compose")
}

android {
    namespace = "io.oneloop.remote"
    compileSdk = 37
    defaultConfig {
        applicationId = "io.oneloop.remote"
        minSdk = 26
        targetSdk = 37
        versionCode = 1
        versionName = "0.1"
    }
    buildTypes {
        release {
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"))
            signingConfig = signingConfigs.getByName("debug") // sideloaded builds for now
        }
    }
    buildFeatures { compose = true }
}

dependencies {
    implementation(platform("androidx.compose:compose-bom:2026.09.00"))
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.foundation:foundation")
    implementation("androidx.activity:activity-compose:1.13.0")
    // Applies the compilation profiles Compose ships with at install time:
    // the interface runs compiled from the first launch, without warming up.
    implementation("androidx.profileinstaller:profileinstaller:1.4.1")
}
