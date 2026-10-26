import org.jetbrains.kotlin.gradle.tasks.KotlinCompile

// Pure Kotlin/JVM: no Android, no Room, no Compose. Everything that decides
// what the client does lives here, so it runs (and is tested) on a plain JVM.
plugins {
    `java-library`
    // testFixtures: the store contract and the in-memory servers, shared with
    // the other modules' tests.
    `java-test-fixtures`
    id("org.jetbrains.kotlin.jvm")
}

java {
    sourceCompatibility = JavaVersion.VERSION_17
    targetCompatibility = JavaVersion.VERSION_17
}

kotlin {
    explicitApi()
}

tasks.withType<KotlinCompile>().configureEach {
    kotlinOptions.jvmTarget = "17"
}

dependencies {
    // The -jvm artifacts directly: same classes as the multiplatform
    // coordinates, and the versions the source project already resolves.
    api("org.jetbrains.kotlinx:kotlinx-coroutines-core-jvm:1.9.0")
    // api: MediaUploader and the WebSocket transport take an OkHttpClient in their public constructors.
    api("com.squareup.okhttp3:okhttp:4.12.0")

    testImplementation("junit:junit:4.13.2")
    testImplementation("org.jetbrains.kotlinx:kotlinx-coroutines-test-jvm:1.9.0")

    testFixturesApi("junit:junit:4.13.2")
}

tasks.test {
    // The contract files (uid vectors, scenarios) are read from the repository.
    systemProperty("talkwire.repoRoot", rootProject.projectDir.parentFile.absolutePath)
    testLogging {
        events("failed")
        exceptionFormat = org.gradle.api.tasks.testing.logging.TestExceptionFormat.FULL
    }
}
