@file:OptIn(androidx.compose.material3.ExperimentalMaterial3Api::class, androidx.compose.foundation.layout.ExperimentalLayoutApi::class)
package com.cesarpetrescu.ledger

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.filter
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.time.Duration
import java.time.LocalDate
import java.time.OffsetDateTime
import java.time.ZoneId
import java.time.format.DateTimeFormatter

private val entryKinds = listOf("decision", "note", "todo", "status").map { it to label(it) }
private val stateLabels = mapOf("done" to "Done", "in_progress" to "In progress", "blocked" to "Blocked")
private const val STALE_DAYS = 14L

// ---- Pure helpers (unit-tested) ----

/** Query for an entry list; the view fixes the kind, todo state, and reading filter. */
fun tableQuery(view: String, project: String = "", source: String = "", tag: String = "", status: String = "", q: String = "", kind: String = "",
               reading: String = "", hideRoutine: Boolean = false): String {
    val fields = listOf(
        "project" to project, "source" to source, "tag" to tag, "q" to q.trim(),
        "kind" to when (view) { "todos" -> "todo"; "decisions" -> "decision"; "reading" -> ""; else -> kind },
        "status" to if (view == "todos") status else "",
        "reading" to if (view == "reading") reading.ifBlank { "unread" } else "",
        "hide_routine" to if (hideRoutine && view != "todos" && view != "reading") "1" else "",
    ).filter { it.second.isNotBlank() }
    return fields.joinToString("&") { (k, v) -> "$k=${segment(v)}" }
}

/** The extracted title, or the entry's first line until extraction catches up. */
fun entryTitle(entry: JSONObject): String {
    entry.optJSONObject("meta")?.text("title")?.takeIf { it.isNotBlank() }?.let { return it }
    val line = entry.text("body").trim().lineSequence().firstOrNull().orEmpty()
    return if (line.length > 110) line.take(109) + "…" else line
}

/** One line under the title: why it matters for reading, otherwise the gist. */
fun entrySummary(entry: JSONObject, reading: Boolean = false): String {
    val meta = entry.optJSONObject("meta") ?: return ""
    return if (reading) meta.text("why").ifBlank { meta.text("gist") } else meta.text("gist")
}

data class Folded(val entry: JSONObject, val repeats: List<JSONObject>)

/** Folds repeats under the newest loaded copy; the server links each repeat to its root. */
fun foldRepeats(entries: List<JSONObject>): List<Folded> {
    val order = mutableListOf<String>()
    val groups = mutableMapOf<String, MutableList<JSONObject>>()
    entries.forEach { entry ->
        val key = entry.text("duplicate_of").ifBlank { entry.text("id") }
        groups.getOrPut(key) { order += key; mutableListOf() } += entry
    }
    return order.map { key -> groups.getValue(key).let { Folded(it.first(), it.drop(1)) } }
}

/** Consecutive items with the same key form one group, preserving order. */
fun <T> runsBy(items: List<T>, key: (T) -> String): List<Pair<String, List<T>>> {
    val out = mutableListOf<Pair<String, MutableList<T>>>()
    items.forEach { item ->
        val k = key(item)
        if (out.lastOrNull()?.first == k) out.last().second += item else out += k to mutableListOf(item)
    }
    return out
}

fun dayLabel(iso: String, today: LocalDate = LocalDate.now()): String = runCatching {
    val day = OffsetDateTime.parse(iso).atZoneSameInstant(ZoneId.systemDefault()).toLocalDate()
    when (day) {
        today -> "Today"
        today.minusDays(1) -> "Yesterday"
        else -> day.format(DateTimeFormatter.ofPattern("EEEE d MMMM"))
    }
}.getOrDefault(iso)

/** "3h", "2d", "5w" since a timestamp. */
fun ago(iso: String, now: OffsetDateTime = OffsetDateTime.now()): String = runCatching {
    val minutes = Duration.between(OffsetDateTime.parse(iso), now).toMinutes().coerceAtLeast(0)
    when {
        minutes < 60 -> "${minutes}m"
        minutes < 60 * 24 -> "${minutes / 60}h"
        minutes < 60 * 24 * 14 -> "${minutes / (60 * 24)}d"
        else -> "${minutes / (60 * 24 * 7)}w"
    }
}.getOrDefault("")

enum class Tone { Neutral, Accent, Warn, Danger, Good }

