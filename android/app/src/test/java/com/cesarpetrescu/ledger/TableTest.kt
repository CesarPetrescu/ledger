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

    @Test fun onlyWebAndEmailLinksOpen() {
        assertEquals(true, safeLink("https://example.com"))
        assertEquals(true, safeLink("mailto:a@example.com"))
        assertEquals(false, safeLink("javascript:alert(1)"))
        assertEquals(false, safeLink("intent://evil#Intent;end"))
        assertEquals(false, safeLink("file:///sdcard/x"))
        assertEquals(false, safeLink(null))
    }

    @Test fun oversizedMarkdownIsNotFormatted() {
        assertEquals(true, parseBounded("# Plan\n\n- [x] done\n\n| a | b |\n|---|---|\n| 1 | 2 |") != null)
        assertEquals(null, parseBounded("x\n\n".repeat(20_000)))
        assertEquals(null, parseBounded(">".repeat(40) + " deep"))
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

    @Test fun aProjectAndItsScreensAreTitledWithTheProjectsName() {
        val names = mapOf("atlas" to "Atlas Platform")
        assertEquals("Atlas Platform", screenTitle(projectRoute("atlas", "todos"), names))
        assertEquals("Atlas Platform · Repos", screenTitle("project-repos/atlas", names))
        assertEquals("Atlas Platform · Files", screenTitle("project-files/atlas", names))
        assertEquals("Atlas Platform · Edit", screenTitle("project-edit/atlas", names))
        assertEquals("Atlas Platform · New entry", screenTitle("entry/atlas", names))
        // Before any list has named it, the bar says what it is rather than a section name.
        assertEquals("Project", screenTitle("project/other/activity/", names))
        assertEquals("Projects", screenTitle("project-edit/", names))
        assertEquals("Projects", screenTitle("projects", names))
        assertEquals("Connect an agent", screenTitle("connect"))
        assertEquals("Calendars", screenTitle("calendar-settings"))
        assertEquals("Settings", screenTitle("settings"))
    }

    private fun actions(json: String) = entryActions(JSONObject(json))

    @Test fun anEntryScreenHasOneFilledActionAndTucksTheRestAway() {
        val todo = actions("""{"id":"1","kind":"todo","source":"codex","meta":{"title":"Ship","origin":"model","due":"2026-10-01"}}""")
        assertEquals("done", todo.primary)
        assertEquals(listOf("snooze"), todo.secondary)
        assertEquals(listOf("calendar", "labels", "project", "delete"), todo.overflow)
        // A todo that also asks you: Mark done leads, Handled steps back to an outlined button.
        val asking = actions("""{"id":"2","kind":"todo","source":"codex","meta":{"title":"Ship","ask":"Which day?"}}""")
        assertEquals("done", asking.primary)
        assertEquals(listOf("handled", "snooze"), asking.secondary)
        val ask = actions("""{"id":"3","kind":"note","source":"codex","meta":{"title":"Pricing","ask":"Confirm it"}}""")
        assertEquals("handled", ask.primary)
        // Reading: open the link; marking read stays at hand; starring moves under the menu.
        val news = actions("""{"id":"4","kind":"note","source":"codex","owner":{"read":true,"starred":true},"meta":{"title":"News","link":"https://example.com"}}""")
        assertEquals("open_link", news.primary)
        assertEquals(listOf("unread"), news.secondary)
        assertEquals(listOf("unstar", "project", "delete"), news.overflow)
        // Nothing left to settle: no filled button at all.
        val done = actions("""{"id":"5","kind":"todo","source":"codex","resolved_by":{"entry_id":"6"},"meta":{"title":"Ship","origin":"model","unsure":["size"]}}""")
        assertEquals(null, done.primary)
        // Labels the AI doubted get a visible button instead of a menu item.
        assertEquals(listOf("reopen", "labels"), done.secondary)
        assertEquals(listOf("project", "delete"), done.overflow)
        listOf(todo, asking, ask, news, done).forEach { a ->
            (listOfNotNull(a.primary) + a.secondary + a.overflow).let { all ->
                assertEquals(all.distinct(), all)
                all.forEach { assertEquals(true, it in entryActionNames) }
            }
        }
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
        // High priority is the one emphasis label; Important would repeat it.
        assertEquals(listOf("Asks you", "High", "M", overdue, "Stale"),
            focusLabels(focus("todo", meta, created = "2026-09-01T10:00:00Z"), today, now).map { it.first })
        // Handled asks, resolved todos, and fresh todos drop those labels.
        assertEquals(listOf("High", "M"),
            focusLabels(focus("todo", meta, resolved = true, owner = JSONObject().put("handled", true)), today, now).map { it.first })
        val emphasis = { kind: String, importance: String, priority: String ->
            focusLabels(focus(kind, JSONObject().put("importance", importance).put("priority", priority)), today, now).map { it.first } }
        assertEquals(listOf("Important"), emphasis("todo", "important", "low"))
        assertEquals(listOf("Low"), emphasis("todo", "useful", "low"))
        assertEquals(listOf("Important"), emphasis("note", "important", ""))
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

    @Test fun aSummaryThatOnlyRepeatsTheBodyIsDetected() {
        assertEquals(true, sameText("Use the Qwen3 reranker for search.", "  use the Qwen3\nreranker for search "))
        assertEquals(false, sameText("Use the Qwen3 reranker.", "Use **Qwen3 reranker** for search, it beat BGE."))
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
