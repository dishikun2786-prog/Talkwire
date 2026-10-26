import org.jetbrains.kotlin.gradle.tasks.KotlinCompile

// Firebase Cloud Messaging for Talkwire: the token source for
// io.talkwire.core.notify.PushRegistration and a messaging-service base class
// that turns the server's data pushes into PushNotice. Separate from
// talkwire-android so that apps without Google Play services do not pull it in.
plugins {
    id("com.android.library")
    id("org.jetbrains.kotlin.android")
}

android {
    namespace = "io.talkwire.push.fcm"
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

tasks.withType<KotlinCompile>().configureEach {
    kotlinOptions.jvmTarget = "17"
}

dependencies {
    api(project(":talkwire-core"))
    // The BOM the source app uses (firebase-messaging 24.1.0).
    api(platform("com.google.firebase:firebase-bom:33.7.0"))
    api("com.google.firebase:firebase-messaging")

    testImplementation("junit:junit:4.13.2")
    testImplementation("org.robolectric:robolectric:4.12.2")
    testImplementation("androidx.test:core:1.5.0")
    testImplementation("org.jetbrains.kotlinx:kotlinx-coroutines-test-jvm:1.9.0")
}