/** Short labels shown on a row instead of the text, most important first. */
fun focusLabels(entry: JSONObject, today: LocalDate = LocalDate.now(), now: OffsetDateTime = OffsetDateTime.now()): List<Pair<String, Tone>> {
    val meta = entry.optJSONObject("meta")
    val todo = entry.text("kind") == "todo"
    val open = entry.optJSONObject("resolved_by") == null
    return buildList {
        if (meta == null) return@buildList
        if (meta.text("ask").isNotBlank() && entry.optJSONObject("owner")?.optBoolean("handled") != true) add("Asks you" to Tone.Warn)
        if (meta.text("importance") == "important") add("Important" to Tone.Accent)
        stateLabels[meta.text("state")]?.let { add(it to when (meta.text("state")) { "blocked" -> Tone.Danger; "done" -> Tone.Good; else -> Tone.Accent }) }
        if (todo && meta.text("priority") == "high") add("High" to Tone.Danger)
        if (todo && meta.text("priority") == "low") add("Low" to Tone.Neutral)
        if (meta.text("size").isNotBlank()) add(meta.text("size") to Tone.Neutral)
        val due = runCatching { LocalDate.parse(meta.text("due")) }.getOrNull()
        if (due != null && open) add((if (due < today) "Overdue " else "Due ") + due.format(DateTimeFormatter.ofPattern("d MMM")) to if (due < today) Tone.Danger else Tone.Neutral)
        val created = runCatching { OffsetDateTime.parse(entry.text("created_at")) }.getOrNull()
        if (todo && open && created != null && Duration.between(created, now).toDays() > STALE_DAYS) add("Stale" to Tone.Neutral)
    }
}

/** Route that opens a project on a tab, optionally with a search. */
fun projectRoute(slug: String, tab: String = "activity", q: String = "") = "project/${segment(slug)}/${segment(tab)}/${segment(q)}"

// ---- Paged, pull-to-refresh entry lists ----

/** Holds an entry list that grows as the user scrolls; reloads on refresh. */
class Pager {
    var entries by mutableStateOf(listOf<JSONObject>())
    var data by mutableStateOf<JSONObject?>(null)
    var next by mutableStateOf("")
    var loading by mutableStateOf(true)
    var error by mutableStateOf<String?>(null)
    var generation = 0
}

@Composable
fun rememberPager(model: LedgerModel, query: String): Pager {
    val pager = remember(query) { Pager() }
    val client = model.api
    LaunchedEffect(query, client, model.revision) {
        if (client == null) return@LaunchedEffect
        val generation = ++pager.generation
        pager.loading = true
        pager.error = null
        try {
            val data = withContext(Dispatchers.IO) { client.request("GET", "/entries?limit=50${if (query.isBlank()) "" else "&$query"}") }
            if (generation == pager.generation) {
                pager.data = data; pager.entries = data.rows("entries"); pager.next = data.text("next_before")
            }
        } catch (e: Exception) {
            if (e is CancellationException) throw e
            pager.error = errorMessage(e)
            if (e is ApiError && e.status == 401) model.failed(e, client)
        } finally { if (generation == pager.generation) pager.loading = false }
    }
    return pager
}

@Composable
private fun PagedEntries(model: LedgerModel, pager: Pager, query: String, header: LazyListScope.() -> Unit = {},
                         empty: String = "Nothing here.", rows: LazyListScope.(List<JSONObject>) -> Unit) {
    val client = model.api
    val scope = rememberCoroutineScope()
    val list = rememberLazyListState()
    var loadingMore by remember(query) { mutableStateOf(false) }
    // Load the next page when the last rows come into view.
    LaunchedEffect(list, query) {
        snapshotFlow { list.layoutInfo.visibleItemsInfo.lastOrNull()?.index to list.layoutInfo.totalItemsCount }
            .distinctUntilChanged()
            .filter { (last, total) -> last != null && last >= total - 4 }
            .collect {
                val cursor = pager.next
                if (cursor.isBlank() || loadingMore || client == null) return@collect
                loadingMore = true
                val generation = pager.generation
                scope.launch {
                    try {
                        val page = withContext(Dispatchers.IO) { client.request("GET", "/entries?limit=50&$query&before=${segment(cursor)}") }
                        // A reload in the meantime replaced the list; drop the stale page.
                        if (generation == pager.generation && pager.next == cursor) {
                            pager.entries = pager.entries + page.rows("entries"); pager.next = page.text("next_before")
                        }
                    } catch (e: Exception) {
                        if (e is CancellationException) throw e
                        model.notice = errorMessage(e)
                    } finally { loadingMore = false }
                }
            }
    }
    PullToRefreshBox(isRefreshing = pager.loading && pager.data != null, onRefresh = model::refresh, modifier = Modifier.fillMaxSize()) {
        LazyColumn(Modifier.fillMaxSize().testTag("page"), state = list, contentPadding = PaddingValues(bottom = 24.dp)) {
            header()
            pager.error?.let { message -> item { Column(Modifier.padding(20.dp)) { Text(message, color = MaterialTheme.colorScheme.error); TextButton(onClick = model::refresh) { Text("Retry") } } } }
            if (pager.data == null && pager.loading) item { LinearProgressIndicator(Modifier.fillMaxWidth().padding(20.dp)) }
            if (pager.data != null && pager.entries.isEmpty()) item { Box(Modifier.padding(horizontal = 20.dp)) { Empty(empty) } }
            rows(pager.entries)
            if (loadingMore) item { LinearProgressIndicator(Modifier.fillMaxWidth().padding(20.dp)) }
        }
    }
}

