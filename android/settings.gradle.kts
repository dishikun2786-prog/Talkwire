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
        // Alibaba Cloud's DingRTC SDK, for the optional imkit-call-dingrtc adapter
        // only: this repository serves the com.ding.rtc group and nothing else.
        exclusiveContent {
            forRepository { maven { url = uri("https://maven.aliyun.com/repository/public") } }
            filter { includeGroup("com.ding.rtc") }
        }
    }
}

rootProject.name = "imkit-android"

include(":imkit-core")
include(":imkit-storage-room")
include(":imkit-android")
include(":imkit-push-fcm")
include(":imkit-ui")

// Optional modules: each needs only the core.
include(":imkit-group-join")
include(":imkit-call")
include(":imkit-video-compress")
// End-to-end encryption: links libsignal, so it is AGPLv3 and published on its own.
include(":imkit-e2ee")

// RTC adapters: a module and one RTC SDK, optional for apps.
include(":imkit-call-dingrtc")

include(":sample-app")

// The red packet card (plan 4.6) lives with its host example, outside the SDK.
include(":example-red-packet")
project(":example-red-packet").projectDir = file("../examples/red-packet/android")
