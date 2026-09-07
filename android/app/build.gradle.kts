plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.compose.compiler)
}

val releaseVersion = providers.gradleProperty("releaseVersion").getOrElse("0.1.0")
val parts = Regex("(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)").matchEntire(releaseVersion)
    ?.groupValues?.drop(1)?.map(String::toLong) ?: error("Use a MAJOR.MINOR.PATCH releaseVersion")
require(parts[0] <= 2000 && parts[1] < 1000 && parts[2] < 1000) { "Version exceeds Android versionCode limits" }
val releaseCode = parts[0] * 1_000_000 + parts[1] * 1000 + parts[2]
require(releaseCode in 1..2_100_000_000) { "Version code must be positive" }

android {
    namespace = "com.cesarpetrescu.ledger"
    compileSdk = 36
    defaultConfig {
        applicationId = "com.cesarpetrescu.ledger"
        minSdk = 28
        targetSdk = 36
        versionCode = releaseCode.toInt()
        versionName = releaseVersion
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
    }
    signingConfigs {
        create("release") {
            providers.environmentVariable("ANDROID_KEYSTORE_PATH").orNull?.let { storeFile = file(it) }
            storePassword = providers.environmentVariable("ANDROID_KEYSTORE_PASSWORD").orNull
            keyAlias = providers.environmentVariable("ANDROID_KEY_ALIAS").orNull
            keyPassword = providers.environmentVariable("ANDROID_KEY_PASSWORD").orNull
        }
    }
    buildTypes {
        debug { applicationIdSuffix = ".debug"; versionNameSuffix = "-debug" }
        release {
            signingConfig = signingConfigs.getByName("release")
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"))
        }
    }
    // Only local debug builds can opt in to an ephemeral test CA.
    providers.gradleProperty("testCaDir").orNull?.let { sourceSets.getByName("debug").res.srcDir(it) }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_21
        targetCompatibility = JavaVersion.VERSION_21
    }
    buildFeatures { compose = true; buildConfig = true }
    packaging { resources.excludes += "/META-INF/{AL2.0,LGPL2.1}" }
}

kotlin { jvmToolchain(21) }

dependencies {
    implementation(platform(libs.androidx.compose.bom))
    androidTestImplementation(platform(libs.androidx.compose.bom))
    implementation(libs.androidx.core.ktx)
    implementation(libs.androidx.lifecycle.runtime.ktx)
    implementation(libs.androidx.activity.compose)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.compose.ui)
    implementation(libs.androidx.compose.material3)
    debugImplementation(libs.androidx.compose.ui.tooling)
    debugImplementation(libs.androidx.compose.ui.test.manifest)
    testImplementation(libs.junit)
    androidTestImplementation(libs.androidx.compose.ui.test.junit4)
    androidTestImplementation(libs.androidx.test.ext.junit)
    androidTestImplementation(libs.androidx.test.runner)
    androidTestImplementation(libs.androidx.test.espresso.core)
}
