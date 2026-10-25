@file:OptIn(androidx.compose.material3.ExperimentalMaterial3Api::class, androidx.compose.foundation.layout.ExperimentalLayoutApi::class)
package com.cesarpetrescu.ledger

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
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

val entryKinds = listOf("decision", "note", "todo", "status").map { it to label(it) }
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

/** Short labels shown on a row instead of the text, most important first. */
fun focusLabels(entry: JSONObject, today: LocalDate = LocalDate.now(), now: OffsetDateTime = OffsetDateTime.now()): List<Pair<String, Tone>> {
    val meta = entry.optJSONObject("meta")
    val todo = entry.text("kind") == "todo"
    val open = entry.optJSONObject("resolved_by") == null
    return buildList {
        if (meta == null) return@buildList
        if (asksYou(entry)) add("Asks you" to Tone.Warn)
        // One emphasis label at most: a todo's high priority, else importance, else low priority.
        when {
            todo && meta.text("priority") == "high" -> add("High" to Tone.Danger)
            meta.text("importance") == "important" -> add("Important" to Tone.Accent)
            todo && meta.text("priority") == "low" -> add("Low" to Tone.Neutral)
        }
        stateLabels[meta.text("state")]?.let { add(it to when (meta.text("state")) { "blocked" -> Tone.Danger; "done" -> Tone.Good; else -> Tone.Accent }) }
        if (meta.text("size").isNotBlank()) add(meta.text("size") to Tone.Neutral)
        val due = runCatching { LocalDate.parse(meta.text("due")) }.getOrNull()
        if (due != null && open) add((if (due < today) "Overdue " else "Due ") + due.format(DateTimeFormatter.ofPattern("d MMM")) to if (due < today) Tone.Danger else Tone.Neutral)
        val created = runCatching { OffsetDateTime.parse(entry.text("created_at")) }.getOrNull()
        if (todo && open && created != null && Duration.between(created, now).toDays() > STALE_DAYS) add("Stale" to Tone.Neutral)
        if (meta.strings("unsure").isNotEmpty()) add("Check" to Tone.Warn)
        if (meta.text("category").isNotBlank()) add(meta.text("category") to Tone.Neutral)
    }
}

/** A row's labels: at most three. "Asks you" is left to the row's amber mark and its headline, which is the question. */
fun rowLabels(entry: JSONObject, today: LocalDate = LocalDate.now(), now: OffsetDateTime = OffsetDateTime.now()) =
    focusLabels(entry, today, now).filterNot { it.first == "Asks you" }.take(3)

/** An entry screen's actions: at most one filled [primary], a few outlined [secondary] ones, and the rest under ⋮. */
data class EntryActions(val primary: String?, val secondary: List<String>, val overflow: List<String>)

val entryActionNames = mapOf("done" to "Mark done", "handled" to "Handled", "open_link" to "Open link", "reopen" to "Reopen", "snooze" to "Snooze",
    "read" to "Mark read", "unread" to "Mark unread", "star" to "Star", "unstar" to "Unstar", "calendar" to "Add to calendar",
    "labels" to "Edit labels", "project" to "Open project", "delete" to "Delete entry")

fun entryActions(entry: JSONObject): EntryActions {
    val meta = entry.optJSONObject("meta")
    val resolved = entry.optJSONObject("resolved_by") != null
    val openTodo = entry.text("kind") == "todo" && !resolved
    val asking = asksYou(entry)
    val link = meta?.text("link")?.isNotBlank() == true
    val labels = meta != null && meta.text("title").isNotBlank() && meta.text("origin") == "model"
    // Labels the AI doubted earn a button; otherwise correcting them is occasional.
    val doubted = labels && meta.strings("unsure").isNotEmpty()
    val shown = buildList {
        if (openTodo) add("done")
        if (asking) add("handled")
        if (link) add("open_link")
        if (resolved) add("reopen")
        if (openTodo || asking) add("snooze")
        if (link) add(if (owner(entry).optBoolean("read")) "unread" else "read")
        if (doubted) add("labels")
    }
    // What settles the entry leads: done, then handled, then reading the link.
    val primary = shown.firstOrNull { it == "done" || it == "handled" || it == "open_link" }
    return EntryActions(primary, shown - setOfNotNull(primary), buildList {
        if (link) add(if (owner(entry).optBoolean("starred")) "unstar" else "star")
        if (openTodo && meta?.text("due")?.isNotBlank() == true) add("calendar")
        if (labels && !doubted) add("labels")
        add("project")
        add("delete")
    })
}

