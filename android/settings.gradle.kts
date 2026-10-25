pluginManagement {
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
}

dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories {
        google()
        mavenCentral()
    }
}

rootProject.name = "imkit-android"

include(":imkit-core")
include(":imkit-storage-room")
include(":imkit-android")
include(":imkit-push-fcm")
include(":imkit-ui")
include(":sample-app")

// The red packet card (plan 4.6) lives with its host example, outside the SDK.
include(":example-red-packet")
project(":example-red-packet").projectDir = file("../examples/red-packet/android")
