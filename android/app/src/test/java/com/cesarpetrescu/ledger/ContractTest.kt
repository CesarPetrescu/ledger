package com.cesarpetrescu.ledger

import org.junit.Assert.*
import org.junit.Test

class ContractTest {
    @Test fun serverAndSessionBoundaries() {
        assertEquals("https://ledger.example.com", serverOrigin(" HTTPS://LEDGER.EXAMPLE.COM:443/ "))
        assertEquals("https://localhost:8443", serverOrigin("https://localhost:8443"))
        assertEquals("https://[::1]:8443", serverOrigin("https://[::1]:8443/"))
        listOf("http://example.com", "https://user:pass@example.com", "https://example.com/admin", "https://example.com?x=1", "https://example.com#x", "https://example.com:0", "https://example.com:65536", "https://example.com\\@evil.test", "javascript:alert(1)").forEach {
            assertThrows(IllegalArgumentException::class.java) { serverOrigin(it) }
        }
        val value = "a".repeat(43)
        assertEquals("ledger_admin_session=$value", sessionCookie(listOf("other=abc; Secure", "ledger_admin_session=$value; Path=/admin; Secure; HttpOnly; SameSite=Strict")))
        listOf("ledger_admin_session=$value; Path=/admin", "ledger_admin_session=$value; Path=/; Secure", "ledger_admin_session=bad; Path=/admin; Secure").forEach {
            assertThrows(IllegalStateException::class.java) { sessionCookie(listOf(it)) }
        }
        assertEquals("a%2Fb%20%26%3F%23", segment("a/b &?#"))
        assertArrayEquals(byteArrayOf(1, 2), byteArrayOf(1, 2).inputStream().readBounded(2))
        assertThrows(IllegalArgumentException::class.java) { byteArrayOf(1, 2, 3).inputStream().readBounded(2) }
    }
    @Test fun formBoundariesMatchServerValidation() {
        listOf("atlas", "a-", "a1", "a".repeat(64)).forEach { assertTrue(validProjectSlug(it)) }
        listOf("-atlas", "--", "a", "a".repeat(65), "Atlas").forEach { assertFalse(validProjectSlug(it)) }
        assertTrue(validFieldText("😀".repeat(100), 100, false))
        assertFalse(validFieldText("😀".repeat(101), 100, false))
        assertFalse(validFieldText("target\nother", 100, false))
        assertTrue(validFieldText("line\nline", 100, true))
        assertTrue(validFieldText("a".repeat(200), 200, false))
        assertFalse(validFieldText("a".repeat(201), 200, false))
        assertTrue(canRetarget("draft"))
        assertTrue(canRetarget("ready"))
        listOf("in_progress", "blocked", "done").forEach { assertFalse(canRetarget(it)) }
        assertFalse(canRetarget("ready", claimed = true))
    }
    @Test fun messageStateTransitionsMatchOwnerConsole() {
        assertEquals(listOf("publish"), messageActions("draft", "unseen"))
        assertEquals(listOf("acknowledge", "claim"), messageActions("ready", "unseen"))
        assertEquals(listOf("block", "complete", "release"), messageActions("in_progress", "seen"))
        assertEquals(listOf("complete" to "Accept", "release" to "Send back"), researchActions("blocked", "review", brief = true))
        assertEquals(listOf("release" to "Retry"), researchActions("blocked", "dead", brief = true))
        assertEquals(emptyList<Pair<String, String>>(), researchActions("ready", "", brief = true))
        assertEquals(emptyList<Pair<String, String>>(), researchActions("done", "", brief = false))
        assertEquals(listOf("publish" to "Publish"), researchActions("draft", "", brief = false))
        assertEquals("3 of 3 failed runs. Last error: exit 137. Retry gives it a fresh set of attempts.", researchHint("blocked", "dead", 3, 3, 3, "", "exit 137"))
        assertEquals("Run 2 · A sandbox is working on it.", researchHint("in_progress", "", 2, 0, 3, "", ""))
        assertEquals(listOf("complete", "release"), messageActions("blocked", "seen"))
        assertEquals(listOf("reopen"), messageActions("done", "seen"))
    }

    @Test fun handoffListShowsResearchStateInsteadOfMessageCounts() {
        val research = org.json.JSONObject("""{"kind":"research","research_status":"review","ready_count":0,"in_progress_count":0,"done_count":0}""")
        // The state is a coloured label, amber when it waits on the owner; the empty message counts are left out.
        assertEquals("", handoffProgress(research))
        assertEquals(listOf("Research" to Tone.Neutral, "Ready for review" to Tone.Warn), researchTags(research))
        assertEquals("Question for you" to Tone.Warn, researchTags(org.json.JSONObject("""{"kind":"research","research_status":"question"}"""))[1])
        assertEquals("Queued" to Tone.Accent, researchTags(org.json.JSONObject("""{"kind":"research","research_status":"queued"}"""))[1])
        // A server without research_status still marks the task as research.
        assertEquals(listOf("Research" to Tone.Neutral), researchTags(research.put("research_status", org.json.JSONObject.NULL)))
        assertEquals(listOf("Draft", "Queued", "Running", "Ready for review", "Question for you", "Stopped", "Accepted"),
            listOf("draft", "queued", "running", "review", "question", "stopped", "accepted").map(::researchStatusLabel))
        val general = org.json.JSONObject("""{"kind":"general","draft_count":0,"ready_count":2,"in_progress_count":1,"blocked_count":0,"done_count":0}""")
        assertEquals("2 ready · 1 in progress", handoffProgress(general))
        assertEquals(emptyList<Pair<String, Tone>>(), researchTags(general))
    }

