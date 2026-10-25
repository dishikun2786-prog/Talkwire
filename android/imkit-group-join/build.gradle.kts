import org.jetbrains.kotlin.gradle.tasks.KotlinCompile

// Scan-to-join (protocol section 14) for Android, an optional module: a
// group's QR token, joining with it, and reviewing join requests, with
// Compose screens in the kit's theme. It needs only the core; the app draws
// the QR code and scans it (the module ships no encoder or camera).
plugins {
    id("com.android.library")
    id("org.jetbrains.kotlin.android")
}

android {
    namespace = "io.imkit.groupjoin"
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
            it.systemProperty("imkit.repoRoot", rootProject.projectDir.parentFile.absolutePath)
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
    api(project(":imkit-ui"))
    val composeBom = platform("androidx.compose:compose-bom:2023.10.01")
    implementation(composeBom)
    implementation("androidx.compose.material3:material3")

    testImplementation(testFixtures(project(":imkit-core")))
    testImplementation(composeBom)
    testImplementation("androidx.compose.ui:ui-test-junit4")
    testImplementation("junit:junit:4.13.2")
    testImplementation("org.robolectric:robolectric:4.12.2")
    testImplementation("androidx.test:core:1.5.0")
    testImplementation("org.jetbrains.kotlinx:kotlinx-coroutines-test-jvm:1.9.0")
    debugImplementation(composeBom)
    debugImplementation("androidx.compose.ui:ui-test-manifest")
}
