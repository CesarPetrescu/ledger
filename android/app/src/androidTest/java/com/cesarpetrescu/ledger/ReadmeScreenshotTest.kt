package com.cesarpetrescu.ledger

import android.content.Context
import android.graphics.Bitmap
import android.os.ParcelFileDescriptor
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.test.core.app.ActivityScenario
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assume.assumeTrue
import org.junit.Rule
import org.junit.Test

/** Captures the actual authenticated Overview screen for README documentation. */
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
                val instrumentation = InstrumentationRegistry.getInstrumentation()
                val screenshot = instrumentation.uiAutomation.takeScreenshot()
                context.openFileOutput("readme-overview.png", Context.MODE_PRIVATE).use { output ->
                    check(screenshot.compress(Bitmap.CompressFormat.PNG, 100, output))
                }
                screenshot.recycle()

                // connectedDebugAndroidTest removes the APK before smoke.sh can
                // run-as it. Copy the real framebuffer out while the package is
                // still installed; reading to EOF waits for the shell command.
                val command = instrumentation.uiAutomation.executeShellCommand(
                    "sh -c 'run-as com.cesarpetrescu.ledger cat files/readme-overview.png > /sdcard/ledger-readme-overview.png'",
                )
                ParcelFileDescriptor.AutoCloseInputStream(command).use { it.readBytes() }
            }
        } finally {
            SessionStore(context).clear()
        }
    }
}
