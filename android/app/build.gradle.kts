plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.plugin.compose")
}

abstract class LocalRuntimeBuild : Exec() {
    @get:OutputDirectory
    abstract val outputDirectory: DirectoryProperty
}

// One version for every part of 1loop (the repository's VERSION file):
// versionCode follows internal/update.Code, "1.2.3" being 10203.
val oneloopVersion = rootProject.file("../VERSION").readText().trim()
val oneloopVersionCode = oneloopVersion.split(".").fold(0) { code, part -> code * 100 + part.toInt() }

val localRuntimeDirectory = layout.buildDirectory.dir("local-runtime")
val buildLocalNative = tasks.register<LocalRuntimeBuild>("buildLocalNative") {
    outputDirectory.set(localRuntimeDirectory.map { it.dir("jniLibs") })
    inputs.file(rootProject.file("runtime/build.py"))
    inputs.file(rootProject.file("runtime/guard.c"))
    inputs.dir(rootProject.file("runtime/patches"))
    val sdk = providers.environmentVariable("ANDROID_HOME").orElse("${System.getProperty("user.home")}/Android/Sdk")
    val ndk = providers.environmentVariable("ANDROID_NDK_HOME").orElse(sdk.map { "$it/ndk/30.0.16248370" })
    commandLine("python3", rootProject.file("runtime/build.py"), "--ndk", ndk.get(), "--output", localRuntimeDirectory.get().asFile)
}
val buildLocalAgent = tasks.register<LocalRuntimeBuild>("buildLocalAgent") {
    outputDirectory.set(localRuntimeDirectory.map { it.dir("assets") })
    inputs.files(rootProject.file("../go.mod"), rootProject.file("../go.sum"), rootProject.file("../VERSION"))
    inputs.files(fileTree(rootProject.file("../internal")) { include("**/*.go") })
    inputs.files(fileTree(rootProject.file("../cmd/1loop-server")) { include("**/*.go") })
    workingDir(rootProject.file(".."))
    environment("CGO_ENABLED", "0")
    environment("GOOS", "linux")
    environment("GOARCH", "arm64")
    commandLine("go", "build", "-trimpath", "-ldflags=-s -w -X 1loop/internal/update.Version=$oneloopVersion", "-o", outputDirectory.get().file("local/1loop-server").asFile, "./cmd/1loop-server")
}

android {
    namespace = "io.oneloop.remote"
    compileSdk = 37
    defaultConfig {
        applicationId = "io.oneloop.remote"
        minSdk = 26
        targetSdk = 37
        versionCode = oneloopVersionCode
        versionName = oneloopVersion
    }
    buildTypes {
        release {
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"))
            signingConfig = signingConfigs.getByName("debug") // sideloaded builds for now
        }
    }
    buildFeatures { compose = true; aidl = true }
    packaging {
        jniLibs.useLegacyPackaging = true
        jniLibs.keepDebugSymbols += "**/*.so"
    }
}

androidComponents.onVariants { variant ->
    variant.sources.jniLibs?.addGeneratedSourceDirectory(buildLocalNative, LocalRuntimeBuild::outputDirectory)
    variant.sources.assets?.addGeneratedSourceDirectory(buildLocalAgent, LocalRuntimeBuild::outputDirectory)
}

dependencies {
    implementation(platform("androidx.compose:compose-bom:2026.09.00"))
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.foundation:foundation")
    implementation("androidx.activity:activity-compose:1.13.0")
    // Applies the compilation profiles Compose ships with at install time:
    // the interface runs compiled from the first launch, without warming up.
    implementation("androidx.profileinstaller:profileinstaller:1.4.1")
    implementation("androidx.webkit:webkit:1.17.1") // separate WebView profiles for ChatGPT (Web)
    implementation("io.noties.markwon:core:4.6.2")
    implementation("io.noties.markwon:ext-tables:4.6.2")
    implementation("io.noties.markwon:ext-strikethrough:4.6.2")
    implementation("io.noties.markwon:ext-tasklist:4.6.2")
    implementation("io.noties.markwon:inline-parser:4.6.2")
    implementation("io.noties.markwon:ext-latex:4.6.2")
    implementation("com.journeyapps:zxing-android-embedded:4.3.0")
    testImplementation("junit:junit:4.13.2")
    implementation("dev.rikka.shizuku:api:13.1.5")
    implementation("dev.rikka.shizuku:provider:13.1.5")
}
