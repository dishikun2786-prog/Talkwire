// The Android Gradle plugin and the Kotlin plugin are put on the build
// classpath here once, so the modules can apply them by id without a version
// (and without resolving plugin marker artifacts). Versions follow the source
// project: AGP 8.4.2, Kotlin 1.9.24.
buildscript {
    repositories {
        google()
        mavenCentral()
    }
    dependencies {
        classpath("com.android.tools.build:gradle:8.4.2")
        classpath("org.jetbrains.kotlin:kotlin-gradle-plugin:1.9.24")
    }
}
