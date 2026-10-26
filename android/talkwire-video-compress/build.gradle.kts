import org.jetbrains.kotlin.gradle.tasks.KotlinCompile

// Video compression before upload (plan 3.9), an optional module: a
// VideoCompressor for MediaSender on Media3 Transformer, which re-encodes a
// large video to H.264 and AAC the way the reference deployment did. It is a
// module of its own so that apps that never send large videos do not pull in
// the encoder stack (Transformer brings ExoPlayer, the effect pipeline and a
// muxer).
plugins {
    id("com.android.library")
    id("org.jetbrains.kotlin.android")
}

android {
    namespace = "io.talkwire.videocompress"
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
    api(project(":talkwire-android"))
    // The same Media3 version as talkwire-android: Media3 artifacts must match.
    implementation("androidx.media3:media3-transformer:1.4.1")
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.9.0")

    testImplementation("junit:junit:4.13.2")
    testImplementation("org.robolectric:robolectric:4.12.2")
    testImplementation("androidx.test:core:1.5.0")
}
