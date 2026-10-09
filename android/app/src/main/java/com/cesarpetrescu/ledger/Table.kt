@file:OptIn(androidx.compose.material3.ExperimentalMaterial3Api::class, androidx.compose.foundation.layout.ExperimentalLayoutApi::class)
package com.cesarpetrescu.ledger

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
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
import androidx.compose.ui.text.input.ImeAction
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

/** An entry screen's actions: at most one filled [primary], a few outlined [secondary] ones, and the rest under ⋯. */
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

/** Offers notifications once, until turned on or dismissed. */
@Composable
private fun NotificationPrompt(model: LedgerModel) {
    val context = LocalContext.current
    var hidden by remember { mutableStateOf(Notifier.enabled(context) || Notifier.promptDismissed(context)) }
    val toggle = rememberNotificationSwitch(model) { if (it) hidden = true }
    if (hidden) return
    OutlinedCard(Modifier.fillMaxWidth().padding(horizontal = 20.dp, vertical = 8.dp)) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Text("Get a notification when an agent asks you something?", style = MaterialTheme.typography.titleSmall)
            Text("Also when a todo becomes overdue. Your phone checks your own server about every 15 minutes.", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Button(onClick = { toggle(true) }) { Text("Turn on") }
                TextButton(onClick = { Notifier.dismissPrompt(context); hidden = true }) { Text("Not now") }
            }
        }
    }
}