    @Test fun researchThreadHeaderShowsTheListsStateLabel() {
        // The detail has the brief's work state and phase, not research_status; the mapping matches the server's.
        val states = listOf("draft" to "", "ready" to "", "in_progress" to "", "done" to "", "blocked" to "review", "blocked" to "question", "blocked" to "dead")
        assertEquals(listOf("draft", "queued", "running", "accepted", "review", "question", "stopped"), states.map { (work, phase) -> researchStatus(work, phase) })
        assertEquals("Ready for review" to Tone.Warn, researchStatusTag(researchStatus("blocked", "review")))
        assertEquals("Running" to Tone.Accent, researchStatusTag("running"))
        assertEquals("Accepted" to Tone.Good, researchStatusTag("accepted"))
        assertEquals("Stopped" to Tone.Neutral, researchStatusTag("stopped"))
        assertNull(researchStatusTag(""))
    }

    @Test fun replyStepsBackWhileAcceptOrQueueLeads() {
        assertEquals(true, researchLeads("blocked", "review"))
        assertEquals(true, researchLeads("draft", ""))
        // Answering a question, Reply is the next step and stays filled.
        assertEquals(false, researchLeads("blocked", "question"))
        assertEquals(false, researchLeads("in_progress", ""))
    }

    @Test fun anUnpublishedResearchReplySaysDraft() {
        assertEquals("You · Draft", researchNoteTitle(org.json.JSONObject("""{"source":"ledger-admin","work_state":"draft"}""")))
        assertEquals("codex", researchNoteTitle(org.json.JSONObject("""{"source":"codex","work_state":"blocked"}""")))
    }

    @Test fun countsArePluralizedCorrectly() {
        assertEquals("1 open todo", plural(1, "open todo"))
        assertEquals("3 open todos", plural(3, "open todo"))
        assertEquals("1 needs you", plural(1, "needs you", "need you"))
        assertEquals("0 entries", plural(0, "entry", "entries"))
    }

    @Test fun helpCoversCalendarResearchReposAndAccess() {
        val help = helpSections.joinToString("\n") { "${it.first}\n${it.second}" }
        listOf("More › Calendar", "Handoffs › Research", "Accept", "Send back", "Files travel both ways", "Repos tab", "GitHub sync", "API keys",
            "Settings › Access › Connected apps", "Settings › Access › Connect an agent", "web console › Access › GitHub sync")
            .forEach { assertTrue(it, help.contains(it)) }
        // Access moved off the Agents page, which now only shows what agents did.
        listOf("Agents page", "Agents › API keys", "Agents › GitHub sync", "how to connect").forEach { assertFalse(it, help.contains(it)) }
        // Files and Repos moved from the project's ⋯ menu into its tabs.
        listOf("⋯ menu opens Files", "(from its ⋯ menu)").forEach { assertFalse(it, help.contains(it)) }
    }

    @Test
    fun repoCardsShowRoleBranchAndSyncedActivity() {
        val synced = org.json.JSONObject("""{"repo":"acme/atlas-api","url":"https://github.com/acme/atlas-api","provider":"github","role":"backend","branch":"release","path":"api",
            "note":"Deploys from tags","added_by":"claude-code","sync":{"head_at":"2026-10-08T09:30:00Z","head_message":"Fix login","open_prs":1,"latest_release":"v1.2.0"}}""")
        val (subtitle, body) = repoSummary(synced)
        assertEquals("backend · branch release", subtitle)
        assertTrue(body.contains("Folder: api"))
        assertTrue(body.contains(": Fix login · 1 open PR · release v1.2.0"))
        assertTrue(body.endsWith("Deploys from tags\nLinked by claude-code"))
        val failed = org.json.JSONObject("""{"repo":"acme/x","url":"git@github.com:acme/x.git","provider":"github","added_by":"ledger-admin","sync":{"error":"not found, or the token cannot read this repository"}}""")
        assertEquals("", repoSummary(failed).first)
        assertTrue(repoSummary(failed).second.contains("Sync: not found"))
        assertTrue(repoSummary(failed).second.endsWith("Linked by you"))
        val stale = org.json.JSONObject("""{"repo":"acme/x","url":"https://github.com/acme/x","provider":"github","added_by":"codex","sync":{"error":"GitHub answered HTTP 502","head_at":"2026-10-08T09:30:00Z","head_message":"Fix","open_prs":2}}""")
        assertTrue(repoSummary(stale).second.contains("Sync: GitHub answered HTTP 502\n"))
        assertTrue(repoSummary(stale).second.contains(": Fix · 2 open PRs"))
        val plain = org.json.JSONObject("""{"repo":"photon/panel","url":"http://forgejo.lan/photon/panel","provider":"git","added_by":"codex"}""")
        assertFalse(repoSummary(plain).second.contains("synced"))
    }
}