/** Whether a summary only repeats the text it sums up, spacing, case, and a final period aside. */
fun sameText(a: String, b: String): Boolean {
    val plain = { text: String -> text.trim().trimEnd('.').replace(Regex("\\s+"), " ") }
    return plain(a).equals(plain(b), ignoreCase = true)
}

/** Route that opens a project on a tab, optionally with a search. */
fun projectRoute(slug: String, tab: String = "activity", q: String = "") = "project/${segment(slug)}/${segment(tab)}/${segment(q)}"

/** Who the owner is shown as: "You" for anything written from the console or this phone. */
fun writerName(source: String) = if (source == OWNER_SOURCE) "You" else source

const val OWNER_SOURCE = "ledger-admin"

/** An agent waits on your answer; your own entries never ask you anything. */
fun asksYou(entry: JSONObject) = entry.optJSONObject("meta")?.text("ask")?.isNotBlank() == true &&
    !owner(entry).optBoolean("handled") && entry.text("source") != OWNER_SOURCE

/** One line of an entry's history, in plain words. */
fun historyLine(event: JSONObject): String {
    val who = writerName(event.text("actor"))
    return when (event.text("kind")) {
        "created" -> "$who wrote it" + event.text("text").takeIf { it.isNotBlank() && who != "You" }?.let { " through $it" }.orEmpty()
        "repeat" -> "$who wrote it again"
        "resolved" -> "$who reported it finished"
        "action" -> "You: ${event.text("text").lowercase()}" + if (event.optBoolean("undone")) " (undone)" else ""
        "labels" -> "You corrected ${event.text("text").replace('_', ' ')}"
        "reply" -> "$who replied"
        else -> event.text("text")
    }
}

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
fun PagedEntries(model: LedgerModel, pager: Pager, query: String, header: LazyListScope.() -> Unit = {},
                 empty: String = "Nothing here.", bottom: Int = 24, rows: LazyListScope.(List<JSONObject>) -> Unit) {
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
        LazyColumn(Modifier.fillMaxSize().testTag("page"), state = list, contentPadding = PaddingValues(bottom = bottom.dp)) {
            header()
            pager.error?.let { message -> item { Column(Modifier.padding(Gutter)) { Text(message, color = MaterialTheme.colorScheme.error); TextButton(onClick = model::refresh) { Text("Retry") } } } }
            if (pager.data == null && pager.loading) item { LinearProgressIndicator(Modifier.fillMaxWidth().padding(Gutter)) }
            if (pager.data != null && pager.entries.isEmpty()) item { Box(Modifier.padding(horizontal = Gutter)) { Empty(empty) } }
            rows(pager.entries)
            if (loadingMore) item { LinearProgressIndicator(Modifier.fillMaxWidth().padding(Gutter)) }
        }
    }
}

