package com.cesarpetrescu.ledger

import android.content.Context
import android.view.inputmethod.InputMethodManager
import java.io.OutputStream
import androidx.compose.ui.test.*
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import androidx.test.runner.lifecycle.ActivityLifecycleMonitorRegistry
import androidx.test.runner.lifecycle.Stage
import androidx.test.core.app.ActivityScenario
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.*
import org.junit.Assume.assumeTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test

@OptIn(ExperimentalTestApi::class)
class OwnerFlowTest {
    @get:Rule val ui = createEmptyComposeRule()
    private val context get() = InstrumentationRegistry.getInstrumentation().targetContext
    @Before fun reset() { SessionStore(context).clear() }

    private fun awaitText(text: String) {
        ui.waitUntilAtLeastOneExists(hasText(text), 15_000)
        ui.waitForIdle()
    }
    private fun tap(text: String) {
        awaitText(text)
        // Use the rule's synchronization, not an eager semantics-tree walk
        // inside a waitUntil callback while Android is measuring another frame.
        ui.waitUntilDoesNotExist(SemanticsMatcher.keyIsDefined(SemanticsProperties.LiveRegion), 15_000)
        ui.onNodeWithText(text).performClick()
        ui.waitForIdle()
    }
    private fun scrollTo(text: String) {
        ui.waitForIdle()
        ui.runOnUiThread {
            val activity = ActivityLifecycleMonitorRegistry.getInstance()
                .getActivitiesInStage(Stage.RESUMED).single()
            val view = activity.currentFocus ?: activity.window.decorView
            (activity.getSystemService(Context.INPUT_METHOD_SERVICE) as InputMethodManager)
                .hideSoftInputFromWindow(view.windowToken, 0)
            view.clearFocus()
            WindowCompat.getInsetsController(activity.window, activity.window.decorView)
                .hide(WindowInsetsCompat.Type.ime())
        }
        ui.waitForIdle()
        // Exercise actual touch scrolling. performScrollToNode traverses lazy
        // layout semantics on the instrumentation thread; that path raced the
        // API-28 draw pass in run 34345248347. Do not disable the observer check.
        repeat(24) {
            val visible = try { ui.onNodeWithText(text).isDisplayed() }
                catch (_: AssertionError) { false }
            if (visible) return
            ui.onNodeWithTag("page").performTouchInput {
                swipe(center.copy(y = height * 0.75f), center.copy(y = height * 0.30f), 300)
            }
            ui.waitForIdle()
        }
        ui.onNodeWithText(text).assertIsDisplayed()
    }
    private fun recreate(activity: ActivityScenario<MainActivity>) {
        ui.waitForIdle()
        activity.recreate()
        InstrumentationRegistry.getInstrumentation().waitForIdleSync()
        ui.waitForIdle()
    }

    @Test fun sessionStorageIsEncryptedAndTamperingFailsClosed() {
        val store = SessionStore(context)
        val client = Api("https://ledger.example.com", "ledger_admin_session=" + "a".repeat(43), "b".repeat(43))
        store.save(client)
        assertEquals(client.cookie, store.read()?.cookie)
        assertEquals(client.origin, store.read()?.origin)
        assertEquals(client.csrf, store.read()?.csrf)
        val preferences = context.getSharedPreferences("session", Context.MODE_PRIVATE)
        val disk = preferences.getString("encrypted", "")!!
        assertFalse(disk.contains(client.cookie))
        assertFalse(disk.contains(client.origin))
        preferences.edit().putString("encrypted", disk.reversed()).commit()
        assertNull(store.read())
        assertFalse(preferences.contains("encrypted"))
    }

    @Test fun nativeSignInRejectsCleartext() {
        ActivityScenario.launch(MainActivity::class.java).use {
            awaitText("Server address")
            ui.onNodeWithText("Server address").performTextInput("http://example.com")
            scrollTo("Owner password")
            ui.onNodeWithText("Owner password").performTextInput("fixture-password")
            scrollTo("Sign in")
            tap("Sign in")
            awaitText("Use an HTTPS server address without a path, password, or query.")
            ui.onNodeWithText("Owner password").assertExists()
        }
    }

    @Test fun ownerWorkflowOverHttps() {
        assumeTrue(InstrumentationRegistry.getArguments().getString("fixture") == "true")
        val client = Api("https://localhost:8443").login("fixture-password")
        try { client.request("GET", "/redirect-test"); fail("Followed a redirect") }
        catch (e: ApiError) { assertEquals(302, e.status) }
        var downloaded = 0L
        client.download("/large-export", object : OutputStream() {
            override fun write(value: Int) { downloaded++ }
            override fun write(bytes: ByteArray, offset: Int, length: Int) { downloaded += length }
        })
        assertEquals(28L * 1024 * 1024, downloaded)
        val large = client.request("GET", handoffPath("large")).rows("messages")
        assertEquals(10, large.size)
        assertEquals(100000, large.first().text("body").length)
        ActivityScenario.launch(MainActivity::class.java).use { activity ->
            awaitText("Server address")
            ui.onNodeWithText("Server address").performTextInput("https://localhost:8443")
            scrollTo("Owner password")
            ui.onNodeWithText("Owner password").performTextInput("fixture-password")
            scrollTo("Sign in")
            tap("Sign in")
            awaitText("A clear view of your work.")
            recreate(activity)
            awaitText("A clear view of your work.")
            tap("Projects")
            tap("Atlas")
            tap("Add entry")
            ui.onNodeWithText("Entry").performTextInput("Android verification note")
            recreate(activity)
            ui.onNodeWithText("Android verification note").assertExists()
            scrollTo("Add entry")
            tap("Add entry")
            awaitText("The server could not complete the request (503).")
            ui.onNodeWithText("Android verification note").assertExists()
            tap("Add entry")
            awaitText("Session expired")
            ui.onNodeWithText("Owner password").performTextInput("fixture-password")
            tap("Sign in again")
            ui.waitUntilDoesNotExist(hasText("Session expired"), 15_000)
            ui.onNodeWithText("Android verification note").assertExists()
            tap("Add entry")
            awaitText("Entries")
            scrollTo("Android verification note")
            ui.onNodeWithText("Android verification note").assertExists()
            ui.onNodeWithContentDescription("Back").performClick()
            tap("Handoffs")
            tap("Atlas handoff")
            awaitText("Add message")
            scrollTo("Publish")
            tap("Publish")
            awaitText("Claim")
            tap("Claim")
            awaitText("Complete")
            ui.onNodeWithText("Retarget").assertDoesNotExist()
            scrollTo("Complete")
            tap("Complete")
            awaitText("Reopen")
            ui.onNodeWithContentDescription("Back").performClick()
            tap("Calendar")
            tap("Plan the week")
            awaitText("Title")
            ui.onNodeWithText("Title").performTextReplacement("Updated planning session")
            scrollTo("Save event")
            tap("Save event")
            awaitText("Updated planning session")
            tap("Search")
            ui.onNodeWithText("Search your work").performTextInput("Atlas")
            ui.onNodeWithTag("search-submit").performClick()
            awaitText("Atlas search result")
            ui.onNodeWithContentDescription("Settings").performClick()
            tap("Connected clients")
            tap("Revoke access")
            tap("Cancel")
            ui.onNodeWithContentDescription("Back").performClick()
            scrollTo("Sign out")
            tap("Sign out")
            ui.onNode(hasText("Sign out") and hasClickAction() and hasAnyAncestor(isDialog())).performClick()
            awaitText("Server address")
            assertNull(SessionStore(context).read())
        }
    }
}
