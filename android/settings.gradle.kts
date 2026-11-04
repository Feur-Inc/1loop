// SPDX-License-Identifier: MPL-2.0

pluginManagement { repositories { google(); mavenCentral(); gradlePluginPortal() } }
dependencyResolutionManagement {
    repositories {
        google()
        mavenCentral()

    }
}
rootProject.name = "1loop-remote"
include(":app")