// ---- Rows, swipe actions, and the details sheet ----

@Composable
fun Tag(text: String, tone: Tone) {
    val scheme = MaterialTheme.colorScheme
    val (background, foreground) = when (tone) {
        Tone.Danger -> scheme.errorContainer to scheme.onErrorContainer
        Tone.Warn -> Color(0xFFFFF0D6) to Color(0xFF7A4E00)
        Tone.Accent -> scheme.primaryContainer to scheme.onPrimaryContainer
        Tone.Good -> Color(0xFFDDF3E6) to Color(0xFF1E6B45)
        Tone.Neutral -> scheme.surfaceVariant to scheme.onSurfaceVariant
    }
    Surface(color = background, contentColor = foreground, shape = MaterialTheme.shapes.small) {
        Text(text, Modifier.padding(horizontal = 6.dp, vertical = 2.dp), style = MaterialTheme.typography.labelSmall, maxLines = 1)
    }
}

private fun owner(entry: JSONObject) = entry.optJSONObject("owner") ?: JSONObject()

private fun resolve(model: LedgerModel, entry: JSONObject) =
    model.act("Todo marked done") { it.request("POST", "/entries/${segment(entry.text("id"))}/resolve") }

private fun ownerAction(model: LedgerModel, entry: JSONObject, message: String, vararg patch: Pair<String, Any?>) =
    model.act(message) { it.request("POST", "/entries/${segment(entry.text("id"))}/owner", json(*patch)) }

/** The two swipe actions a row offers, if any: start-to-end, then end-to-start. */
private fun swipeActions(model: LedgerModel, entry: JSONObject, view: String): Pair<Pair<String, () -> Unit>?, Pair<String, () -> Unit>?> {
    val asking = entry.optJSONObject("meta")?.text("ask")?.isNotBlank() == true && !owner(entry).optBoolean("handled")
    val openTodo = entry.text("kind") == "todo" && entry.optJSONObject("resolved_by") == null
    return when {
        view == "reading" -> {
            val read = owner(entry).optBoolean("read")
            val starred = owner(entry).optBoolean("starred")
            (if (read) "Mark unread" else "Mark read") to { ownerAction(model, entry, if (read) "Marked unread" else "Marked read", "read" to !read) } to
                ((if (starred) "Unstar" else "Star") to { ownerAction(model, entry, if (starred) "Unstarred" else "Starred", "starred" to !starred) })
        }
        view == "inbox" && asking -> ("Handled" to { ownerAction(model, entry, "Marked handled", "handled" to true) }) to
            ("Snooze" to { ownerAction(model, entry, "Snoozed until tomorrow", "snooze_days" to 1) })
        openTodo -> ("Done" to { resolve(model, entry) }) to
            ("Snooze" to { ownerAction(model, entry, "Snoozed until tomorrow", "snooze_days" to 1) })
        else -> null to null
    }
}

@Composable
fun EntryItem(model: LedgerModel, entry: JSONObject, view: String, repeats: List<JSONObject> = emptyList(), headline: String? = null,
              showProject: Boolean = true, onOpen: (JSONObject, List<JSONObject>) -> Unit) {
    val (start, end) = swipeActions(model, entry, view)
    val row: @Composable () -> Unit = { EntryRowContent(entry, view, repeats, headline, showProject) { onOpen(entry, repeats) } }
    if (start == null && end == null) { row(); HorizontalDivider(); return }
    // The action runs on the swipe and the row springs back; the reload
    // then shows the new state (or removes the row from filtered lists).
    val state = rememberSwipeToDismissBoxState(confirmValueChange = { value ->
        if (!model.busy) when (value) {
            SwipeToDismissBoxValue.StartToEnd -> start?.second?.invoke()
            SwipeToDismissBoxValue.EndToStart -> end?.second?.invoke()
            SwipeToDismissBoxValue.Settled -> Unit
        }
        false
    })
    SwipeToDismissBox(state, enableDismissFromStartToEnd = start != null, enableDismissFromEndToStart = end != null, backgroundContent = {
        // Draw the action label only while swiping, so idle rows carry no hidden text.
        val direction = state.dismissDirection
        if (direction != SwipeToDismissBoxValue.Settled) {
            val toStart = direction == SwipeToDismissBoxValue.EndToStart
            Box(Modifier.fillMaxSize().padding(horizontal = 20.dp), contentAlignment = if (toStart) Alignment.CenterEnd else Alignment.CenterStart) {
                Text((if (toStart) end?.first else start?.first).orEmpty(), style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.primary)
            }
        }
    }) { Surface(color = MaterialTheme.colorScheme.background) { row() } }
    HorizontalDivider()
}

