plugins { id("com.android.application") }

val licenseAssets = tasks.register<Sync>("licenseAssets") {
    from(rootProject.file("../LICENSE"), rootProject.file("../NOTICE"))
    into(layout.buildDirectory.dir("generated/licenseAssets/licenses"))
}

android {
    namespace = "chat.localmessenger.android"
    compileSdk = 36
    defaultConfig {
        applicationId = "chat.localmessenger.android"
        minSdk = 26
        targetSdk = 36
        versionCode = 1
        versionName = "0.1.0"
    }
    compileOptions { sourceCompatibility = JavaVersion.VERSION_17; targetCompatibility = JavaVersion.VERSION_17 }
    buildTypes {
        release { isMinifyEnabled = true; isShrinkResources = true; proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro") }
    }
    splits {
        abi {
            isEnable = true
            reset()
            include("arm64-v8a", "x86_64")
            isUniversalApk = false
        }
    }
    packaging { jniLibs { useLegacyPackaging = false } }
    sourceSets.getByName("main").assets.srcDir(layout.buildDirectory.dir("generated/licenseAssets").get().asFile)
}

tasks.named("preBuild").configure { dependsOn(licenseAssets) }

dependencies {
    implementation(files("libs/messenger.aar"))
    testImplementation("junit:junit:4.13.2")
}
