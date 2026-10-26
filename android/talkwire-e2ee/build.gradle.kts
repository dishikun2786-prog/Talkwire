import org.jetbrains.kotlin.gradle.tasks.KotlinCompile

// End-to-end encryption (protocol section 13) for Android, an optional
// module. It links libsignal, which is AGPLv3, so this module is published on
// its own under AGPLv3 (see LICENSE here); the rest of Talkwire is Apache-2.0 and
// never depends on it. The server only relays opaque ciphertext.
//
// Apps that build an APK with it must turn on core library desugaring: the
// libsignal AAR declares that requirement (minSdk 26 needs none of it, but
// the AAR metadata check fails without it).
plugins {
    id("com.android.library")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.kapt")
}

android {
    namespace = "io.talkwire.e2ee"
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
        kotlinCompilerExtensionVersion = "1.5.14"
    }
    sourceSets["main"].java.srcDir("src/main/kotlin")
    sourceSets["test"].java.srcDir("src/test/kotlin")
    testOptions {
        unitTests.isIncludeAndroidResources = true
        unitTests.all {
            // The golden transcripts and the replay binary are read from the repository.
            it.systemProperty("talkwire.repoRoot", rootProject.projectDir.parentFile.absolutePath)
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

kapt {
    arguments {
        arg("room.schemaLocation", "$projectDir/schemas")
    }
}

androidComponents {
    // Compose UI tests need ui-test-manifest, a debug-only dependency: unit tests run on debug only.
    beforeVariants(selector().withBuildType("release")) { it.enableUnitTest = false }
}

tasks.withType<KotlinCompile>().configureEach {
    kotlinOptions.jvmTarget = "17"
}

// The AAR metadata check also runs for unit tests, which run on a full JDK
// where desugaring means nothing. Apps still get the check when they build.
tasks.matching { it.name.matches(Regex("check.*UnitTestAarMetadata")) }.configureEach { enabled = false }

dependencies {
    api(project(":talkwire-ui"))
    // Pinned on purpose: both ends of a session must run the same libsignal
    // (plan 7.1), and the tests' peer is this same version. Its JVM build,
    // libsignal-client, comes with it and carries the desktop native library
    // the unit tests load.
    api("org.signal:libsignal-android:0.73.0")
    implementation("androidx.room:room-runtime:2.6.1")
    implementation("androidx.room:room-ktx:2.6.1")
    kapt("androidx.room:room-compiler:2.6.1")
    val composeBom = platform("androidx.compose:compose-bom:2023.10.01")
    implementation(composeBom)
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.material:material-icons-extended")

    testImplementation(testFixtures(project(":talkwire-core")))
    testImplementation(composeBom)
    testImplementation("androidx.compose.ui:ui-test-junit4")
    testImplementation("junit:junit:4.13.2")
    testImplementation("org.robolectric:robolectric:4.12.2")
    testImplementation("androidx.test:core:1.5.0")
    testImplementation("org.jetbrains.kotlinx:kotlinx-coroutines-test-jvm:1.9.0")
    debugImplementation(composeBom)
    debugImplementation("androidx.compose.ui:ui-test-manifest")
}
