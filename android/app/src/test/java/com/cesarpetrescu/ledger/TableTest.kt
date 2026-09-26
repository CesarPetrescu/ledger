package com.cesarpetrescu.ledger

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Test
import java.time.LocalDate

class TableTest {
    private fun entry(id: String, duplicateOf: String = "", body: String = "Body $id", title: String? = null) = JSONObject().apply {
        put("id", id); put("body", body)
        if (duplicateOf.isNotBlank()) put("duplicate_of", duplicateOf)
        if (title != null) put("meta", JSONObject().put("title", title))
    }

    @Test fun viewsFixKindAndTodoStateAndEncodeFilters() {
        assertEquals("q=a%20b%26c&kind=todo&status=open", tableQuery("todos", q = " a b&c ", status = "open", kind = "note"))
        assertEquals("project=atlas&kind=decision", tableQuery("decisions", project = "atlas", status = "open"))
        assertEquals("source=codex&tag=deploy", tableQuery("activity", source = "codex", tag = "deploy"))
        assertEquals("kind=status", tableQuery("activity", kind = "status"))
    }

    @Test fun repeatsFoldUnderTheNewestLoadedCopyEvenWhenTheRootIsNotLoaded() {
        val folded = foldRepeats(listOf(entry("9", "2"), entry("5"), entry("8", "2"), entry("2"), entry("1", "0"), entry("-1", "0")))
        assertEquals(listOf("9", "5", "1"), folded.map { it.entry.text("id") })
        assertEquals(listOf("8", "2"), folded[0].repeats.map { it.text("id") })
        assertEquals(listOf("-1"), folded[2].repeats.map { it.text("id") })
    }

    @Test fun runsGroupOnlyConsecutiveItems() {
        assertEquals(listOf("a" to listOf(1, 2), "b" to listOf(3), "a" to listOf(4)), runsBy(listOf(1, 2, 3, 4)) { if (it == 3) "b" else "a" })
    }

    @Test fun titlesPreferExtractionThenFirstLine() {
        assertEquals("Shipped export", entryTitle(entry("1", body = "long text", title = "Shipped export")))
        assertEquals("First line", entryTitle(entry("2", body = "  First line\nsecond")))
        assertEquals("x".repeat(109) + "…", entryTitle(entry("3", body = "x".repeat(200))))
    }

    @Test fun dayLabelsAreRelativeForTodayAndYesterday() {
        val today = LocalDate.now()
        assertEquals("Today", dayLabel(today.atStartOfDay(java.time.ZoneId.systemDefault()).plusHours(12).toOffsetDateTime().toString(), today))
        assertEquals("Yesterday", dayLabel(today.minusDays(1).atStartOfDay(java.time.ZoneId.systemDefault()).plusHours(12).toOffsetDateTime().toString(), today))
        assertEquals("not a date", dayLabel("not a date", today))
    }

    @Test fun projectRoutesKeepEntryIdentitySafelyEncoded() {
        val route = projectRoute("atlas", "activity", "Ship v2/3 & more")
        assertEquals("project/atlas/activity/Ship%20v2%2F3%20%26%20more", route)
        assertEquals(listOf("project", "atlas", "activity", "Ship v2/3 & more"), route.split('/').map { java.net.URLDecoder.decode(it, Charsets.UTF_8.name()) })
    }

    @Test fun readingAndRoutineFiltersFollowTheView() {
        assertEquals("reading=unread", tableQuery("reading"))
        assertEquals("reading=starred", tableQuery("reading", reading = "starred", hideRoutine = true))
        assertEquals("project=atlas&hide_routine=1", tableQuery("activity", project = "atlas", hideRoutine = true))
        assertEquals("kind=todo&status=open", tableQuery("todos", status = "open", hideRoutine = true))
    }

    private fun focus(kind: String, meta: JSONObject, created: String = "2026-09-20T10:00:00Z", resolved: Boolean = false, owner: JSONObject? = null) = JSONObject().apply {
        put("id", "1"); put("kind", kind); put("body", "x"); put("created_at", created); put("meta", meta)
        if (resolved) put("resolved_by", JSONObject().put("entry_id", "2"))
        if (owner != null) put("owner", owner)
    }

    @Test fun labelsSummarizeTheEntryInPriorityOrder() {
        val today = LocalDate.parse("2026-09-26")
        val now = java.time.OffsetDateTime.parse("2026-09-26T12:00:00Z")
        val meta = JSONObject().put("title", "t").put("ask", "Confirm pricing").put("importance", "important").put("priority", "high").put("size", "M").put("due", "2026-09-25")
        val overdue = "Overdue " + LocalDate.parse("2026-09-25").format(java.time.format.DateTimeFormatter.ofPattern("d MMM"))
        assertEquals(listOf("Asks you", "Important", "High", "M", overdue, "Stale"),
            focusLabels(focus("todo", meta, created = "2026-09-01T10:00:00Z"), today, now).map { it.first })
        // Handled asks, resolved todos, and fresh todos drop those labels.
        assertEquals(listOf("Important", "High", "M"),
            focusLabels(focus("todo", meta, resolved = true, owner = JSONObject().put("handled", true)), today, now).map { it.first })
        assertEquals(listOf("Blocked"), focusLabels(focus("status", JSONObject().put("title", "t").put("state", "blocked")), today, now).map { it.first })
        assertEquals(Tone.Danger, focusLabels(focus("status", JSONObject().put("state", "blocked")), today, now).single().second)
        assertEquals(emptyList<Pair<String, Tone>>(), focusLabels(JSONObject().put("kind", "note"), today, now))
    }

    @Test fun summariesAndAgesAreShort() {
        val entry = focus("note", JSONObject().put("gist", "Key fact").put("why", "Matters because"))
        assertEquals("Key fact", entrySummary(entry))
        assertEquals("Matters because", entrySummary(entry, reading = true))
        assertEquals("", entrySummary(JSONObject().put("kind", "note")))
        val now = java.time.OffsetDateTime.parse("2026-09-26T12:00:00Z")
        assertEquals("5m", ago("2026-09-26T11:55:00Z", now))
        assertEquals("3h", ago("2026-09-26T09:00:00Z", now))
        assertEquals("2d", ago("2026-09-24T12:00:00Z", now))
        assertEquals("3w", ago("2026-09-01T12:00:00Z", now))
    }
}
