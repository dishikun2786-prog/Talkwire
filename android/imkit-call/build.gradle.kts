import org.jetbrains.kotlin.gradle.tasks.KotlinCompile

// Voice and video calls (protocol section 16) for Android, an optional module:
// the signaling, the call state, the foreground service and the call screen.
// Media goes through an RTC adapter (CallMediaEngine); this module has no RTC
// SDK in its graph. imkit-call-dingrtc is the adapter for Alibaba Cloud ARTC.
plugins {
    id("com.android.library")
    id("org.jetbrains.kotlin.android")
}

android {
    namespace = "io.imkit.call"
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
    implementation("androidx.core:core-ktx:1.12.0")
    // The permission requests of the call buttons.
    implementation("androidx.activity:activity-compose:1.8.1")
    val composeBom = platform("androidx.compose:compose-bom:2023.10.01")
    implementation(composeBom)
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.material:material-icons-extended")

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
