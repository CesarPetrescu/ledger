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
}
