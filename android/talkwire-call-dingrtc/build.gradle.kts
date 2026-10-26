import org.jetbrains.kotlin.gradle.tasks.KotlinCompile

// The RTC adapter for Alibaba Cloud ARTC 3.0 (DingRTC): a CallMediaEngine for
// talkwire-call over com.ding.rtc:dingrtc-full. Optional, like the call module:
// the core and talkwire-call never see the SDK. The SDK comes from Alibaba Cloud's
// Maven repository (settings.gradle.kts limits that repository to its group).
plugins {
    id("com.android.library")
    id("org.jetbrains.kotlin.android")
}

android {
    namespace = "io.talkwire.call.dingrtc"
    compileSdk = 34
    defaultConfig {
        minSdk = 26
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    sourceSets["main"].java.srcDir("src/main/kotlin")
    sourceSets["test"].java.srcDir("src/test/kotlin")
    testOptions {
        unitTests.isIncludeAndroidResources = true
        unitTests.all {
            it.testLogging {
                events("failed")
                exceptionFormat = org.gradle.api.tasks.testing.logging.TestExceptionFormat.FULL
            }
        }
    }
}

kotlin {
    explicitApi()
}

androidComponents {
    beforeVariants(selector().withBuildType("release")) { it.enableUnitTest = false }
}

tasks.withType<KotlinCompile>().configureEach {
    kotlinOptions.jvmTarget = "17"
}

dependencies {
    api(project(":talkwire-call"))
    // Written against 3.0.0; the adapter touches only what RealDingRtc names.
    api("com.ding.rtc:dingrtc-full:3.0.0")

    testImplementation("junit:junit:4.13.2")
    testImplementation("org.robolectric:robolectric:4.12.2")
    testImplementation("androidx.test:core:1.5.0")
    testImplementation("org.jetbrains.kotlinx:kotlinx-coroutines-test-jvm:1.9.0")
}
