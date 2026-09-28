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

    @Test fun historyReadsInPlainWordsWithTheOwnerAsYou() {
        val event = { kind: String, actor: String, text: String -> JSONObject().put("kind", kind).put("actor", actor).put("text", text) }
        assertEquals("codex wrote it through Codex CLI", historyLine(event("created", "codex", "Codex CLI")))
        assertEquals("You wrote it", historyLine(event("created", OWNER_SOURCE, "Browser")))
        assertEquals("You: snoozed 1 day (undone)", historyLine(event("action", OWNER_SOURCE, "Snoozed 1 day").put("undone", true)))
        assertEquals("You corrected next step", historyLine(event("labels", OWNER_SOURCE, "next_step")))
        assertEquals("You replied", historyLine(event("reply", OWNER_SOURCE, "Use 90 days")))
        assertEquals("claude-code replied", historyLine(event("reply", "claude-code", "Done")))
    }

    @Test fun monthGridStartsOnMondayAndShowsSixWeeks() {
        val days = monthDays(java.time.YearMonth.of(2026, 9))
        assertEquals(42, days.size)
        assertEquals(LocalDate.of(2026, 8, 31), days.first())
        assertEquals(java.time.DayOfWeek.MONDAY, days.first().dayOfWeek)
        assertEquals(true, LocalDate.of(2026, 9, 30) in days)
    }

    @Test fun calendarDaysHoldEventsTodosDeadlinesAndWakeUps() {
        val utc = java.time.ZoneOffset.UTC
        val first = LocalDate.of(2026, 9, 28)
        val last = first.plusDays(7)
        val trip = JSONObject().put("id", "trip").put("title", "Conference").put("all_day", true).put("start", "2026-09-20").put("end", "2026-09-30")
        val call = JSONObject().put("id", "call").put("title", "Call").put("all_day", false).put("start", "2026-09-29T09:00:00Z").put("end", "2026-09-29T10:00:00Z")
        val todo = JSONObject().put("id", "7").put("body", "Invoice").put("project_name", "Atlas").put("meta", JSONObject().put("due", "2026-09-28"))
        val woken = JSONObject().put("id", "8").put("body", "Later").put("owner", JSONObject().put("snoozed_until", "2026-10-01"))
        val projects = listOf(JSONObject().put("slug", "atlas").put("name", "Atlas").put("deadline", "2026-10-02"), JSONObject().put("slug", "x").put("name", "X").put("deadline", "daily"))
        val days = dayItems(listOf(trip, call), listOf(todo), listOf(woken), projects, first, last, today = LocalDate.of(2026, 9, 29), zone = utc)
        // A trip that began before the view shows on the days it still covers, and not after it ends.
        assertEquals("Conference", days[first]!!.first { it.kind == "event" }.title)
        assertEquals(true, days[LocalDate.of(2026, 9, 30)].orEmpty().none { it.title == "Conference" })
        assertEquals(listOf("All day", "09:00–10:00"), days[LocalDate.of(2026, 9, 29)]!!.map { it.time })
        assertEquals(true, days[first]!!.first { it.kind == "todo" }.overdue)
        assertEquals("entry-view/8", days[LocalDate.of(2026, 10, 1)]!!.single().route)
        assertEquals("Deadline", days[LocalDate.of(2026, 10, 2)]!!.single().time)
        // The trip twice (28th and 29th), the call, the todo, the wake-up, and the real deadline.
        assertEquals(6, days.values.sumOf { it.size })
    }

    @Test fun onlyAgentsAskYou() {
        val ask = { source: String -> JSONObject().put("source", source).put("meta", JSONObject().put("ask", "Pick one")) }
        assertEquals(true, asksYou(ask("codex")))
        assertEquals(false, asksYou(ask(OWNER_SOURCE)))
        assertEquals(false, asksYou(ask("codex").put("owner", JSONObject().put("handled", true))))
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

    @Test fun labelPatchSendsOnlyChangedFieldsAndSplitsTags() {
        val body = labelPatch(mapOf("title" to "Old", "tags" to "a, b", "ask" to ""), mapOf("title" to " Old ", "tags" to "a,  c d", "ask" to "Confirm it"))
        val set = body.getJSONObject("set")
        assertEquals(setOf("tags", "ask"), set.keys().asSequence().toSet())
        assertEquals("""["a","c","d"]""", set.getJSONArray("tags").toString())
        assertEquals("Confirm it", set.getString("ask"))
        assertEquals("""{"set":{},"reset":["ask"]}""", labelPatch(mapOf("ask" to "x"), emptyMap(), listOf("ask")).toString())
        // Saving a doubted field unchanged confirms it.
        assertEquals("""{"set":{"ask":"x"},"reset":[]}""", labelPatch(mapOf("ask" to "x"), mapOf("ask" to "x"), unsure = listOf("ask")).toString())
    }

    @Test fun unsureAndCategoryShowAsTags() {
        val meta = JSONObject().put("title", "t").put("category", "billing").put("unsure", org.json.JSONArray(listOf("importance")))
        assertEquals(listOf("Check", "billing"), focusLabels(JSONObject().put("kind", "note").put("meta", meta)).map { it.first })
    }

    @Test fun whyHereExplainsAsksAndUrgentTodosInPlainWords() {
        val now = java.time.OffsetDateTime.parse("2026-09-20T12:00:00Z")
        val today = LocalDate.parse("2026-09-20")
        fun entry(kind: String, meta: JSONObject, created: String = "2026-09-20T10:00:00Z", handled: Boolean = false) = JSONObject()
            .put("kind", kind).put("source", "codex").put("created_at", created).put("meta", meta).put("owner", JSONObject().put("handled", handled))
        assertEquals("codex asked 2h ago and is waiting on your answer.", whyHere(entry("note", JSONObject().put("ask", "Confirm it")), today, now))
        assertEquals("", whyHere(entry("note", JSONObject().put("ask", "Confirm it"), handled = true), today, now))
        assertEquals("It was due 19 Sept and is still open.".replace("Sept", java.time.LocalDate.parse("2026-09-19").format(java.time.format.DateTimeFormatter.ofPattern("MMM"))), whyHere(entry("todo", JSONObject().put("due", "2026-09-19")), today, now))
        assertEquals("The AI rated it high priority.", whyHere(entry("todo", JSONObject().put("priority", "high")), today, now))
        assertEquals("It has been open for 20 days.", whyHere(entry("todo", JSONObject(), created = "2026-08-31T12:00:00Z"), today, now))
        assertEquals("", whyHere(entry("decision", JSONObject()), today, now))
    }

    @Test fun aiStatusSaysWhyLabellingIsPausedAndStaysQuietWithoutALabeller() {
        fun progress(active: Boolean, configured: Boolean, problem: String = "", ready: Int = 3) =
            JSONObject().put("total", 4).put("ready", ready).put("failed", 0).put("active", active).put("configured", configured).put("problem", problem)
        assertEquals("AI labelling is paused: can't reach the AI model. 1 entry is waiting and will get titles and labels when it is back." to true,
            aiStatusText(progress(true, true, "can't reach the AI model")))
        assertEquals(true, aiStatusText(progress(false, true))?.second)
        assertEquals("AI summaries: 3 of 4 entries processed." to false, aiStatusText(progress(true, true)))
        assertEquals(null, aiStatusText(progress(false, false)))
        assertEquals(null, aiStatusText(progress(true, true, ready = 4)))
    }

    @Test fun notificationsAnnounceNewAsksAndOverdueTodosOnce() {
        val ask = JSONObject().put("id", "70").put("source", "codex").put("project_name", "Atlas").put("meta", JSONObject().put("ask", "Confirm the pricing"))
        val overdue = JSONObject().put("id", "50").put("project_name", "Atlas").put("meta", JSONObject().put("title", "Add export").put("due", "2026-09-10"))
        val later = JSONObject().put("id", "51").put("project_name", "Atlas").put("meta", JSONObject().put("title", "Later").put("due", "2026-10-10"))
        val inbox = JSONObject().put("needs_you", org.json.JSONArray().put(ask))
        // An overdue todo that also asks something is announced once, as the ask.
        val askingTodo = JSONObject(ask.toString()).put("meta", JSONObject().put("ask", "Confirm the pricing").put("due", "2026-09-01"))
        val nudges = Notifier.nudges(inbox, listOf(overdue, later, askingTodo), LocalDate.parse("2026-09-20"))
        assertEquals(listOf("a:70", "t:50:2026-09-10"), nudges.map { it.key })
        assertEquals("codex asks you", nudges[0].title)
        assertEquals("Confirm the pricing · Atlas", nudges[0].text)
        // Only what was not announced before is new; what is gone is forgotten.
        val (fresh, remember) = Notifier.fresh(nudges, setOf("a:70", "a:1"))
        assertEquals(listOf("t:50:2026-09-10"), fresh.map { it.key })
        assertEquals(setOf("a:70", "t:50:2026-09-10"), remember)
    }

    @Test fun beforeABaselineOnlyWhatArrivedAfterOptInIsAnnounced() {
        val optIn = java.time.Instant.parse("2026-09-20T10:00:00Z")
        val before = Nudge("a:1", "1", "t", "x", java.time.Instant.parse("2026-09-20T09:00:00Z"))
        val after = Nudge("a:2", "2", "t", "x", java.time.Instant.parse("2026-09-20T11:00:00Z"))
        assertEquals(listOf("a:2"), Notifier.toAnnounce(listOf(before, after), seeded = false, since = optIn).map { it.key })
        assertEquals(listOf("a:1", "a:2"), Notifier.toAnnounce(listOf(before, after), seeded = true, since = optIn).map { it.key })
    }
}
