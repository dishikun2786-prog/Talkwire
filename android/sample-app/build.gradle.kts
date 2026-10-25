import org.jetbrains.kotlin.gradle.tasks.KotlinCompile

// The sample app: what an app writes to use ImKit (token provider, contact
// source, push setup). Package io.imkit.sample, so it installs next to any
// other app on a test phone.
plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

android {
    namespace = "io.imkit.sample"
    compileSdk = 34
    defaultConfig {
        applicationId = "io.imkit.sample"
        minSdk = 26
        targetSdk = 34
        versionCode = 1
        versionName = "0.1.0"
        // The sample host and the IM server on the development machine, reached through
        // `adb reverse tcp:6001 tcp:6001` and `adb reverse tcp:6062 tcp:6062`.
        buildConfigField("String", "HOST_URL", "\"http://127.0.0.1:6001\"")
        buildConfigField("String", "IM_URL", "\"ws://127.0.0.1:6062/ws/im\"")
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    buildFeatures {
        compose = true
        buildConfig = true
    }
    composeOptions {
        kotlinCompilerExtensionVersion = "1.5.14"
    }
    sourceSets["main"].java.srcDir("src/main/kotlin")
}

tasks.withType<KotlinCompile>().configureEach {
    kotlinOptions.jvmTarget = "17"
}

dependencies {
    implementation(project(":imkit-ui"))
    implementation(project(":imkit-storage-room"))
    // The host's own message type, joined through the registry only.
    implementation(project(":example-red-packet"))
    implementation("androidx.activity:activity-compose:1.8.1")
    val composeBom = platform("androidx.compose:compose-bom:2023.10.01")
    implementation(composeBom)
    implementation("androidx.compose.material3:material3")
}
