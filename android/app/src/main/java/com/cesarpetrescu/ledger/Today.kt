package com.cesarpetrescu.ledger

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import org.json.JSONObject

/** Today: what agents wait on you for, the most urgent todos, blocked projects, and each project's week. */
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
                AiStatus(data.optJSONObject("metadata"))
                NotificationPrompt(model)
            }
            item { SectionLabel("Needs you", count = asks.size, countTone = if (asks.isEmpty()) Tone.Neutral else Tone.Warn) }
            if (asks.isEmpty()) item { Box(Modifier.padding(horizontal = Gutter)) { Empty("Nothing is waiting on you.") } }
            items(asks, key = { "a" + it.text("id") }) { e -> QuestionCard(model, e) }
            item {
                val total = data.optInt("todos_total")
                SectionLabel("Todos", count = total) { if (total > 0) TextButton(onClick = { model.go("todos") }) { Text("All todos") } }
            }
            if (todos.isEmpty()) item { Box(Modifier.padding(horizontal = Gutter)) { Empty("No open todos. Nice.") } }
            items(todos, key = { "t" + it.text("id") }) { e -> EntryItem(model, e, "inbox", check = true) { entry, _ -> openEntry(model, entry) } }
            if (blocked.isNotEmpty()) {
                item { SectionLabel("Blocked", count = blocked.size, countTone = Tone.Danger) }
                items(blocked, key = { "b" + it.text("slug") }) { p -> ProjectLine(model, p, p.text("status_detail").ifBlank { p.text("status_title") }) }
            }
            if (digests.isNotEmpty()) {
                item { SectionLabel("This week") }
                items(digests, key = { "d" + it.text("slug") }) { p -> ProjectLine(model, p, p.text("digest")) }
            }
        }
    }
}

/** A question an agent waits on you for: who asks, what, and the three ways to settle it without leaving Today. */
@Composable
private fun QuestionCard(model: LedgerModel, entry: JSONObject) {
    val agent = writerName(entry.text("source"))
    val muted = MaterialTheme.colorScheme.onSurfaceVariant
    val open = { openEntry(model, entry) }
    val ask = entry.optJSONObject("meta")?.text("ask").orEmpty()
    Group(Modifier.padding(start = Gutter, end = Gutter, bottom = 10.dp).clip(MaterialTheme.shapes.medium).clickable(enabled = !model.busy, onClick = open)
        .testTag("entry-${entry.text("id")}")) {
        Column(Modifier.padding(start = Gutter, end = 10.dp, top = 14.dp, bottom = 10.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                AgentMark(entry.text("source"))
                Text(listOf(agent, entry.text("project_name"), ago(entry.text("created_at"))).filter { it.isNotBlank() }.joinToString(" · "),
                    style = MaterialTheme.typography.bodySmall, color = muted, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
            Text(ask, style = MaterialTheme.typography.bodyLarge, fontWeight = FontWeight.Medium)
            // The AI's title adds context when it says something the question does not.
            val title = entryTitle(entry)
            if (title.isNotBlank() && !sameText(title, ask)) Text(title, style = MaterialTheme.typography.bodyMedium, color = muted, maxLines = 2, overflow = TextOverflow.Ellipsis)
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                // Looks like the box it opens: the entry page, with the answer field ready.
                Surface(onClick = open, enabled = !model.busy, modifier = Modifier.weight(1f).heightIn(min = 44.dp), shape = MaterialTheme.shapes.medium,
                    color = MaterialTheme.colorScheme.surfaceVariant, border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant)) {
                    Row(Modifier.padding(horizontal = 12.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        Glyph("reply", null, tint = muted, size = 18.dp)
                        Text("Reply to $agent", style = MaterialTheme.typography.bodyMedium, color = muted, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    }
                }
                OutlinedIconButton(onClick = { ownerAction(model, entry, "Marked handled", "handled" to true) }, enabled = !model.busy, shape = MaterialTheme.shapes.medium,
                    border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant), modifier = Modifier.size(44.dp)) {
                    Glyph("check", "Mark handled", tint = toneColors(Tone.Good).second, size = 20.dp)
                }
                OutlinedIconButton(onClick = { ownerAction(model, entry, "Snoozed until tomorrow", "snooze_days" to 1) }, enabled = !model.busy, shape = MaterialTheme.shapes.medium,
                    border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant), modifier = Modifier.size(44.dp)) {
                    Glyph("snooze", "Snooze until tomorrow", tint = MaterialTheme.colorScheme.secondary, size = 20.dp)
                }
            }
        }
    }
}

