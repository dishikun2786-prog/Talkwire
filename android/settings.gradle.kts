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
        // Alibaba Cloud's DingRTC SDK, for the optional talkwire-call-dingrtc adapter
        // only: this repository serves the com.ding.rtc group and nothing else.
        exclusiveContent {
            forRepository { maven { url = uri("https://maven.aliyun.com/repository/public") } }
            filter { includeGroup("com.ding.rtc") }
        }
    }
}

rootProject.name = "talkwire-android"

include(":talkwire-core")
include(":talkwire-storage-room")
include(":talkwire-android")
include(":talkwire-push-fcm")
include(":talkwire-ui")

// Optional modules: each needs only the core.
include(":talkwire-group-join")
include(":talkwire-call")
include(":talkwire-video-compress")
// End-to-end encryption: links libsignal, so it is AGPLv3 and published on its own.
include(":talkwire-e2ee")

// RTC adapters: a module and one RTC SDK, optional for apps.
include(":talkwire-call-dingrtc")

include(":sample-app")

// The red packet card (plan 4.6) lives with its host example, outside the SDK.
include(":example-red-packet")
project(":example-red-packet").projectDir = file("../examples/red-packet/android")
