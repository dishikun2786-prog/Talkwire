import org.jetbrains.kotlin.gradle.tasks.KotlinCompile

// Android pieces of the SDK that are not UI: rendering the notification
// decisions of talkwire-core (C6), the media cache (C7), the Keystore key wrapper
// for the Room store's crypto-shredding, and image compression before upload.
plugins {
    id("com.android.library")
    id("org.jetbrains.kotlin.android")
}

android {
    namespace = "io.talkwire.android"
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
    api(project(":talkwire-storage-room"))
    implementation("androidx.core:core-ktx:1.12.0")
    // The versions the source app ships.
    api("androidx.media3:media3-common:1.4.1")
    api("androidx.media3:media3-datasource:1.4.1")
    implementation("androidx.media3:media3-database:1.4.1")

    testImplementation(testFixtures(project(":talkwire-core")))
    testImplementation("junit:junit:4.13.2")
    testImplementation("org.robolectric:robolectric:4.12.2")
    testImplementation("androidx.test:core:1.5.0")
    testImplementation("org.jetbrains.kotlinx:kotlinx-coroutines-test-jvm:1.9.0")
}