@Composable
private fun EntryRowContent(entry: JSONObject, view: String, repeats: List<JSONObject>, headline: String?, showProject: Boolean, open: () -> Unit) {
    val reading = view == "reading"
    val muted = MaterialTheme.colorScheme.onSurfaceVariant
    val done = entry.optJSONObject("resolved_by") != null || (reading && owner(entry).optBoolean("read"))
    Column(Modifier.fillMaxWidth().clickable(onClick = open).padding(horizontal = 20.dp, vertical = 12.dp).testTag("entry-${entry.text("id")}"),
        verticalArrangement = Arrangement.spacedBy(3.dp)) {
        val context = buildList {
            if (showProject) add(entry.text("project_name"))
            if (reading) entry.optJSONObject("meta")?.text("source")?.takeIf { it.isNotBlank() }?.let { add(it) } else add(entry.text("source"))
            add(ago(entry.text("created_at")))
            if (view == "activity") add(label(entry.text("kind")))
        }.filter { it.isNotBlank() }
        Text(context.joinToString(" · "), style = MaterialTheme.typography.labelSmall, color = muted, maxLines = 1, overflow = TextOverflow.Ellipsis)
        Row(verticalAlignment = Alignment.Top, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Text(headline ?: entryTitle(entry), Modifier.weight(1f), style = MaterialTheme.typography.titleSmall,
                fontWeight = if (done) FontWeight.Normal else FontWeight.SemiBold, color = if (done) muted else MaterialTheme.colorScheme.onSurface,
                maxLines = 2, overflow = TextOverflow.Ellipsis)
            if (reading && owner(entry).optBoolean("starred")) Text("★", color = Color(0xFFB88A00))
        }
        val summary = if (headline != null) entryTitle(entry) else entrySummary(entry, reading)
        if (summary.isNotBlank()) Text(summary, style = MaterialTheme.typography.bodyMedium, color = muted, maxLines = 2, overflow = TextOverflow.Ellipsis)
        val labels = focusLabels(entry)
        if (labels.isNotEmpty() || repeats.isNotEmpty()) FlowRow(horizontalArrangement = Arrangement.spacedBy(4.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            labels.forEach { (text, tone) -> Tag(text, tone) }
            if (repeats.isNotEmpty()) Tag("+${repeats.size} ${if (repeats.size == 1) "repeat" else "repeats"}", Tone.Neutral)
        }
    }
}

/** Remembers which entry's details sheet is open, shared by a screen's rows. */
class SheetState { var entry by mutableStateOf<JSONObject?>(null); var repeats by mutableStateOf(listOf<JSONObject>()) }

@Composable
fun EntrySheetHost(model: LedgerModel, sheet: SheetState) {
    val entry = sheet.entry ?: return
    ModalBottomSheet(onDismissRequest = { sheet.entry = null }) {
        EntrySheet(model, entry, sheet.repeats) { sheet.entry = null }
    }
}

@Composable
private fun EntrySheet(model: LedgerModel, entry: JSONObject, repeats: List<JSONObject>, close: () -> Unit) {
    val context = LocalContext.current
    val meta = entry.optJSONObject("meta")
    val id = entry.text("id")
    val openTodo = entry.text("kind") == "todo" && entry.optJSONObject("resolved_by") == null
    val act: (() -> Unit) -> Unit = { action -> action(); close() }
    Column(Modifier.fillMaxWidth().verticalScroll(rememberScrollState()).padding(horizontal = 20.dp).navigationBarsPadding().padding(bottom = 24.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text("${entry.text("project_name")} · ${label(entry.text("kind"))} · ${entry.text("source")} · ${displayTime(entry.text("created_at"))}",
            style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Text(entryTitle(entry), style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.SemiBold)
        val labels = focusLabels(entry)
        if (labels.isNotEmpty()) FlowRow(horizontalArrangement = Arrangement.spacedBy(4.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) { labels.forEach { (t, tone) -> Tag(t, tone) } }
        FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            if (openTodo) Button(onClick = { act { resolve(model, entry) } }, enabled = !model.busy) { Text("Mark done") }
            entry.optJSONObject("resolved_by")?.let { OutlinedButton(onClick = { act { model.act("Todo reopened") { it.request("POST", "/entries/${segment(id)}/reopen") } } }, enabled = !model.busy) { Text("Reopen") } }
            if (meta?.text("ask")?.isNotBlank() == true && !owner(entry).optBoolean("handled")) Button(onClick = { act { ownerAction(model, entry, "Marked handled", "handled" to true) } }, enabled = !model.busy) { Text("Handled") }
            if (openTodo || (meta?.text("ask")?.isNotBlank() == true && !owner(entry).optBoolean("handled"))) OutlinedButton(onClick = { act { ownerAction(model, entry, "Snoozed until tomorrow", "snooze_days" to 1) } }, enabled = !model.busy) { Text("Snooze") }
            if (meta?.text("link")?.isNotBlank() == true) {
                val read = owner(entry).optBoolean("read")
                val starred = owner(entry).optBoolean("starred")
                // Only a link that actually opened counts as read.
                OutlinedButton(onClick = { if (openBrowser(context, meta.text("link"), model) && !read) ownerAction(model, entry, "Marked read", "read" to true) }) { Text("Open link") }
                OutlinedButton(onClick = { act { ownerAction(model, entry, if (read) "Marked unread" else "Marked read", "read" to !read) } }, enabled = !model.busy) { Text(if (read) "Mark unread" else "Mark read") }
                OutlinedButton(onClick = { act { ownerAction(model, entry, if (starred) "Unstarred" else "Starred", "starred" to !starred) } }, enabled = !model.busy) { Text(if (starred) "Unstar" else "Star") }
            }
            if (openTodo && meta?.text("due")?.isNotBlank() == true) OutlinedButton(onClick = { act { addToCalendar(model, entry) } }, enabled = !model.busy) { Text("Add to calendar") }
            TextButton(onClick = { close(); model.go(projectRoute(entry.text("slug"))) }) { Text("Open project") }
        }
        if (meta != null) listOf(
            "Asks you" to meta.text("ask"), "Summary" to meta.text("gist"), "Next step" to meta.text("next_step"), "Blocked by" to meta.text("blocker"),
            (if (entry.text("kind") == "decision") "Why" else "Why it matters") to meta.text("why"), "Source" to meta.text("source"),
        ).filter { it.second.isNotBlank() }.forEach { (name, value) ->
            Column { Text(name, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant); SelectionContainer { Text(value, style = MaterialTheme.typography.bodyMedium) } }
        }
        HorizontalDivider()
        SelectionContainer { Text(entry.text("body"), style = MaterialTheme.typography.bodyMedium) }
        meta?.strings("refs")?.takeIf { it.isNotEmpty() }?.let { refs -> SelectionContainer { Text(refs.joinToString("\n"), style = MaterialTheme.typography.bodySmall) } }
        if (repeats.isNotEmpty()) Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Text("Repeats", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            repeats.forEach { Text("${entryTitle(it)} · ${displayTime(it.text("created_at"))}", style = MaterialTheme.typography.bodySmall) }
        }
        RelatedEntries(model, id, close)
        Text(when { meta == null -> "Summary pending."; meta.text("origin") == "model" -> "Title, summary, and labels generated by AI from the text above."; else -> "Written from the console." },
            style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

/** Adds a todo's due date as an all-day event in the first selected calendar. */
private fun addToCalendar(model: LedgerModel, entry: JSONObject) = model.act("Added to calendar") { api ->
    val due = LocalDate.parse(entry.optJSONObject("meta")?.text("due"))
    val calendar = api.request("GET", "/calendar/calendars").rows("calendars").firstOrNull { it.optBoolean("selected") }
        ?: throw IllegalStateException("Choose a calendar first: More › Calendar settings.")
    api.request("POST", "/calendar/events", json("calendar_id" to calendar.text("id"), "title" to entryTitle(entry).take(200),
        "start" to due.toString(), "end" to due.plusDays(1).toString(), "all_day" to true, "location" to "",
        "description" to "Ledger todo in ${entry.text("project_name")}".take(4000)))
}

@Composable
private fun RelatedEntries(model: LedgerModel, id: String, close: () -> Unit) {
    val client = model.api ?: return
    var related by remember(id) { mutableStateOf<List<JSONObject>?>(null) }
    LaunchedEffect(id, client, model.revision) {
        related = try {
            withContext(Dispatchers.IO) { client.request("GET", "/entries/${segment(id)}/related").rows("related") }
        } catch (e: Exception) {
            if (e is CancellationException) throw e
            // Related entries are best effort, but an expired session must still
            // reach the normal sign-in-again flow.
            if (e is ApiError && e.status == 401) model.failed(e, client)
            emptyList()
        }
    }
    val rows = related.orEmpty()
    if (rows.isEmpty()) return
    Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
        Text("Related", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        rows.forEach { r ->
            // Open the entry itself (by project and title), not just its project's newest page.
            TextButton(onClick = { close(); model.go(projectRoute(r.text("slug"), "activity", entryTitle(r))) }, contentPadding = PaddingValues(0.dp)) {
                Text("${entryTitle(r)} · ${r.text("project_name")}", style = MaterialTheme.typography.bodySmall, maxLines = 2, overflow = TextOverflow.Ellipsis)
            }
        }
    }
}

@Composable
private fun SectionHeader(text: String, action: (@Composable () -> Unit)? = null) {
    Row(Modifier.fillMaxWidth().padding(start = 20.dp, end = 8.dp, top = 20.dp, bottom = 4.dp), verticalAlignment = Alignment.CenterVertically) {
        Text(text, Modifier.weight(1f), style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.Bold, color = MaterialTheme.colorScheme.primary)
        action?.invoke()
    }
}

@Composable
private fun FilterChips(options: List<Pair<String, String>>, selected: String, change: (String) -> Unit) {
    Row(Modifier.fillMaxWidth().padding(horizontal = 20.dp, vertical = 4.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        options.forEach { (id, name) -> FilterChip(selected = selected == id, onClick = { change(id) }, label = { Text(name) }) }
    }
}

// ---- Screens ----

/** Everything that needs the owner, most urgent first. */
@Composable
fun InboxScreen(model: LedgerModel) {
    val sheet = remember { SheetState() }
    Load(model, "inbox", { it.request("GET", "/inbox") }) { data ->
        val asks = data.rows("needs_you")
        val todos = data.rows("todos")
        val projects = data.rows("projects")
        val blocked = projects.filter { it.text("status_state") == "blocked" }
        val digests = projects.filter { it.text("digest").isNotBlank() }
        PullToRefreshBox(isRefreshing = false, onRefresh = model::refresh, modifier = Modifier.fillMaxSize()) {
            LazyColumn(Modifier.fillMaxSize().testTag("page"), contentPadding = PaddingValues(bottom = 24.dp)) {
                item { SectionHeader("Needs you · ${asks.size}") }
                if (asks.isEmpty()) item { Box(Modifier.padding(horizontal = 20.dp)) { Empty("Nothing is waiting on you.") } }
                items(asks, key = { "a" + it.text("id") }) { e -> EntryItem(model, e, "inbox", headline = e.optJSONObject("meta")?.text("ask")) { entry, r -> sheet.entry = entry; sheet.repeats = r } }
                item {
                    val total = data.optInt("todos_total")
                    SectionHeader("Todos · ${if (todos.size < total) "${todos.size} of $total" else "$total"}") { if (total > 0) TextButton(onClick = { model.go("todos") }) { Text("All todos") } }
                }
                if (todos.isEmpty()) item { Box(Modifier.padding(horizontal = 20.dp)) { Empty("No open todos. Nice.") } }
                items(todos, key = { "t" + it.text("id") }) { e -> EntryItem(model, e, "inbox") { entry, r -> sheet.entry = entry; sheet.repeats = r } }
                if (blocked.isNotEmpty()) {
                    item { SectionHeader("Blocked · ${blocked.size}") }
                    items(blocked, key = { "b" + it.text("slug") }) { p -> ProjectLine(model, p, p.text("status_title").ifBlank { p.text("status_body") }) }
                }
                if (digests.isNotEmpty()) {
                    item { SectionHeader("This week") }
                    items(digests, key = { "d" + it.text("slug") }) { p -> ProjectLine(model, p, p.text("digest")) }
                }
            }
        }
    }
    EntrySheetHost(model, sheet)
}

@Composable
private fun ProjectLine(model: LedgerModel, p: JSONObject, text: String) {
    Column(Modifier.fillMaxWidth().clickable { model.go(projectRoute(p.text("slug"))) }.padding(horizontal = 20.dp, vertical = 12.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
            Text(p.text("name"), style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold)
            HealthTag(p.text("status_state"))
        }
        if (text.isNotBlank()) Text(text, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 3, overflow = TextOverflow.Ellipsis)
    }
    HorizontalDivider()
}

/** A project's health label. Only "blocked" is shown: a latest status of
 *  "done" means that update finished something, not that the project did. */
@Composable
private fun HealthTag(state: String) {
    if (state == "blocked") Tag("Blocked", Tone.Danger)
}

/** Projects with health, open work, and what needs the owner. */
@Composable
fun ProjectsHome(model: LedgerModel) = Load(model, "table-projects", { it.request("GET", "/table/projects") }) { data ->
    val progress = data.optJSONObject("metadata")
    PullToRefreshBox(isRefreshing = false, onRefresh = model::refresh, modifier = Modifier.fillMaxSize()) {
        LazyColumn(Modifier.fillMaxSize().testTag("page"), contentPadding = PaddingValues(bottom = 88.dp)) {
            if (progress != null && progress.optBoolean("active") && progress.optInt("ready") + progress.optInt("failed") < progress.optInt("total")) item {
                Text("AI summaries: ${progress.optInt("ready")} of ${progress.optInt("total")} entries processed.", Modifier.padding(20.dp, 12.dp), style = MaterialTheme.typography.bodySmall)
            }
            item { Row(Modifier.padding(horizontal = 20.dp, vertical = 8.dp)) { Button(onClick = { model.go("project-edit/") }, enabled = !model.busy) { Text("New project") } } }
            if (data.rows("projects").isEmpty()) item { Box(Modifier.padding(20.dp)) { Empty("No projects yet.") } }
            items(data.rows("projects"), key = { it.text("slug") }) { p ->
                Column(Modifier.fillMaxWidth().clickable { model.go(projectRoute(p.text("slug"))) }.padding(horizontal = 20.dp, vertical = 14.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                        Text(p.text("name"), Modifier.weight(1f, fill = false), style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        HealthTag(p.text("status_state"))
                    }
                    val facts = buildList {
                        add(label(p.text("tier")))
                        if (p.optInt("open_todos") > 0) add("${p.optInt("open_todos")} open todos")
                        if (p.optInt("needs_you") > 0) add("${p.optInt("needs_you")} need you")
                        add(if (p.optInt("week_entries") > 0) "${p.optInt("week_entries")} this week" else "quiet")
                        if (p.text("deadline").isNotBlank()) add("due ${p.text("deadline")}")
                    }
                    Text(facts.joinToString(" · "), style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.primary)
                    val summary = p.text("digest").ifBlank { p.text("status_title").ifBlank { p.text("status_body") } }
                    if (summary.isNotBlank()) Text(summary, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 3, overflow = TextOverflow.Ellipsis)
                }
                HorizontalDivider()
            }
        }
    }
}

private val projectTabs = listOf("todos" to "Todos", "decisions" to "Decisions", "activity" to "Activity")

/** One project: its health and digest, then its todos, decisions, or activity. */
@Composable
fun ProjectScreen(model: LedgerModel, slug: String, initialTab: String = "activity", initialQuery: String = "") {
    var tab by rememberSaveable { mutableStateOf(initialTab.takeIf { t -> projectTabs.any { it.first == t } } ?: "activity") }
    var q by rememberSaveable { mutableStateOf(initialQuery) }
    var showRoutine by rememberSaveable { mutableStateOf(initialQuery.isNotBlank()) }
    var todoState by rememberSaveable { mutableStateOf("open") }
    val sheet = remember { SheetState() }
    val query = tableQuery(tab, project = slug, q = q, status = todoState, hideRoutine = !showRoutine)
    val pager = rememberPager(model, query)
    Load(model, "project-summary:$slug", { api -> api.request("GET", "/table/projects").rows("projects").firstOrNull { it.text("slug") == slug } ?: throw ApiError(404, "Project not found.") }) { p ->
        Column {
            Column(Modifier.padding(start = 20.dp, end = 8.dp, top = 4.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                    Text(p.text("name"), Modifier.weight(1f, fill = false), style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.SemiBold, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    HealthTag(p.text("status_state"))
                }
                // Tap the summary to read it all, including what the project needs from you.
                var expanded by rememberSaveable { mutableStateOf(false) }
                val summary = p.text("digest").ifBlank { p.text("status_title") }
                if (summary.isNotBlank()) Text(summary, Modifier.clickable { expanded = !expanded }, style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = if (expanded) Int.MAX_VALUE else 2, overflow = TextOverflow.Ellipsis)
                if (expanded && p.text("needs_me").isNotBlank()) Text("Needs you: ${p.text("needs_me")}", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.tertiary)
                Row(horizontalArrangement = Arrangement.spacedBy(4.dp), verticalAlignment = Alignment.CenterVertically) {
                    FilledTonalButton(onClick = { model.go("entry/$slug") }, enabled = !model.busy, contentPadding = PaddingValues(horizontal = 14.dp)) { Text("Add entry") }
                    TextButton(onClick = { model.go("project-edit/$slug") }, enabled = !model.busy) { Text("Edit") }
                    TextButton(onClick = { model.go("project-files/$slug") }) { Text("Files") }
                }
            }
            PrimaryTabRow(selectedTabIndex = projectTabs.indexOfFirst { it.first == tab }) {
                projectTabs.forEach { (id, name) -> Tab(selected = tab == id, onClick = { tab = id }, text = { Text(name) }) }
            }
            PagedEntries(model, pager, query, empty = if (tab == "todos" && todoState == "open") "No open todos. Nice." else "No entries match.", header = {
                item {
                    Column(Modifier.padding(horizontal = 20.dp, vertical = 4.dp)) {
                        if (q.isNotBlank()) AssistChip(onClick = { q = "" }, label = { Text("Search: $q ✕") })
                        if (tab == "todos") Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                            listOf("open" to "Open", "done" to "Done").forEach { (id, name) -> FilterChip(selected = todoState == id, onClick = { todoState = id }, label = { Text(name) }) }
                        }
                        if (tab != "todos") FilterChip(selected = showRoutine, onClick = { showRoutine = !showRoutine }, label = { Text("Show routine") })
                    }
                }
            }) { entries ->
                val folded = foldRepeats(entries).let { heads -> if (tab == "todos") heads.sortedBy { priorityRank(it.entry) } else heads }
                runsBy(folded) { if (tab == "todos") "" else dayLabel(it.entry.text("created_at")) }.forEach { (day, group) ->
                    if (day.isNotBlank()) item(key = "h:$day:${group.first().entry.text("id")}") { SectionHeader(day) }
                    items(group, key = { it.entry.text("id") }) { f -> EntryItem(model, f.entry, tab, f.repeats, showProject = false) { e, r -> sheet.entry = e; sheet.repeats = r } }
                }
            }
        }
    }
    EntrySheetHost(model, sheet)
}

private fun priorityRank(entry: JSONObject) = when (entry.optJSONObject("meta")?.text("priority")) { "high" -> 0; "low" -> 2; else -> 1 }

/** Open todos across projects, grouped by project and most important first. */
@Composable
fun TodosScreen(model: LedgerModel) {
    val sheet = remember { SheetState() }
    val query = tableQuery("todos", status = "open")
    val pager = rememberPager(model, query)
    PagedEntries(model, pager, query, empty = "No open todos. Nice.") { entries ->
        val heads = foldRepeats(entries).sortedWith(compareBy({ it.entry.text("project_name") }, { it.entry.text("slug") }, { priorityRank(it.entry) }))
        runsBy(heads) { it.entry.text("slug") }.forEach { (slug, group) ->
            item(key = "h:$slug") { SectionHeader(group.first().entry.text("project_name")) }
            items(group, key = { it.entry.text("id") }) { f -> EntryItem(model, f.entry, "todos", f.repeats, showProject = false) { e, r -> sheet.entry = e; sheet.repeats = r } }
        }
    }
    EntrySheetHost(model, sheet)
}

/** Linked news-style entries: headline, why it matters, and read/star triage. */
@Composable
fun ReadingScreen(model: LedgerModel) {
    var filter by rememberSaveable { mutableStateOf("unread") }
    val sheet = remember { SheetState() }
    val query = tableQuery("reading", reading = filter)
    val pager = rememberPager(model, query)
    Column {
        FilterChips(listOf("unread" to "Unread", "starred" to "Starred", "all" to "All"), filter) { filter = it }
        PagedEntries(model, pager, query, empty = if (filter == "unread") "Nothing left to read." else "Nothing here yet.") { entries ->
            items(foldRepeats(entries), key = { it.entry.text("id") }) { f -> EntryItem(model, f.entry, "reading", f.repeats) { e, r -> sheet.entry = e; sheet.repeats = r } }
        }
    }
    EntrySheetHost(model, sheet)
}

@Composable
fun MoreScreen(model: LedgerModel) {
    Page {
        listOf(
            Triple("Calendar", "Your Nextcloud events", "calendar"),
            Triple("Search", "Find projects, decisions, and notes", "search"),
            Triple("Connected clients", "Review and revoke agent access", "clients"),
            Triple("Approve a device", "Enter the code shown by the Ledger CLI", "device"),
            Triple("Settings", "Version, updates, sign out", "settings"),
        ).forEach { (title, subtitle, route) ->
            item { ListItem(headlineContent = { Text(title) }, supportingContent = { Text(subtitle) }, modifier = Modifier.clickable { model.go(route) }); HorizontalDivider() }
        }
    }
}

/** Adds an entry, choosing the project when none was given. */
@Composable
fun TableAdd(model: LedgerModel, defaultKind: String, initialProject: String) = Load(model, "table-add-projects", { it.request("GET", "/projects") }) { data ->
    val projects = data.rows("projects")
    var slug by rememberSaveable { mutableStateOf(initialProject.ifBlank { projects.firstOrNull()?.text("slug").orEmpty() }) }
    var kind by rememberSaveable { mutableStateOf(defaultKind.takeIf { k -> entryKinds.any { it.first == k } } ?: "note") }
    var body by rememberSaveable { mutableStateOf("") }
    Page {
        item { Text(if (kind == "todo") "Add a todo" else "Add an entry", style = MaterialTheme.typography.headlineSmall) }
        if (projects.isEmpty()) item { Empty("Create a project first.") }
        else {
            item { Choice("Project", slug, projects.map { p -> p.text("slug") to if (projects.count { it.text("name") == p.text("name") } > 1) "${p.text("name")} (${p.text("slug")})" else p.text("name") }) { slug = it } }
            item { Choice("Kind", kind, entryKinds) { kind = it } }
            item { Field("Text", body, { body = it }, multiline = true, max = 4000) }
            item { Text("Entries are permanent. Add a correction as a new entry.", style = MaterialTheme.typography.bodySmall) }
            item { Button(onClick = { model.act("Entry added", after = model::back) { it.request("POST", "/projects/${segment(slug)}/entries", json("kind" to kind, "body" to body.trim())) } },
                enabled = !model.busy && validProjectSlug(slug) && body.isNotBlank(), modifier = Modifier.fillMaxWidth()) { Text("Add") } }
        }
    }
}