/** Whether a summary only repeats the text it sums up, spacing, case, and a final period aside. */
fun sameText(a: String, b: String): Boolean {
    val plain = { text: String -> text.trim().trimEnd('.').replace(Regex("\\s+"), " ") }
    return plain(a).equals(plain(b), ignoreCase = true)
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

// ---- Rows, swipe actions, and the entry page ----

/** A tone's background and text colours, for labels and the project's stat tiles. */
@Composable
fun toneColors(tone: Tone): Pair<Color, Color> {
    val scheme = MaterialTheme.colorScheme
    return when (tone) {
        Tone.Danger -> scheme.errorContainer to scheme.onErrorContainer
        Tone.Warn -> if (isDark()) Color(0xFF3A2C13) to Color(0xFFF3C46E) else Color(0xFFFFF0D6) to Color(0xFF7A4E00)
        Tone.Accent -> scheme.primaryContainer to scheme.onPrimaryContainer
        Tone.Good -> if (isDark()) Color(0xFF163326) to Color(0xFF82D8AA) else Color(0xFFDDF3E6) to Color(0xFF1E6B45)
        Tone.Neutral -> scheme.surfaceVariant to scheme.onSurfaceVariant
    }
}

@Composable
fun Tag(text: String, tone: Tone) {
    val (background, foreground) = toneColors(tone)
    Surface(color = background, contentColor = foreground, shape = MaterialTheme.shapes.small) {
        Text(text, Modifier.padding(horizontal = 6.dp, vertical = 2.dp), style = MaterialTheme.typography.labelSmall, maxLines = 1)
    }
}

private fun owner(entry: JSONObject) = entry.optJSONObject("owner") ?: JSONObject()

private fun resolve(model: LedgerModel, entry: JSONObject, after: () -> Unit = {}) =
    model.undoable("Todo marked done", after = after) { it.request("POST", "/entries/${segment(entry.text("id"))}/resolve") }

private fun ownerAction(model: LedgerModel, entry: JSONObject, message: String, vararg patch: Pair<String, Any?>, after: () -> Unit = {}) =
    model.undoable(message, after = after) { it.request("POST", "/entries/${segment(entry.text("id"))}/owner", json(*patch)) }

/** The two swipe actions a row offers, if any: start-to-end, then end-to-start. */
private fun swipeActions(model: LedgerModel, entry: JSONObject, view: String): Pair<Pair<String, () -> Unit>?, Pair<String, () -> Unit>?> {
    val asking = asksYou(entry)
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
            if (reading) entry.optJSONObject("meta")?.text("source")?.takeIf { it.isNotBlank() }?.let { add(it) } else add(writerName(entry.text("source")))
            if (entry.optInt("replies") > 0) add("${entry.optInt("replies")} ${if (entry.optInt("replies") == 1) "reply" else "replies"}")
            add(ago(entry.text("created_at")))
            if (view == "activity") add(label(entry.text("kind")))
        }.filter { it.isNotBlank() }
        Text(context.joinToString(" · "), style = MaterialTheme.typography.labelSmall, color = muted, maxLines = 1, overflow = TextOverflow.Ellipsis)
        Row(verticalAlignment = Alignment.Top, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Text(headline ?: entryTitle(entry), Modifier.weight(1f), style = MaterialTheme.typography.titleSmall,
                fontWeight = if (done) FontWeight.Normal else FontWeight.SemiBold, color = if (done) muted else MaterialTheme.colorScheme.onSurface,
                maxLines = 2, overflow = TextOverflow.Ellipsis)
            if (reading && owner(entry).optBoolean("starred")) Text("★", color = if (isDark()) Color(0xFFF2C14E) else Color(0xFFB88A00))
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

/** Opens an entry on its own page; Back returns to the list where it was. */
fun openEntry(model: LedgerModel, entry: JSONObject) = model.go("entry-view/${segment(entry.text("id"))}")

/** One entry on its own screen: search results, related entries, and repeats open here. */
@Composable
fun EntryScreen(model: LedgerModel, id: String) =
    Load(model, "entry:$id", { it.request("GET", "/entries/${segment(id)}") }) { entry ->
        // Once an action that settles it succeeds (done, handled, answered, snoozed, deleted), return to the list;
        // a failure keeps the page open to try again.
        EntrySheet(model, entry, entry.rows("repeats"), close = model::back, afterDelete = {})
    }

/** An entry's details on its own page. */
@Composable
private fun EntrySheet(model: LedgerModel, entry: JSONObject, repeats: List<JSONObject>, close: () -> Unit, afterDelete: () -> Unit = {}) {
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
    // imePadding keeps the reply box and its Send button above the keyboard on small phones.
    Column(Modifier.fillMaxWidth().imePadding().verticalScroll(rememberScrollState()).padding(horizontal = 20.dp).navigationBarsPadding().padding(bottom = 24.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text("${entry.text("project_name")} · ${label(entry.text("kind"))} · ${writerName(entry.text("source"))} · ${displayTime(entry.text("created_at"))}",
            style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Text(entryTitle(entry), style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.SemiBold)
        val labels = focusLabels(entry)
        if (labels.isNotEmpty()) FlowRow(horizontalArrangement = Arrangement.spacedBy(4.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) { labels.forEach { (t, tone) -> Tag(t, tone) } }
        // One filled button for what settles the entry; the rest are outlined or under ⋯.
        val actions = entryActions(entry)
        Row(verticalAlignment = Alignment.Top) {
            FlowRow(Modifier.weight(1f), horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                actions.primary?.let { action -> Button(onClick = { run(action) }, enabled = !model.busy) { Text(entryActionNames.getValue(action)) } }
                actions.secondary.forEach { action -> OutlinedButton(onClick = { run(action) }, enabled = !model.busy) { Text(entryActionNames.getValue(action)) } }
            }
            Overflow("More actions for this entry", actions.overflow.map { action -> MenuAction(entryActionNames.getValue(action), danger = action == "delete") { run(action) } }, enabled = !model.busy)
        }
        if (labeling) LabelEditor(model, entry, close) { labeling = false }
        if (deleting) ConfirmDialog("Delete entry", "Move this entry to Trash? You can undo it or restore it from Trash for 30 days.", { deleting = false }, danger = true) {
            model.undoable("Entry moved to Trash", after = { afterDelete(); close() }) { it.request("DELETE", "/entries/${segment(id)}") }
        }
        entry.text("reply_to").takeIf { it.isNotBlank() }?.let { root ->
            TextButton(onClick = { open(root) }, contentPadding = PaddingValues(0.dp)) { Text("A reply to an earlier entry · open it", style = MaterialTheme.typography.bodySmall) }
        }
        val asking = asksYou(entry)
        // The question comes before the box that answers it.
        if (asking) Column {
            Text("Asks you", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            SelectionContainer { Text(meta?.text("ask").orEmpty(), style = MaterialTheme.typography.bodyMedium) }
        }
        ReplyBox(model, entry, asking = asking, sent = close)
        meta?.let { LabelNotes(it) }
        entry.text("duplicate_of").takeIf { it.isNotBlank() }?.let { root ->
            TextButton(onClick = { open(root) }, contentPadding = PaddingValues(0.dp)) { Text("This repeats an earlier entry · open it", style = MaterialTheme.typography.bodySmall) }
        }
        entry.optJSONObject("resolved_by")?.let { by ->
            TextButton(onClick = { open(by.text("entry_id")) }, contentPadding = PaddingValues(0.dp)) {
                Text("Closed ${ago(by.text("created_at"))} ago by ${if (by.text("origin") == "model") "an agent's update" else "you"} · open it", style = MaterialTheme.typography.bodySmall)
            }
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
        ).filter { it.second.isNotBlank() }.forEach { (name, value) ->
            Column { Text(name, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant); SelectionContainer { Text(value, style = MaterialTheme.typography.bodyMedium) } }
        }
        HorizontalDivider()
        SelectionContainer { Text(entry.text("body"), style = MaterialTheme.typography.bodyMedium) }
        meta?.strings("refs")?.takeIf { it.isNotEmpty() }?.let { refs -> SelectionContainer { Text(refs.joinToString("\n"), style = MaterialTheme.typography.bodySmall) } }
        if (repeats.isNotEmpty()) Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
            // The entry screen lists only the newest repeats; say when there are more.
            val total = entry.optInt("repeats_total", repeats.size)
            Text(if (total > repeats.size) "Repeats · the newest ${repeats.size} of $total" else "Repeats", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            repeats.forEach { repeat ->
                TextButton(onClick = { open(repeat.text("id")) }, contentPadding = PaddingValues(0.dp)) {
                    Text("${entryTitle(repeat)} · ${displayTime(repeat.text("created_at"))}", style = MaterialTheme.typography.bodySmall, maxLines = 2, overflow = TextOverflow.Ellipsis)
                }
            }
        }
        History(model, entry, open)
        RelatedEntries(model, id, close)
        Text(when { meta == null -> "Summary pending."; meta.text("origin") == "model" -> "Title, summary, and labels generated by AI from the text above."; else -> "Written from the console." },
            style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
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

/** Who the owner is shown as: "You" for anything written from the console or this phone. */
fun writerName(source: String) = if (source == OWNER_SOURCE) "You" else source

const val OWNER_SOURCE = "ledger-admin"

/** An agent waits on your answer; your own entries never ask you anything. */
fun asksYou(entry: JSONObject) = entry.optJSONObject("meta")?.text("ask")?.isNotBlank() == true &&
    !owner(entry).optBoolean("handled") && entry.text("source") != OWNER_SOURCE

/** Your answer, saved under the entry where the agent reads it. */
@Composable
private fun ReplyBox(model: LedgerModel, entry: JSONObject, asking: Boolean, sent: () -> Unit) {
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
        // Send sits inside the field, so the keyboard never hides it on a small phone.
        OutlinedTextField(text, { text = it }, Modifier.fillMaxWidth(), minLines = if (asking) 3 else 2,
            label = { Text(if (asking) "Answer $agent" else "Reply") },
            placeholder = { Text(if (asking) "Type your answer" else if (agent.isNotBlank()) "A note or instruction for $agent" else "A note") },
            isError = length > 4000,
            trailingIcon = { TextButton(onClick = send, enabled = !model.busy && length in 1..4000) { Text("Send") } })
        Text(buildString {
            append(if (agent.isNotBlank()) "$agent sees it the next time it checks Ledger." else "Saved under this entry.")
            if (asking) append(" Sending marks the question handled.")
            if (length > 4000) append(" Too long: keep it to 4,000 characters.")
        }, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

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
    Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
        Text("History", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        entry.text("context").takeIf { it.isNotBlank() }?.let { Text("Written from $it", style = MaterialTheme.typography.bodySmall) }
        if (failed) Row(verticalAlignment = Alignment.CenterVertically) {
            Text(if (events == null) "Couldn't load the history." else "Couldn't refresh the history.", Modifier.weight(1f), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
            TextButton(onClick = { attempt++ }) { Text("Retry") }
        }
        if (history?.optBoolean("truncated") == true) Text("Older history is not shown; this lists the newest 200 events.", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        // A reply already says so; its "marked handled" twin would repeat it.
        events.orEmpty().filterNot { it.text("kind") == "action" && it.text("text").startsWith("Replied") }.forEach { event ->
            Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
                val target = event.text("entry_id")
                val line = "${historyLine(event)} · ${ago(event.text("at"))} ago"
                if (target.isNotBlank() && event.text("kind") != "reply") TextButton(onClick = { open(target) }, contentPadding = PaddingValues(0.dp)) { Text(line, style = MaterialTheme.typography.bodySmall) }
                else Text(line, style = MaterialTheme.typography.bodySmall)
                if (event.text("kind") == "reply") Surface(color = MaterialTheme.colorScheme.surfaceVariant, shape = MaterialTheme.shapes.small, modifier = Modifier.fillMaxWidth()) {
                    SelectionContainer { Text(event.text("text"), Modifier.padding(10.dp), style = MaterialTheme.typography.bodyMedium) }
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
        Text("Related", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        rows.forEach { r ->
            TextButton(onClick = { close(); model.go("entry-view/${segment(r.text("id"))}") }, contentPadding = PaddingValues(0.dp)) {
                Text("${entryTitle(r)} · ${r.text("project_name")}", style = MaterialTheme.typography.bodySmall, maxLines = 2, overflow = TextOverflow.Ellipsis)
            }
        }
    }
}

@Composable
private fun SectionHeader(text: String, action: (@Composable () -> Unit)? = null) {
    Row(Modifier.fillMaxWidth().padding(start = 20.dp, end = 8.dp, top = 20.dp, bottom = 4.dp), verticalAlignment = Alignment.CenterVertically) {
        // A heading, not a link: blue is kept for what can be tapped, such as "All todos" beside it.
        Text(text, Modifier.weight(1f), style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.Bold)
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
    Load(model, "inbox", { it.request("GET", "/inbox") }) { data ->
        val asks = data.rows("needs_you")
        val todos = data.rows("todos")
        val projects = data.rows("projects")
        val blocked = projects.filter { it.text("status_state") == "blocked" }
        val digests = projects.filter { it.text("digest").isNotBlank() }
        LaunchedEffect(data) { model.knowProjects(projects) }
        LazyColumn(Modifier.fillMaxSize().testTag("page"), contentPadding = PaddingValues(bottom = 24.dp)) {
            item {
                Column(Modifier.padding(start = 20.dp, end = 8.dp, top = 8.dp)) {
                    Text("Questions your agents are waiting on you to answer, the most urgent todos, and each project's week.", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    LegendButton()
                }
                AiStatus(data.optJSONObject("metadata"))
                NotificationPrompt(model)
            }
            item { SectionHeader("Needs you · ${asks.size}") }
            if (asks.isEmpty()) item { Box(Modifier.padding(horizontal = 20.dp)) { Empty("Nothing is waiting on you.") } }
            items(asks, key = { "a" + it.text("id") }) { e -> EntryItem(model, e, "inbox", headline = e.optJSONObject("meta")?.text("ask")) { entry, _ -> openEntry(model, entry) } }
            item {
                val total = data.optInt("todos_total")
                SectionHeader("Todos · ${if (todos.size < total) "${todos.size} of $total" else "$total"}") { if (total > 0) TextButton(onClick = { model.go("todos") }) { Text("All todos") } }
            }
            if (todos.isEmpty()) item { Box(Modifier.padding(horizontal = 20.dp)) { Empty("No open todos. Nice.") } }
            items(todos, key = { "t" + it.text("id") }) { e -> EntryItem(model, e, "inbox") { entry, _ -> openEntry(model, entry) } }
            if (blocked.isNotEmpty()) {
                item { SectionHeader("Blocked · ${blocked.size}") }
                items(blocked, key = { "b" + it.text("slug") }) { p -> ProjectLine(model, p, p.text("status_detail").ifBlank { p.text("status_title") }) }
            }
            if (digests.isNotEmpty()) {
                item { SectionHeader("This week") }
                items(digests, key = { "d" + it.text("slug") }) { p -> ProjectLine(model, p, p.text("digest")) }
            }
        }
    }
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
    LaunchedEffect(data) { model.knowProjects(data.rows("projects")) }
    LazyColumn(Modifier.fillMaxSize().testTag("page"), contentPadding = PaddingValues(bottom = 88.dp)) {
        if (aiStatusText(progress) != null) item {
            AiStatus(progress)
        }
        item { Row(Modifier.padding(horizontal = 20.dp, vertical = 8.dp)) { Button(onClick = { model.go("project-edit/") }, enabled = !model.busy) { Text("New project") } } }
        if (data.rows("projects").isEmpty()) item { Box(Modifier.padding(20.dp)) { Empty("No projects yet.") } }
        items(data.rows("projects"), key = { it.text("slug") }) { p ->
            Column(Modifier.fillMaxWidth().clickable { model.go(projectRoute(p.text("slug"))) }.padding(horizontal = 20.dp, vertical = 14.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                    Text(p.text("name"), Modifier.weight(1f, fill = false), style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    HealthTag(p.text("status_state"))
                }
                // Facts are metadata, not links: the whole row opens the project.
                Text(projectFacts(p), style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                val summary = p.text("digest").ifBlank { p.text("status_title").ifBlank { p.text("status_body") } }
                if (summary.isNotBlank()) Text(summary, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 3, overflow = TextOverflow.Ellipsis)
            }
            HorizontalDivider()
        }
    }
}

/** A project row's facts. A date deadline reads "due 15 Nov 2026" and wraps as one piece. */
fun projectFacts(p: JSONObject): String = buildList {
    add(label(p.text("tier")))
    if (p.optInt("open_todos") > 0) add(plural(p.optInt("open_todos"), "open todo"))
    if (p.optInt("needs_you") > 0) add(plural(p.optInt("needs_you"), "needs you", "need you"))
    add(if (p.optInt("week_entries") > 0) "${p.optInt("week_entries")} this week" else "quiet")
    val deadline = p.text("deadline")
    // Free text stays as written; only a date is shortened and held together.
    val date = deadlineDate(deadline)?.replace(' ', '\u00A0')
    if (deadline.isNotBlank()) add("due\u00A0" + (date ?: deadline))
}.joinToString(" · ")

/** A date deadline as "15 Nov 2026"; null for free text such as "after the beta". */
fun deadlineDate(deadline: String): String? = runCatching { LocalDate.parse(deadline).format(DateTimeFormatter.ofPattern("d MMM yyyy")) }.getOrNull()

/** A project's sections, in the web console's order. */
val projectTabs = listOf("activity" to "Activity", "todos" to "Todos", "decisions" to "Decisions", "details" to "Details",
    "handoffs" to "Handoffs", "files" to "Files", "repos" to "Repos")

/** A project screen's slug, tab, and search from its route; the older project-files/ and project-repos/ routes open those tabs. */
fun projectScreenArgs(route: String): Triple<String, String, String> {
    val parts = route.split('/').map { java.net.URLDecoder.decode(it, Charsets.UTF_8.name()) }
    val tab = when (parts.first()) { "project-files" -> "files"; "project-repos" -> "repos"; else -> parts.getOrElse(2) { "" } }
    return Triple(parts.getOrElse(1) { "" }, tab.takeIf { t -> projectTabs.any { it.first == t } } ?: "activity",
        if (parts.first() == "project") parts.getOrElse(3) { "" } else "")
}

/** One of a project's stat tiles: a count, what it counts, and what a tap [opens] ("inbox", a tab, or "" for nothing). */
data class ProjectStat(val count: Int, val label: String, val opens: String)

/** The web's stat tiles: questions waiting for you, open todos, entries this week, and the agents who wrote them. */
fun projectStats(p: JSONObject, now: OffsetDateTime = OffsetDateTime.now()): List<ProjectStat> {
    val asks = p.optInt("needs_you")
    val todos = p.optInt("open_todos")
    val week = p.optInt("week_entries")
    val agents = p.strings("week_agents")
    return listOf(
        ProjectStat(asks, if (asks == 1) "question waits for you" else "questions wait for you", "inbox"),
        ProjectStat(todos, if (todos == 1) "open todo" else "open todos", "todos"),
        ProjectStat(week, (if (week == 1) "entry" else "entries") + " this week", "activity"),
        ProjectStat(agents.size, when {
            agents.isNotEmpty() -> agents.joinToString(", ", transform = ::writerName)
            p.text("last_entry_at").isNotBlank() -> "quiet; last entry ${ago(p.text("last_entry_at"), now)} ago"
            else -> "agents this week"
        }, ""),
    )
}

/** The stat tiles in one compact row, so the tabs stay in sight on a small phone; a question waiting for you is amber, like the web. */
@Composable
private fun ProjectStatTiles(stats: List<ProjectStat>, open: (String) -> Unit) {
    Row(Modifier.fillMaxWidth().height(IntrinsicSize.Min), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
        stats.forEach { stat ->
            val ask = stat.opens == "inbox" && stat.count > 0
            val (background, accent) = if (ask) toneColors(Tone.Warn) else MaterialTheme.colorScheme.background to MaterialTheme.colorScheme.onSurface
            Surface(Modifier.weight(1f).fillMaxHeight(), shape = MaterialTheme.shapes.small, color = background,
                border = BorderStroke(1.dp, if (ask) accent else MaterialTheme.colorScheme.outlineVariant)) {
                Column(Modifier.fillMaxSize().then(if (stat.opens.isBlank()) Modifier else Modifier.clickable { open(stat.opens) })
                    .padding(horizontal = 8.dp, vertical = 6.dp)) {
                    Text("${stat.count}", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold, color = accent)
                    Text(stat.label, style = MaterialTheme.typography.labelSmall, color = if (ask) accent else MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 2, overflow = TextOverflow.Ellipsis)
                }
            }
        }
    }
}

/** One project: where it stands, then its activity, todos, decisions, details, handoffs, files, and repos, like the web. */
@Composable
fun ProjectScreen(model: LedgerModel, slug: String, initialTab: String = "activity", initialQuery: String = "") {
    var tab by rememberSaveable { mutableStateOf(initialTab.takeIf { t -> projectTabs.any { it.first == t } } ?: "activity") }
    var q by rememberSaveable { mutableStateOf(initialQuery) }
    var showRoutine by rememberSaveable { mutableStateOf(initialQuery.isNotBlank()) }
    var todoState by rememberSaveable { mutableStateOf("open") }
    Load(model, "project-summary:$slug", { api -> api.request("GET", "/table/projects").rows("projects").firstOrNull { it.text("slug") == slug } ?: throw ApiError(404, "Project not found.") }) { p ->
        // The top bar names the project; the header says how it is doing and offers one main action.
        LaunchedEffect(p) { model.knowProjects(listOf(p)) }
        Column {
            Column(Modifier.padding(start = 20.dp, end = 8.dp, top = 4.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                // The tiles keep a 20dp margin on both sides; the column's narrower end is for the ⋯ button's own padding.
                Box(Modifier.padding(end = 12.dp, bottom = 4.dp)) {
                    ProjectStatTiles(projectStats(p)) { target -> if (target == "inbox") model.tab("inbox") else { tab = target; if (target == "todos") todoState = "open" } }
                }
                HealthTag(p.text("status_state"))
                // Tap the summary to read it all, including what the project needs from you.
                var expanded by rememberSaveable { mutableStateOf(false) }
                val summary = p.text("digest").ifBlank { p.text("status_title") }
                if (summary.isNotBlank()) Text(summary, Modifier.clickable { expanded = !expanded }, style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = if (expanded) Int.MAX_VALUE else 2, overflow = TextOverflow.Ellipsis)
                if (expanded && p.text("needs_me").isNotBlank()) Text("Needs you: ${p.text("needs_me")}", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.tertiary)
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Button(onClick = { model.go("entry/$slug") }, enabled = !model.busy) { Text("Add entry") }
                    Spacer(Modifier.weight(1f))
                    var deleting by remember { mutableStateOf(false) }
                    Overflow("More actions for ${p.text("name")}", listOf(
                        MenuAction("Edit project") { model.go("project-edit/$slug") },
                        MenuAction("Delete project", danger = true) { deleting = true }), enabled = !model.busy)
                    if (deleting) DeleteProjectDialog(model, slug) { deleting = false }
                }
            }
            // Seven tabs scroll sideways on a phone; the selected one scrolls into view.
            PrimaryScrollableTabRow(selectedTabIndex = projectTabs.indexOfFirst { it.first == tab }, edgePadding = 4.dp) {
                projectTabs.forEach { (id, name) -> Tab(selected = tab == id, onClick = { tab = id }, text = { Text(name) },
                    // Only the selected tab is blue; the rest are muted, like other metadata.
                    selectedContentColor = MaterialTheme.colorScheme.primary, unselectedContentColor = MaterialTheme.colorScheme.onSurfaceVariant) }
            }
            when (tab) {
                "details" -> ProjectDetails(model, slug, p)
                "handoffs" -> HandoffList(model, "archive=all&project=${segment(slug)}", empty = "No handoffs are linked to this project.", showProject = false)
                "files" -> ProjectFiles(model, slug)
                "repos" -> ProjectRepos(model, slug)
                else -> {
                    // Entries load only while one of their tabs is open. A search looks everywhere, routine entries included.
                    val query = tableQuery(tab, project = slug, q = q, status = todoState, hideRoutine = !showRoutine && q.isBlank())
                    val pager = rememberPager(model, query)
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
                            items(group, key = { it.entry.text("id") }) { f -> EntryItem(model, f.entry, tab, f.repeats, showProject = false) { e, _ -> openEntry(model, e) } }
                        }
                    }
                }
            }
        }
    }
}

private fun priorityRank(entry: JSONObject) = when (entry.optJSONObject("meta")?.text("priority")) { "high" -> 0; "low" -> 2; else -> 1 }

/** Open todos across projects, grouped by project and most important first. */
@Composable
fun TodosScreen(model: LedgerModel) {
    val query = tableQuery("todos", status = "open")
    val pager = rememberPager(model, query)
    PagedEntries(model, pager, query, empty = "No open todos. Nice.") { entries ->
        val heads = foldRepeats(entries).sortedWith(compareBy({ it.entry.text("project_name") }, { it.entry.text("slug") }, { priorityRank(it.entry) }))
        runsBy(heads) { it.entry.text("slug") }.forEach { (slug, group) ->
            item(key = "h:$slug") { SectionHeader(group.first().entry.text("project_name")) }
            items(group, key = { it.entry.text("id") }) { f -> EntryItem(model, f.entry, "todos", f.repeats, showProject = false) { e, _ -> openEntry(model, e) } }
        }
    }
}

/** Linked news-style entries: headline, why it matters, and read/star triage. */
@Composable
fun ReadingScreen(model: LedgerModel) {
    var filter by rememberSaveable { mutableStateOf("unread") }
    val query = tableQuery("reading", reading = filter)
    val pager = rememberPager(model, query)
    Column {
        FilterChips(listOf("unread" to "Unread", "starred" to "Starred", "all" to "All"), filter) { filter = it }
        if (filter == "unread" && pager.entries.isNotEmpty()) TextButton(onClick = {
            // Everything unread, not just this page; one Undo puts it all back.
            model.undoable("Marked everything read") { it.request("POST", "/reading/read-all?reading=unread") }
        }, enabled = !model.busy, modifier = Modifier.padding(horizontal = 12.dp)) { Text("Mark all read") }
        PagedEntries(model, pager, query, empty = if (filter == "unread") "Nothing left to read." else "Nothing here yet.") { entries ->
            items(foldRepeats(entries), key = { it.entry.text("id") }) { f -> EntryItem(model, f.entry, "reading", f.repeats) { e, _ -> openEntry(model, e) } }
        }
    }
}

/** A project's name, with its slug when another project shares the name. */
private fun projectName(p: JSONObject, all: List<JSONObject>) =
    if (all.count { it.text("name") == p.text("name") } > 1) "${p.text("name")} (${p.text("slug")})" else p.text("name")

private val tableViews = listOf("activity" to "Activity", "decisions" to "Decisions", "todos" to "Todos")

/** Every project's entries in one filterable list, like the web console's Table. */
@Composable
fun TableScreen(model: LedgerModel, initialSource: String = "") = Load(model, "table-projects", { it.request("GET", "/projects") }) { data ->
    val projects = data.rows("projects")
    var view by rememberSaveable { mutableStateOf("activity") }
    var project by rememberSaveable { mutableStateOf("") }
    var source by rememberSaveable { mutableStateOf(initialSource) }
    var tag by rememberSaveable { mutableStateOf("") }
    var kind by rememberSaveable { mutableStateOf("") }
    var todoState by rememberSaveable { mutableStateOf("open") }
    var showRoutine by rememberSaveable { mutableStateOf(false) }
    var typed by rememberSaveable { mutableStateOf("") }
    var q by rememberSaveable { mutableStateOf("") }
    var filtersOpen by rememberSaveable { mutableStateOf(false) }
    // A search looks everywhere, routine entries included.
    val query = tableQuery(view, project = project, source = source, tag = tag, status = todoState, q = q, kind = kind, hideRoutine = !showRoutine && q.isBlank())
    val pager = rememberPager(model, query)
    val active = listOf(project, source, tag, if (view == "activity") kind else "").count { it.isNotBlank() }
    PagedEntries(model, pager, query, empty = if (view == "todos" && todoState == "open") "No open todos. Nice." else "No entries match.", header = {
        item {
            Column(Modifier.padding(top = 4.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                FilterChips(tableViews, view) { view = it }
                Row(Modifier.padding(horizontal = 20.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    OutlinedTextField(typed, { if (validFieldText(it, 1000, false)) typed = it }, Modifier.weight(1f).testTag("table-search"), singleLine = true,
                        placeholder = { Text("Search every project") },
                        keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search), keyboardActions = KeyboardActions(onSearch = { q = typed.trim() }),
                        trailingIcon = { IconButton(onClick = { q = typed.trim() }) { Glyph("search", "Search") } })
                    BadgedBox(badge = { if (active > 0) Badge { Text("$active") } }) {
                        FilledTonalIconButton(onClick = { filtersOpen = !filtersOpen }) { Glyph("filter", "Filters") }
                    }
                }
                if (filtersOpen) Column(Modifier.padding(horizontal = 20.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Choice("Project", project, listOf("" to "All projects") + projects.map { p -> p.text("slug") to projectName(p, projects) }) { project = it }
                    Choice("Agent", source, listOf("" to "All agents") + pager.data?.strings("sources").orEmpty().map { it to it }) { source = it }
                    Choice("Tag", tag, listOf("" to "All tags") + pager.data?.strings("tags").orEmpty().map { it to it }) { tag = it }
                    if (view == "activity") Choice("Kind", kind, listOf("" to "All kinds") + entryKinds) { kind = it }
                    if (active > 0) TextButton(onClick = { project = ""; source = ""; tag = ""; kind = "" }) { Text("Clear filters") }
                }
                Row(Modifier.padding(horizontal = 20.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    if (view == "todos") listOf("open" to "Open", "done" to "Done", "" to "All").forEach { (id, name) ->
                        FilterChip(selected = todoState == id, onClick = { todoState = id }, label = { Text(name) })
                    } else FilterChip(selected = showRoutine, onClick = { showRoutine = !showRoutine }, label = { Text("Show routine") })
                    if (q.isNotBlank()) AssistChip(onClick = { q = ""; typed = "" }, label = { Text("Search: $q ✕") })
                }
            }
        }
    }) { entries ->
        val folded = foldRepeats(entries).let { heads -> if (view == "todos") heads.sortedWith(compareBy({ it.entry.text("project_name") }, { it.entry.text("slug") }, { priorityRank(it.entry) })) else heads }
        runsBy(folded) { if (view == "todos") it.entry.text("slug") else dayLabel(it.entry.text("created_at")) }.forEach { (key, group) ->
            item(key = "h:$key:${group.first().entry.text("id")}") { SectionHeader(if (view == "todos") projects.firstOrNull { it.text("slug") == key }?.let { projectName(it, projects) } ?: group.first().entry.text("project_name") else key) }
            items(group, key = { it.entry.text("id") }) { f ->
                EntryItem(model, f.entry, view, f.repeats, showProject = view != "todos" && project.isBlank()) { e, _ -> openEntry(model, e) }
            }
        }
    }
}

@Composable
fun MoreScreen(model: LedgerModel) {
    Page {
        listOf(
            Triple("Table", "Every project's activity, decisions, and todos, with filters", "table"),
            Triple("Calendar", "Your Nextcloud events", "calendar"),
            Triple("Search", "Find projects, decisions, and notes", "search"),
            Triple("Recent actions", "Undo what you marked, snoozed, or deleted", "history"),
            Triple("Trash", "Restore deleted projects and entries", "trash"),
            Triple("Agents", "What each agent did lately", "agents"),
            Triple("Help", "How Ledger works and what the labels mean", "help"),
            Triple("Settings", "Access, appearance, notifications, and sign out", "settings"),
        ).forEach { (title, subtitle, route) ->
            item { ListItem(headlineContent = { Text(title) }, supportingContent = { Text(subtitle) }, modifier = Modifier.clickable { model.go(route) },
                trailingContent = { Glyph("chevron", null, tint = MaterialTheme.colorScheme.onSurfaceVariant) }); HorizontalDivider() }
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
            item { Choice("Project", slug, projects.map { p -> p.text("slug") to projectName(p, projects) }) { slug = it } }
            item { Choice("Kind", kind, entryKinds) { kind = it } }
            item { Field("Text", body, { body = it }, multiline = true, max = 4000) }
            item { Text("Entries can't be edited. Add a correction as a new entry.", style = MaterialTheme.typography.bodySmall) }
            item { Button(onClick = { model.act("Entry added", after = model::back) { it.request("POST", "/projects/${segment(slug)}/entries", json("kind" to kind, "body" to body.trim())) } },
                enabled = !model.busy && validProjectSlug(slug) && body.isNotBlank(), modifier = Modifier.fillMaxWidth()) { Text("Add") } }
        }
    }
}