/** Offers notifications once, until turned on or dismissed. */
@Composable
private fun NotificationPrompt(model: LedgerModel) {
    val context = LocalContext.current
    var hidden by remember { mutableStateOf(Notifier.enabled(context) || Notifier.promptDismissed(context)) }
    val toggle = rememberNotificationSwitch(model) { if (it) hidden = true }
    if (hidden) return
    Group(Modifier.padding(horizontal = Gutter, vertical = 6.dp)) {
        Column(Modifier.padding(start = Gutter, end = Gutter, top = 14.dp, bottom = 8.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Text("Get a notification when an agent asks you something?", style = MaterialTheme.typography.titleSmall)
            Text("Also when a todo becomes overdue. Your phone checks your own server about every 15 minutes.", style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant)
            Row(Modifier.padding(top = 4.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Button(onClick = { toggle(true) }, shape = ButtonShape) { Text("Turn on") }
                TextButton(onClick = { Notifier.dismissPrompt(context); hidden = true }) { Text("Not now") }
            }
        }
    }
}

@Composable
private fun ProjectLine(model: LedgerModel, p: JSONObject, text: String) {
    Column(Modifier.fillMaxWidth().clickable { model.go(projectRoute(p.text("slug"))) }.padding(horizontal = Gutter, vertical = 12.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
            Text(p.text("name"), style = MaterialTheme.typography.titleMedium)
            HealthTag(p.text("status_state"))
        }
        if (text.isNotBlank()) Text(text, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 3, overflow = TextOverflow.Ellipsis)
    }
    GroupDivider()
}

/** A project's health label. Only "blocked" is shown: a latest status of
 *  "done" means that update finished something, not that the project did. */
@Composable
fun HealthTag(state: String) {
    if (state == "blocked") Tag("Blocked", Tone.Danger)
}

/** Open todos across projects, grouped by project and most important first. */
@Composable
fun TodosScreen(model: LedgerModel) {
    val query = tableQuery("todos", status = "open")
    val pager = rememberPager(model, query)
    PagedEntries(model, pager, query, empty = "No open todos. Nice.") { entries ->
        val heads = foldRepeats(entries).sortedWith(compareBy({ it.entry.text("project_name") }, { it.entry.text("slug") }, { priorityRank(it.entry) }))
        runsBy(heads) { it.entry.text("slug") }.forEach { (slug, group) ->
            item(key = "h:$slug") { SectionLabel(group.first().entry.text("project_name")) }
            items(group, key = { it.entry.text("id") }) { f -> EntryItem(model, f.entry, "todos", f.repeats, showProject = false, check = true) { e, _ -> openEntry(model, e) } }
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
        Row(verticalAlignment = Alignment.CenterVertically) {
            FilterChips(listOf("unread" to "Unread", "starred" to "Starred", "all" to "All"), filter, Modifier.weight(1f)) { filter = it }
            if (filter == "unread" && pager.entries.isNotEmpty()) TextButton(onClick = {
                // Everything unread, not just this page; one Undo puts it all back.
                model.undoable("Marked everything read") { it.request("POST", "/reading/read-all?reading=unread") }
            }, enabled = !model.busy, modifier = Modifier.padding(end = 4.dp)) { Text("Mark all read") }
        }
        PagedEntries(model, pager, query, empty = if (filter == "unread") "Nothing left to read." else "Nothing here yet.") { entries ->
            items(foldRepeats(entries), key = { it.entry.text("id") }) { f -> EntryItem(model, f.entry, "reading", f.repeats) { e, _ -> openEntry(model, e) } }
        }
    }
}

fun priorityRank(entry: JSONObject) = when (entry.optJSONObject("meta")?.text("priority")) { "high" -> 0; "low" -> 2; else -> 1 }
