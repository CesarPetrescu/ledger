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
    private fun fillField(label: String, value: String) {
        awaitText(label)
        // Request focus separately, then let IME/layout settle before resolving
        // the editable node again. Combining both operations retained a stale
        // lazy-list node on API 28 when keyboard insets changed its layout.
        ui.onNodeWithText(label).performClick()
        ui.waitForIdle()
        ui.waitUntilExactlyOneExists(hasText(label) and isFocused(), 15_000)
        ui.onNode(hasText(label) and isFocused()).performTextReplacement(value)
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
    /** Runs network work off the main thread, as the system job does. */
    private fun <T> onBackground(block: () -> T): T {
        var result: Result<T>? = null
        val thread = Thread { result = runCatching(block) }
        thread.start()
        thread.join(30_000)
        return result!!.getOrThrow()
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
            // The login shows Ledger's mark and wordmark, as the web does.
            ui.onNodeWithTag("ledger-logo").assertIsDisplayed()
            ui.onNodeWithText("Ledger").assertIsDisplayed()
            fillField("Server address", "http://example.com")
            scrollTo("Owner password")
            fillField("Owner password", "fixture-password")
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
            fillField("Server address", "https://localhost:8443")
            scrollTo("Owner password")
            fillField("Owner password", "fixture-password")
            scrollTo("Sign in")
            tap("Sign in")
            awaitText("Confirm the fixture pricing")
            recreate(activity)
            awaitText("Confirm the fixture pricing")
            if (android.os.Build.VERSION.SDK_INT >= 33) {
                // Denied for good, Android shows no dialog: Turn on keeps the wish and opens Android's settings.
                val automation = InstrumentationRegistry.getInstrumentation().uiAutomation
                val shell = { command: String -> android.os.ParcelFileDescriptor.AutoCloseInputStream(automation.executeShellCommand(command)).use { it.readBytes() } }
                val resumed = { var ours = false
                    InstrumentationRegistry.getInstrumentation().runOnMainSync { ours = ActivityLifecycleMonitorRegistry.getInstance().getActivitiesInStage(Stage.RESUMED).isNotEmpty() }
                    ours }
                val until = { done: () -> Boolean -> val deadline = System.currentTimeMillis() + 10_000
                    while (!done() && System.currentTimeMillis() < deadline) Thread.sleep(100)
                    assertTrue(done()) }
                val fixed = "${context.packageName} android.permission.POST_NOTIFICATIONS user-fixed"
                shell("pm set-permission-flags $fixed")
                tap("Turn on")
                until { Notifier.enabled(context) }
                until { !resumed() }
                shell("input keyevent KEYCODE_BACK")
                until { resumed() }
                Notifier.setEnabled(context, false)
                shell("pm clear-permission-flags $fixed")
                awaitText("Confirm the fixture pricing")
            }
            // Notifications: the first check only learns what is there; a new ask is announced once.
            if (android.os.Build.VERSION.SDK_INT >= 33) InstrumentationRegistry.getInstrumentation().uiAutomation
                .grantRuntimePermission(context.packageName, android.Manifest.permission.POST_NOTIFICATIONS)
            Notifier.setEnabled(context, true, schedule = false)
            assertEquals(0, onBackground { Notifier.check(context) })
            context.getSharedPreferences("notify", Context.MODE_PRIVATE).edit().remove("seen").commit()
            assertEquals(1, onBackground { Notifier.check(context) })
            // Posting is asynchronous; give the system a moment to list it.
            val manager = context.getSystemService(android.app.NotificationManager::class.java)
            val shown = { manager.activeNotifications.any { it.notification.extras.getString(android.app.Notification.EXTRA_TITLE) == "claude-code asks you" } }
            val deadline = System.currentTimeMillis() + 5_000
            while (!shown() && System.currentTimeMillis() < deadline) Thread.sleep(100)
            assertTrue(shown())
            assertEquals(0, onBackground { Notifier.check(context) })
            Notifier.setEnabled(context, false)
            context.getSystemService(android.app.NotificationManager::class.java).cancelAll()
            // Tapping it opens the entry, but only for the server it came from.
            val tapped = { origin: String -> activity.onActivity { it.startActivity(android.content.Intent(it, MainActivity::class.java)
                .putExtra(MainActivity.ROUTE, "entry-view/70").putExtra(MainActivity.ORIGIN, origin)
                .addFlags(android.content.Intent.FLAG_ACTIVITY_CLEAR_TOP or android.content.Intent.FLAG_ACTIVITY_SINGLE_TOP)) } }
            tapped("https://other.example")
            ui.waitForIdle()
            ui.onNodeWithText("Entry").assertDoesNotExist()
            tapped("https://localhost:8443")
            awaitText("Entry")
            // The system Back key, not a touch: a notification can briefly cover the top bar.
            androidx.test.espresso.Espresso.pressBack()
            awaitText("Confirm the fixture pricing")
            tap("Projects")
            tap("Atlas")
            tap("Add entry")
            fillField("Entry", "Android verification note")
            recreate(activity)
            ui.onNodeWithText("Android verification note").assertExists()
            scrollTo("Add entry")
            tap("Add entry")
            awaitText("The server could not complete the request (503).")
            ui.onNodeWithText("Android verification note").assertExists()
            tap("Add entry")
            awaitText("Session expired")
            fillField("Owner password", "fixture-password")
            tap("Sign in again")
            ui.waitUntilDoesNotExist(hasText("Session expired"), 15_000)
            ui.onNodeWithText("Android verification note").assertExists()
            tap("Add entry")
            awaitText("Activity")
            scrollTo("Android verification note")
            ui.onNodeWithText("Android verification note").assertExists()
            // The top bar names the project; Repos keeps its link form folded until asked for, and folds it again after linking.
            awaitText("Atlas")
            ui.onNodeWithContentDescription("More actions for Atlas").performClick()
            tap("Repos")
            awaitText("Atlas · Repos")
            awaitText("No repositories linked yet.")
            ui.onNodeWithText("Repository URL").assertDoesNotExist()
            // The empty state reads before the button it motivates.
            assertTrue(ui.onNodeWithText("No repositories linked yet.").fetchSemanticsNode().boundsInRoot.top < ui.onNodeWithText("Link a repository").fetchSemanticsNode().boundsInRoot.top)
            tap("Link a repository")
            fillField("Repository URL", "https://github.com/acme/atlas")
            scrollTo("Link repository")
            tap("Link repository")
            awaitText("acme/atlas")
            ui.waitUntilDoesNotExist(hasText("Repository URL"), 15_000)
            tap("Unlink")
            tap("Cancel")
            ui.onNodeWithContentDescription("Back").performClick()
            ui.onNodeWithContentDescription("Back").performClick()
            tap("Handoffs")
            tap("Atlas handoff")
            awaitText("Add message")
            // Messages read as Markdown (no raw HTML); the switch shows them exactly as written.
            awaitText("Atlas plan")
            awaitText("Review the plan")
            ui.onNodeWithText("☑").assertExists()
            ui.onNodeWithText("<b>", substring = true).assertDoesNotExist()
            ui.onNode(isToggleable()).performClick()
            ui.waitUntilAtLeastOneExists(hasText("# Atlas plan", substring = true), 15_000)
            ui.onNode(isToggleable()).performClick()
            awaitText("Review the plan")
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
            tap("More")
            tap("Calendar")
            tap("Plan the week")
            awaitText("Title")
            fillField("Title", "Updated planning session")
            scrollTo("Save event")
            tap("Save event")
            awaitText("Updated planning session")
            ui.onNodeWithContentDescription("Back").performClick()
            tap("Search")
            fillField("Search your work", "Atlas")
            ui.onNodeWithTag("search-submit").performClick()
            awaitText("Atlas search result")
            // An entry hit opens the entry itself.
            scrollTo("Atlas decision hit")
            tap("Atlas decision hit")
            awaitText("Use SQLite for the fixture cache")
            awaitText("Chose")
            ui.onNodeWithContentDescription("Back").performClick()
            awaitText("Atlas search result")
            ui.onNodeWithContentDescription("Back").performClick()
            tap("Inbox")
            // The inbox says why labelling is paused, why each item is there, and what labels mean.
            awaitText("AI labelling is paused: can't reach the AI model. 1 entry is waiting and will get titles and labels when it is back.")
            tap("What do the labels mean?")
            awaitText("What the labels mean")
            tap("Close")
            // Inbox: answer an ask from its sheet (its history shows where it came from), then complete a todo.
            tap("Confirm the fixture pricing")
            ui.waitUntilAtLeastOneExists(hasText("claude-code wrote it through Fixture Agent", substring = true), 15_000)
            // The question sits above the box that answers it.
            val question = ui.onNodeWithText("Confirm the fixture pricing").fetchSemanticsNode().boundsInRoot.top
            assertTrue(question < ui.onNodeWithText("Answer claude-code").fetchSemanticsNode().boundsInRoot.top)
            fillField("Answer claude-code", "The fixture pricing is right.")
            tap("Send")
            awaitText("Nothing is waiting on you.")
            // The meta line and the labels say why the todo is here; no "Why:" line repeats them.
            ui.onNodeWithText("Why:", substring = true).assertDoesNotExist()
            // Correct a label; the row then shows it and no longer asks for a check.
            tap("Write the fixture todo")
            awaitText("☑ Draft the fixture")
            tap("Edit labels")
            fillField("Category · AI unsure", "fixtures")
            tap("Save")
            awaitText("fixtures")
            ui.onNodeWithText("Check").assertDoesNotExist()
            tap("Write the fixture todo")
            tap("Mark done")
            awaitText("No open todos. Nice.")
            // Reading: mark the only unread item read.
            tap("Reading")
            tap("Fixture model ships")
            tap("Mark read")
            awaitText("Nothing left to read.")
            // Undo straight from the notice, then again from Recent actions.
            awaitText("Undo")
            ui.onNodeWithText("Undo").performClick()
            awaitText("Fixture model ships")
            tap("Fixture model ships")
            tap("Mark read")
            awaitText("Nothing left to read.")
            tap("More")
            tap("Recent actions")
            awaitText("Done: Write the fixture todo")
            ui.onAllNodesWithText("Undo")[0].performClick()
            awaitText("Undone")
            ui.onNodeWithContentDescription("Back").performClick()
            tap("Reading")
            awaitText("Fixture model ships")
            tap("More")
            tap("Trash")
            awaitText("Trash is empty.")
            ui.onNodeWithContentDescription("Back").performClick()
            // The Table lists every project's entries, by view and filter.
            tap("Table")
            awaitText("Android verification note")
            tap("Decisions")
            awaitText("Use SQLite for the fixture cache")
            ui.onNodeWithContentDescription("Filters").performClick()
            awaitText("All projects")
            tap("Use SQLite for the fixture cache")
            awaitText("Chose")
            androidx.test.espresso.Espresso.pressBack()
            ui.waitUntilDoesNotExist(hasText("Chose"), 15_000)
            ui.onNodeWithContentDescription("Back").performClick()
            // Agents shows only what each agent did; connecting one lives under Settings › Access.
            scrollTo("Agents")
            tap("Agents")
            awaitText("Working on 1 handoff")
            ui.onNodeWithText("Connect an agent").assertDoesNotExist()
            tap("Manage access →")
            // Settings opens at its Access heading, not at Appearance, with no cut-off end of Notifications above it.
            awaitText("Connect an agent")
            ui.onNodeWithText("Access").assertIsDisplayed()
            ui.onAllNodesWithText("nothing goes through Google", substring = true).let { notes ->
                if (notes.fetchSemanticsNodes().isNotEmpty()) notes.onFirst().assertIsNotDisplayed()
            }
            ui.onNodeWithText("Connect an agent").assertIsDisplayed()
            tap("Connect an agent")
            awaitText("ledger connect codex --server https://localhost:8443")
            ui.onNodeWithContentDescription("Back").performClick()
            ui.onNodeWithContentDescription("Back").performClick()
            ui.onNodeWithContentDescription("Back").performClick()
            // Help explains the rules.
            scrollTo("Help")
            tap("Help")
            awaitText("How Ledger works")
            // Help is a lazy list: on a short screen (CI's Pixel 2) later sections exist only once scrolled to.
            scrollTo("How the Inbox decides")
            ui.onNodeWithContentDescription("Back").performClick()
            ui.onNodeWithContentDescription("Settings").performClick()
            tap("Dark")
            tap("System")
            ui.onNodeWithContentDescription("Back").performClick()
            ui.onNodeWithContentDescription("Settings").performClick()
            scrollTo("Connected apps")
            tap("Connected apps")
            tap("Revoke access")
            tap("Cancel")
            ui.onNodeWithContentDescription("Back").performClick()
            // Sign-out sits under its own heading, not inside Access.
            scrollTo("This phone")
            scrollTo("Sign out")
            tap("Sign out")
            ui.onNode(hasText("Sign out") and hasClickAction() and hasAnyAncestor(isDialog())).performClick()
            awaitText("Server address")
            assertNull(SessionStore(context).read())
        }
    }
}
