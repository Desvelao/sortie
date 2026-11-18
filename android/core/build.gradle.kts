plugins {
    alias(libs.plugins.kotlin.jvm)
}

kotlin {
    jvmToolchain(17)
}

dependencies {
    implementation(libs.snakeyaml.engine)
    testImplementation(kotlin("test"))
}
