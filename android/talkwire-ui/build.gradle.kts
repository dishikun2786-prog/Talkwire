import org.jetbrains.kotlin.gradle.tasks.KotlinCompile

// The Compose UI kit: chat screens and components on top of ChatEngine, the
// ImTheme tokens, the message type registry, and the framework's own strings
// (English by default, with Chinese and Vietnamese).
plugins {
    id("com.android.library")
    id("org.jetbrains.kotlin.android")
}

android {
    namespace = "io.talkwire.ui"
    compileSdk = 34
    defaultConfig {
        minSdk = 26
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    buildFeatures {
        compose = true
    }
    composeOptions {
        // Matches Kotlin 1.9.24, as in the source app.
        kotlinCompilerExtensionVersion = "1.5.14"
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
    // Compose UI tests need ui-test-manifest, a debug-only dependency: unit tests run on debug only.
    beforeVariants(selector().withBuildType("release")) { it.enableUnitTest = false }
}

tasks.withType<KotlinCompile>().configureEach {
    kotlinOptions.jvmTarget = "17"
}

dependencies {
    api(project(":talkwire-android"))

    // The versions the source app ships.
    val composeBom = platform("androidx.compose:compose-bom:2023.10.01")
    api(composeBom)
    api("androidx.compose.ui:ui")
    api("androidx.compose.foundation:foundation")
    api("androidx.compose.material3:material3")
    implementation("androidx.compose.material:material-icons-extended")
    implementation("androidx.compose.ui:ui-tooling-preview")
    implementation("io.coil-kt:coil-compose:2.6.0")
    // Pickers, the camera, the microphone permission and the back handler.
    implementation("androidx.activity:activity-compose:1.8.1")
    implementation("androidx.media3:media3-exoplayer:1.4.1")
    implementation("androidx.media3:media3-ui:1.4.1")

    testImplementation(testFixtures(project(":talkwire-core")))
    testImplementation(composeBom)
    testImplementation("androidx.compose.ui:ui-test-junit4")
    testImplementation("junit:junit:4.13.2")
    testImplementation("org.robolectric:robolectric:4.12.2")
    testImplementation("androidx.test:core:1.5.0")
    testImplementation("org.jetbrains.kotlinx:kotlinx-coroutines-test-jvm:1.9.0")
    debugImplementation(composeBom)
    debugImplementation("androidx.compose.ui:ui-test-manifest")
    debugImplementation("androidx.compose.ui:ui-tooling")
}
