package com.cesarpetrescu.ledger

import android.graphics.BitmapFactory
import android.os.ParcelFileDescriptor
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.test.core.app.ActivityScenario
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assume.assumeTrue
import org.junit.Rule
import org.junit.Test

/** Captures the real authenticated application; only its data is a CI fixture. */
@OptIn(ExperimentalTestApi::class)
class ReadmeScreenshotTest {
    @get:Rule val ui = createEmptyComposeRule()
    private val context get() = InstrumentationRegistry.getInstrumentation().targetContext

    @Test fun captureOverviewForReadme() {
        assumeTrue(InstrumentationRegistry.getArguments().getString("fixture") == "true")
        val session = Api("https://localhost:8443").login("fixture-password")
        SessionStore(context).save(session)
        try {
            ActivityScenario.launch(MainActivity::class.java).use {
                ui.waitUntilAtLeastOneExists(hasText("A clear view of your work."), 15_000)
                ui.waitForIdle()
                val automation = InstrumentationRegistry.getInstrumentation().uiAutomation
                val path = "/data/local/tmp/ledger-readme-overview.png"
                // Execute a single command, without shell quoting/redirection.
                // Shell-owned storage survives Gradle's APK uninstall and is
                // readable by adb without changing app permissions or using root.
                ParcelFileDescriptor.AutoCloseInputStream(
                    automation.executeShellCommand("screencap -p $path"),
                ).use { it.readBytes() }
                val bytes = ParcelFileDescriptor.AutoCloseInputStream(
                    automation.executeShellCommand("cat $path"),
                ).use { it.readBytes() }
                check(bytes.size > 1024) { "Screenshot export returned no image" }
                val image = checkNotNull(BitmapFactory.decodeByteArray(bytes, 0, bytes.size)) {
                    "Screenshot export is not a decodable PNG"
                }
                try {
                    check(image.width >= 320 && image.height > image.width) {
                        "Expected a portrait Android framebuffer"
                    }
                } finally {
                    image.recycle()
                }
            }
        } finally {
            SessionStore(context).clear()
        }
    }
}