/** A row of chips, one selected. */
@Composable
fun FilterChips(options: List<Pair<String, String>>, selected: String, modifier: Modifier = Modifier, change: (String) -> Unit) {
    Row(modifier.fillMaxWidth().padding(horizontal = Gutter, vertical = 4.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        options.forEach { (id, name) -> FilterChip(selected = selected == id, onClick = { change(id) }, label = { Text(name) }, shape = MaterialTheme.shapes.small) }
    }
}

// ---- Rows, swipe actions, and the entry page ----

private fun owner(entry: JSONObject) = entry.optJSONObject("owner") ?: JSONObject()

fun resolve(model: LedgerModel, entry: JSONObject, after: () -> Unit = {}) =
    model.undoable("Todo marked done", after = after) { it.request("POST", "/entries/${segment(entry.text("id"))}/resolve") }

fun ownerAction(model: LedgerModel, entry: JSONObject, message: String, vararg patch: Pair<String, Any?>, after: () -> Unit = {}) =
    model.undoable(message, after = after) { it.request("POST", "/entries/${segment(entry.text("id"))}/owner", json(*patch)) }

/** A swipe's label, icon and tone, and what it does. */
private class Swipe(val label: String, val icon: String, val tone: Tone, val run: () -> Unit)

/** The two swipe actions a row offers, if any: start-to-end (settles it), then end-to-start (puts it off). */
private fun swipeActions(model: LedgerModel, entry: JSONObject, view: String): Pair<Swipe?, Swipe?> {
    val asking = asksYou(entry)
    val openTodo = entry.text("kind") == "todo" && entry.optJSONObject("resolved_by") == null
    val snooze = Swipe("Snooze", "snooze", Tone.Warn) { ownerAction(model, entry, "Snoozed until tomorrow", "snooze_days" to 1) }
    return when {
        view == "reading" -> {
            val read = owner(entry).optBoolean("read")
            val starred = owner(entry).optBoolean("starred")
            Swipe(if (read) "Mark unread" else "Mark read", "check", Tone.Good) { ownerAction(model, entry, if (read) "Marked unread" else "Marked read", "read" to !read) } to
                Swipe(if (starred) "Unstar" else "Star", "star", Tone.Warn) { ownerAction(model, entry, if (starred) "Unstarred" else "Starred", "starred" to !starred) }
        }
        view == "inbox" && asking -> Swipe("Handled", "check", Tone.Good) { ownerAction(model, entry, "Marked handled", "handled" to true) } to snooze
        openTodo -> Swipe("Done", "check", Tone.Good) { resolve(model, entry) } to snooze
        else -> null to null
    }
}

/** An entry in a list: its kind, title, summary, who and when, and a few labels. Swipe right to settle it, left to put it off.
 * With [check], an open todo leads with a ring that marks it done. */
@Composable
fun EntryItem(model: LedgerModel, entry: JSONObject, view: String, repeats: List<JSONObject> = emptyList(), headline: String? = null,
              showProject: Boolean = true, check: Boolean = false, onOpen: (JSONObject, List<JSONObject>) -> Unit) {
    val (start, end) = swipeActions(model, entry, view)
    val row: @Composable () -> Unit = { EntryRowContent(model, entry, view, repeats, headline, showProject, check) { onOpen(entry, repeats) } }
    if (start == null && end == null) { row(); GroupDivider(); return }
    // The action runs on the swipe and the row springs back; the reload
    // then shows the new state (or removes the row from filtered lists).
    val state = rememberSwipeToDismissBoxState(confirmValueChange = { value ->
        if (!model.busy) when (value) {
            SwipeToDismissBoxValue.StartToEnd -> start?.run?.invoke()
            SwipeToDismissBoxValue.EndToStart -> end?.run?.invoke()
            SwipeToDismissBoxValue.Settled -> Unit
        }
        false
    })
    SwipeToDismissBox(state, enableDismissFromStartToEnd = start != null, enableDismissFromEndToStart = end != null, backgroundContent = {
        // Drawn only while swiping, so idle rows carry no hidden text.
        val direction = state.dismissDirection
        val swipe = if (direction == SwipeToDismissBoxValue.EndToStart) end else if (direction == SwipeToDismissBoxValue.StartToEnd) start else null
        if (swipe != null) {
            val (background, foreground) = toneColors(swipe.tone)
            Row(Modifier.fillMaxSize().background(background).padding(horizontal = 24.dp), verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(8.dp, if (direction == SwipeToDismissBoxValue.EndToStart) Alignment.End else Alignment.Start)) {
                Glyph(swipe.icon, null, tint = foreground, size = 20.dp)
                Text(swipe.label, style = MaterialTheme.typography.labelLarge, color = foreground)
            }
        }
    }) { Surface(color = MaterialTheme.colorScheme.background) { row() } }
    GroupDivider()
}

@Composable
private fun EntryRowContent(model: LedgerModel, entry: JSONObject, view: String, repeats: List<JSONObject>, headline: String?, showProject: Boolean,
                            check: Boolean, open: () -> Unit) {
    val reading = view == "reading"
    val muted = MaterialTheme.colorScheme.onSurfaceVariant
    val done = entry.optJSONObject("resolved_by") != null || (reading && owner(entry).optBoolean("read"))
    val ring = check && entry.text("kind") == "todo" && !done
    Row(Modifier.fillMaxWidth().clickable(onClick = open).padding(start = if (ring) 4.dp else Gutter, end = Gutter, top = if (ring) 2.dp else 12.dp, bottom = 12.dp)
        .testTag("entry-${entry.text("id")}"), horizontalArrangement = Arrangement.spacedBy(if (ring) 0.dp else 12.dp)) {
        if (ring) IconButton(onClick = { resolve(model, entry) }, enabled = !model.busy) {
            Box(Modifier.size(20.dp).border(2.dp, muted, CircleShape).testTag("done-${entry.text("id")}"))
        } else Box(Modifier.padding(top = 1.dp)) { KindMark(entry, done) }
        Column(Modifier.weight(1f).padding(top = if (ring) 10.dp else 0.dp), verticalArrangement = Arrangement.spacedBy(3.dp)) {
            Row(verticalAlignment = Alignment.Top, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(headline ?: entryTitle(entry), Modifier.weight(1f), style = MaterialTheme.typography.titleSmall,
                    fontWeight = if (done) FontWeight.Normal else FontWeight.Medium, color = if (done) muted else MaterialTheme.colorScheme.onSurface,
                    maxLines = 2, overflow = TextOverflow.Ellipsis)
                if (reading && owner(entry).optBoolean("starred")) Glyph("star", "Starred", tint = ledgerColors.star, size = 18.dp)
            }
            val summary = if (headline != null) entryTitle(entry) else entrySummary(entry, reading)
            if (summary.isNotBlank()) Text(summary, style = MaterialTheme.typography.bodyMedium, color = muted, maxLines = 2, overflow = TextOverflow.Ellipsis)
            val context = buildList {
                if (showProject) add(entry.text("project_name"))
                if (reading) entry.optJSONObject("meta")?.text("source")?.takeIf { it.isNotBlank() }?.let { add(it) } else add(writerName(entry.text("source")))
                if (entry.optInt("replies") > 0) add("${entry.optInt("replies")} ${if (entry.optInt("replies") == 1) "reply" else "replies"}")
                add(ago(entry.text("created_at")))
                if (view == "activity") add(label(entry.text("kind")).lowercase())
            }.filter { it.isNotBlank() }
            Text(context.joinToString(" · "), style = MaterialTheme.typography.bodySmall, color = muted, maxLines = 1, overflow = TextOverflow.Ellipsis)
            val labels = rowLabels(entry)
            if (labels.isNotEmpty() || repeats.isNotEmpty()) FlowRow(Modifier.padding(top = 2.dp), horizontalArrangement = Arrangement.spacedBy(4.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                labels.forEach { (text, tone) -> Tag(text, tone) }
                if (repeats.isNotEmpty()) Tag("+${repeats.size} ${if (repeats.size == 1) "repeat" else "repeats"}", Tone.Neutral)
            }
        }
    }
}

/** Opens an entry on its own page; Back returns to the list where it was. */
fun openEntry(model: LedgerModel, entry: JSONObject) = model.go("entry-view/${segment(entry.text("id"))}")

/** One entry on its own screen: search results, related entries, and repeats open here. */
@Composable
fun EntryScreen(model: LedgerModel, id: String) =
    Load(model, "entry:$id", { it.request("GET", "/entries/${segment(id)}") }) { entry ->
        // Once an action that settles it succeeds (done, handled, answered, snoozed, deleted), return to the list;
        // a failure keeps the page open to try again.
        EntrySheet(model, entry, entry.rows("repeats"), close = model::back)
    }

/** An entry's page: what it is and who wrote it, its text and history, and below, its actions and a box to answer its agent. */
@Composable
private fun EntrySheet(model: LedgerModel, entry: JSONObject, repeats: List<JSONObject>, close: () -> Unit) {
    val open: (String) -> Unit = { target -> close(); model.go("entry-view/${segment(target)}") }
    val context = LocalContext.current
    val meta = entry.optJSONObject("meta")
    val id = entry.text("id")
    var deleting by remember { mutableStateOf(false) }
    var labeling by remember { mutableStateOf(false) }
    val read = owner(entry).optBoolean("read")
    val starred = owner(entry).optBoolean("starred")
    val run: (String) -> Unit = { action ->
        when (action) {
            "done" -> resolve(model, entry, after = close)
            "handled" -> ownerAction(model, entry, "Marked handled", "handled" to true, after = close)
            // Only a link that actually opened counts as read.
            "open_link" -> if (openBrowser(context, meta?.text("link").orEmpty(), model) && !read) ownerAction(model, entry, "Marked read", "read" to true)
            "reopen" -> model.undoable("Todo reopened") { it.request("POST", "/entries/${segment(id)}/reopen") }
            "snooze" -> ownerAction(model, entry, "Snoozed until tomorrow", "snooze_days" to 1, after = close)
            "read", "unread" -> ownerAction(model, entry, if (read) "Marked unread" else "Marked read", "read" to !read, after = if (read) ({}) else close)
            "star", "unstar" -> ownerAction(model, entry, if (starred) "Unstarred" else "Starred", "starred" to !starred)
            "calendar" -> addToCalendar(model, entry)
            "labels" -> labeling = true
            "project" -> { close(); model.go(projectRoute(entry.text("slug"))) }
            "delete" -> deleting = true
        }
    }
    val asking = asksYou(entry)
    val muted = MaterialTheme.colorScheme.onSurfaceVariant
    Column(Modifier.fillMaxSize()) {
        Column(Modifier.weight(1f).verticalScroll(rememberScrollState()).padding(horizontal = Gutter).padding(top = 4.dp, bottom = 24.dp),
            verticalArrangement = Arrangement.spacedBy(14.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                if (asking) Tag("Question for you", Tone.Warn) else Tag(label(entry.text("kind")), Tone.Neutral)
                Text(entry.text("project_name"), style = MaterialTheme.typography.bodySmall, color = muted)
            }
            // A question leads with what it asks; the AI's title for the entry follows.
            Text(if (asking) meta?.text("ask").orEmpty() else entryTitle(entry), style = MaterialTheme.typography.headlineSmall)
            if (asking) Text(entryTitle(entry), style = MaterialTheme.typography.titleSmall, color = muted)
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                AgentMark(entry.text("source"), 24.dp)
                Text("${writerName(entry.text("source"))} · ${displayTime(entry.text("created_at"))}", style = MaterialTheme.typography.bodySmall, color = muted)
            }
            val labels = focusLabels(entry).filterNot { asking && it.first == "Asks you" }
            if (labels.isNotEmpty()) FlowRow(horizontalArrangement = Arrangement.spacedBy(4.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) { labels.forEach { (t, tone) -> Tag(t, tone) } }
            entry.text("reply_to").takeIf { it.isNotBlank() }?.let { root -> LinkText("A reply to an earlier entry · open it") { open(root) } }
            meta?.let { LabelNotes(it) }
            entry.text("duplicate_of").takeIf { it.isNotBlank() }?.let { root -> LinkText("This repeats an earlier entry · open it") { open(root) } }
            entry.optJSONObject("resolved_by")?.let { by ->
                LinkText("Closed ${ago(by.text("created_at"))} ago by ${if (by.text("origin") == "model") "an agent's update" else "you"} · open it") { open(by.text("entry_id")) }
            }
            val details = meta?.optJSONObject("details") ?: JSONObject()
            if (meta != null) listOf(
                "Asks you" to meta.text("ask").takeUnless { asking }.orEmpty(), "Summary" to meta.text("gist").takeUnless { sameText(it, entry.text("body")) }.orEmpty(), "Next step" to meta.text("next_step"), "Blocked by" to meta.text("blocker"),
                (if (entry.text("kind") == "decision") "Why" else "Why it matters") to meta.text("why"),
                "Chose" to details.text("chosen"), "Turned down" to details.text("rejected"),
                "Checklist" to details.rows("checklist").joinToString("\n") { (if (it.optBoolean("done")) "☑ " else "☐ ") + it.text("text") },
                "Key numbers" to details.rows("numbers").joinToString(" · ") { "${it.text("label")}: ${it.text("value")}" },
                "About" to details.strings("entities").joinToString(", "),
                "Links" to details.strings("links").filter { it != meta.text("link") }.joinToString("\n"),
                "Source" to meta.text("source"),
            ).filter { it.second.isNotBlank() }.forEach { (name, value) -> Fact(name, value) }
            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
            SelectionContainer { Text(entry.text("body"), style = MaterialTheme.typography.bodyMedium, color = ledgerColors.body) }
            meta?.strings("refs")?.takeIf { it.isNotEmpty() }?.let { refs -> SelectionContainer { Text(refs.joinToString("\n"), style = MaterialTheme.typography.bodySmall, color = muted) } }
            if (repeats.isNotEmpty()) Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
                // The entry screen lists only the newest repeats; say when there are more.
                val total = entry.optInt("repeats_total", repeats.size)
                MiniHeading(if (total > repeats.size) "Repeats · the newest ${repeats.size} of $total" else "Repeats")
                repeats.forEach { repeat -> LinkText("${entryTitle(repeat)} · ${displayTime(repeat.text("created_at"))}") { open(repeat.text("id")) } }
            }
            History(model, entry, open)
            RelatedEntries(model, id, close)
            Text(when { meta == null -> "Summary pending."; meta.text("origin") == "model" -> "Title, summary, and labels generated by AI from the text above."; else -> "Written from the console." },
                style = MaterialTheme.typography.bodySmall, color = muted)
        }
        // Below the text: what settles the entry, then the answer to its agent, always in reach.
        Surface(color = MaterialTheme.colorScheme.surfaceContainerLowest) {
            Column {
                HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                Column(Modifier.padding(start = Gutter, end = 4.dp, top = 10.dp, bottom = 12.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                    val actions = entryActions(entry)
                    Row(verticalAlignment = Alignment.Top) {
                        FlowRow(Modifier.weight(1f), horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                            actions.primary?.let { action -> Button(onClick = { run(action) }, enabled = !model.busy, shape = ButtonShape) { Text(entryActionNames.getValue(action)) } }
                            actions.secondary.forEach { action -> OutlinedButton(onClick = { run(action) }, enabled = !model.busy, shape = ButtonShape) { Text(entryActionNames.getValue(action)) } }
                        }
                        Overflow("More actions for this entry", actions.overflow.map { action -> MenuAction(entryActionNames.getValue(action), danger = action == "delete") { run(action) } }, enabled = !model.busy)
                    }
                    Box(Modifier.padding(end = 12.dp)) { Composer(model, entry, asking = asking, sent = close) }
                }
            }
        }
    }
    if (labeling) LabelEditor(model, entry, close) { labeling = false }
    if (deleting) ConfirmDialog("Delete entry", "Move this entry to Trash? You can undo it or restore it from Trash for 30 days.", { deleting = false }, danger = true) {
        model.undoable("Entry moved to Trash", after = close) { it.request("DELETE", "/entries/${segment(id)}") }
    }
}

/** A muted heading inside a page. */
@Composable
fun MiniHeading(text: String) = Text(text, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)

/** A named fact: a muted name over its selectable value. */
@Composable
fun Fact(name: String, value: String) {
    Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
        MiniHeading(name)
        SelectionContainer { Text(value, style = MaterialTheme.typography.bodyMedium, color = ledgerColors.body) }
    }
}

/** A full-width link line; the whole row is the touch target. */
@Composable
fun LinkText(text: String, onClick: () -> Unit) {
    Text(text, Modifier.fillMaxWidth().heightIn(min = 40.dp).clickable(onClick = onClick).padding(vertical = 10.dp), color = MaterialTheme.colorScheme.primary,
        style = MaterialTheme.typography.bodySmall, maxLines = 2, overflow = TextOverflow.Ellipsis)
}

/** Adds a todo's due date as an all-day event in the first selected calendar. */
private fun addToCalendar(model: LedgerModel, entry: JSONObject) = model.act("Added to calendar") { api ->
    val due = LocalDate.parse(entry.optJSONObject("meta")?.text("due"))
    val calendar = api.request("GET", "/calendar/calendars").rows("calendars").firstOrNull { it.optBoolean("selected") }
        ?: throw IllegalStateException("Choose a calendar first: Settings › Access › Calendars.")
    api.request("POST", "/calendar/events", json("calendar_id" to calendar.text("id"), "title" to entryTitle(entry).take(200),
        "start" to due.toString(), "end" to due.plusDays(1).toString(), "all_day" to true, "location" to "",
        "description" to "Ledger todo in ${entry.text("project_name")}".take(4000)))
}

/** Your answer, saved under the entry where the agent reads it. */
@Composable
private fun Composer(model: LedgerModel, entry: JSONObject, asking: Boolean, sent: () -> Unit) {
    var text by rememberSaveable(entry.text("id")) { mutableStateOf("") }
    val agent = entry.text("source").takeIf { it != OWNER_SOURCE }.orEmpty()
    // Measured as sent: trimmed, and in characters like the server (an emoji is one).
    val length = text.trim().let { it.codePointCount(0, it.length) }
    val send = {
        val body = text.trim()
        model.undoable(if (asking) "Answer sent; marked handled" else "Reply saved", after = { text = ""; sent() }) {
            it.request("POST", "/entries/${segment(entry.text("id"))}/replies", json("body" to body))
        }
    }
    Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            OutlinedTextField(text, { text = it }, Modifier.weight(1f), maxLines = 5, shape = MaterialTheme.shapes.medium,
                label = { Text(if (asking) "Answer $agent" else "Reply") },
                placeholder = { Text(if (asking) "Type your answer" else if (agent.isNotBlank()) "A note or instruction for $agent" else "A note") },
                isError = length > 4000)
            FilledIconButton(onClick = send, enabled = !model.busy && length in 1..4000, modifier = Modifier.size(48.dp)) { Glyph("send", "Send", size = 20.dp) }
        }
        Text(buildString {
            append(if (agent.isNotBlank()) "$agent sees it the next time it checks Ledger." else "Saved under this entry.")
            if (asking) append(" Sending marks the question handled.")
            if (length > 4000) append(" Too long: keep it to 4,000 characters.")
        }, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

/** Where the entry came from and everything that happened to it since, oldest first. */
@Composable
private fun History(model: LedgerModel, entry: JSONObject, open: (String) -> Unit) {
    val client = model.api ?: return
    val id = entry.text("id")
    var history by remember(id) { mutableStateOf<JSONObject?>(null) }
    var failed by remember(id) { mutableStateOf(false) }
    var attempt by remember(id) { mutableStateOf(0) }
    LaunchedEffect(id, client, model.revision, attempt) {
        try {
            history = withContext(Dispatchers.IO) { client.request("GET", "/entries/${segment(id)}/history") }
            failed = false
        } catch (e: Exception) {
            if (e is CancellationException) throw e
            if (e is ApiError && e.status == 401) model.failed(e, client)
            // Keep what was shown; say the refresh failed.
            failed = true
        }
    }
    val events = history?.rows("history")
    val muted = MaterialTheme.colorScheme.onSurfaceVariant
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        MiniHeading("History")
        entry.text("context").takeIf { it.isNotBlank() }?.let { Text("Written from $it", style = MaterialTheme.typography.bodySmall, color = ledgerColors.body) }
        if (failed) Row(verticalAlignment = Alignment.CenterVertically) {
            Text(if (events == null) "Couldn't load the history." else "Couldn't refresh the history.", Modifier.weight(1f), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
            TextButton(onClick = { attempt++ }) { Text("Retry") }
        }
        if (history?.optBoolean("truncated") == true) Text("Older history is not shown; this lists the newest 200 events.", style = MaterialTheme.typography.bodySmall, color = muted)
        // A reply already says so; its "marked handled" twin would repeat it.
        events.orEmpty().filterNot { it.text("kind") == "action" && it.text("text").startsWith("Replied") }.forEach { event ->
            Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                // A dot per event, in the colour of whoever acted: the agent's, or ink for you.
                val actor = event.text("actor")
                Box(Modifier.padding(top = 5.dp).size(8.dp).background(if (actor == OWNER_SOURCE || actor.isBlank()) MaterialTheme.colorScheme.outline else toneColors(agentTone(actor)).second, CircleShape))
                Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    val target = event.text("entry_id")
                    val line = "${historyLine(event)} · ${ago(event.text("at"))} ago"
                    if (target.isNotBlank() && event.text("kind") != "reply") Text(line, Modifier.clickable { open(target) }, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.primary)
                    else Text(line, style = MaterialTheme.typography.bodySmall, color = ledgerColors.body)
                    if (event.text("kind") == "reply") Surface(color = MaterialTheme.colorScheme.surfaceVariant, shape = MaterialTheme.shapes.medium, modifier = Modifier.fillMaxWidth()) {
                        SelectionContainer { Text(event.text("text"), Modifier.padding(10.dp), style = MaterialTheme.typography.bodyMedium) }
                    }
                }
            }
        }
    }
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
        MiniHeading("Related")
        rows.forEach { r -> LinkText("${entryTitle(r)} · ${r.text("project_name")}") { close(); model.go("entry-view/${segment(r.text("id"))}") } }
    }
}
