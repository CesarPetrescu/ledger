package com.cesarpetrescu.ledger

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import org.json.JSONObject
import java.time.LocalDate
import java.time.OffsetDateTime
import java.time.ZoneId
import java.time.format.DateTimeFormatter

private val tableViews = listOf("projects" to "Projects", "todos" to "Todos", "decisions" to "Decisions", "activity" to "Activity")
private val entryKinds = listOf("decision", "note", "todo", "status").map { it to label(it) }

/** Query for one Table view; the view fixes the kind (and todo state) the server filters by. */
fun tableQuery(view: String, project: String = "", source: String = "", tag: String = "", status: String = "", q: String = "", kind: String = ""): String {
    val fields = listOf(
        "project" to project, "source" to source, "tag" to tag, "q" to q.trim(),
        "kind" to when (view) { "todos" -> "todo"; "decisions" -> "decision"; else -> kind },
        "status" to if (view == "todos") status else "",
    ).filter { it.second.isNotBlank() }
    return fields.joinToString("&") { (k, v) -> "$k=${segment(v)}" }
}

/** The extracted title, or the entry's first line until extraction catches up. */
fun entryTitle(entry: JSONObject): String {
    entry.optJSONObject("meta")?.text("title")?.takeIf { it.isNotBlank() }?.let { return it }
    val line = entry.text("body").trim().lineSequence().firstOrNull().orEmpty()
    return if (line.length > 110) line.take(109) + "…" else line
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

private fun timeOf(iso: String) = runCatching {
    OffsetDateTime.parse(iso).atZoneSameInstant(ZoneId.systemDefault()).format(DateTimeFormatter.ofPattern("HH:mm"))
}.getOrDefault(iso)

/** Route that opens a Table view filtered to one project and search text. */
fun tableRoute(view: String, project: String, q: String) = "table-open/${segment(view)}/${segment(project)}/${segment(q)}"

@Composable
fun TableScreen(model: LedgerModel, initialView: String = "projects", initialProject: String = "", initialQuery: String = "") {
    var view by rememberSaveable { mutableStateOf(initialView.takeIf { v -> tableViews.any { it.first == v } } ?: "projects") }
    var project by rememberSaveable { mutableStateOf(initialProject) }
    Column {
        PrimaryTabRow(selectedTabIndex = tableViews.indexOfFirst { it.first == view }) {
            tableViews.forEach { (id, name) ->
                Tab(selected = view == id, onClick = { view = id }, text = { Text(name, maxLines = 1) })
            }
        }
        if (view == "projects") TableProjects(model) { slug, target -> project = slug; view = target }
        else key(view) { TableEntries(model, view, project, if (view == initialView) initialQuery else "") { project = it } }
    }
}

@Composable
private fun TableProjects(model: LedgerModel, open: (String, String) -> Unit) = Load(model, "table-projects", { it.request("GET", "/table/projects") }) { data ->
    val progress = data.optJSONObject("metadata")
    Page {
        if (progress != null && progress.optBoolean("active") && progress.optInt("ready") + progress.optInt("failed") < progress.optInt("total")) item {
            Text("AI summaries: ${progress.optInt("ready")} of ${progress.optInt("total")} entries processed.", style = MaterialTheme.typography.bodySmall)
        }
        if (data.rows("projects").isEmpty()) item { Empty("No projects yet. Create one from Projects.") }
        items(data.rows("projects")) { p ->
            OutlinedCard(Modifier.fillMaxWidth()) {
                Column(Modifier.padding(18.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Text("${p.text("name")} · ${label(p.text("tier"))}", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
                    val digest = p.text("digest")
                    val status = p.text("status_title").ifBlank { p.text("status_body") }
                    if (digest.isNotBlank()) SelectionContainer { Text(digest, style = MaterialTheme.typography.bodyMedium) }
                    if (status.isNotBlank()) Text("${if (digest.isNotBlank()) "Latest: " else ""}$status · ${displayTime(p.text("status_at"))} · ${p.text("status_source")}",
                        style = if (digest.isNotBlank()) MaterialTheme.typography.bodySmall else MaterialTheme.typography.bodyMedium, maxLines = 3, overflow = TextOverflow.Ellipsis)
                    if (digest.isBlank() && status.isBlank()) Text("No status yet", color = MaterialTheme.colorScheme.onSurfaceVariant)
                    if (p.text("needs_me").isNotBlank()) Text("Needs you: ${p.text("needs_me")}", color = MaterialTheme.colorScheme.tertiary, style = MaterialTheme.typography.bodySmall, maxLines = 3, overflow = TextOverflow.Ellipsis)
                    val agents = p.strings("week_agents")
                    Text(if (p.optInt("week_entries") > 0) "${p.optInt("week_entries")} entries this week · ${agents.joinToString()}" else "Quiet this week",
                        style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.primary)
                    if (p.text("deadline").isNotBlank()) Text("Deadline: ${p.text("deadline")}", style = MaterialTheme.typography.labelMedium)
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        if (p.optInt("open_todos") > 0) OutlinedButton(onClick = { open(p.text("slug"), "todos") }) { Text("${p.optInt("open_todos")} open todos") }
                        TextButton(onClick = { open(p.text("slug"), "activity") }) { Text("Activity") }
                    }
                }
            }
        }
    }
}

@Composable
private fun TableEntries(model: LedgerModel, view: String, project: String, initialQuery: String, setProject: (String) -> Unit) {
    var source by rememberSaveable { mutableStateOf("") }
    var tag by rememberSaveable { mutableStateOf("") }
    var status by rememberSaveable { mutableStateOf("open") }
    var kind by rememberSaveable { mutableStateOf("") }
    var q by rememberSaveable { mutableStateOf(initialQuery) }
    var filters by rememberSaveable { mutableStateOf(false) }
    var before by rememberSaveable { mutableStateOf("") }
    val query = tableQuery(view, project, source, tag, status, q, kind)
    LaunchedEffect(query) { before = "" }
    Load(model, "table-projects-list", { it.request("GET", "/projects") }) { projectData ->
        val projects = projectData.rows("projects")
        Column {
            Column(Modifier.padding(horizontal = 20.dp, vertical = 8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Field("Search titles and text", q, { q = it }, max = 1000)
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    TextButton(onClick = { filters = !filters }) { Text(if (filters) "Hide filters" else "Filters") }
                    TextButton(onClick = { model.go("table-add/${if (view == "todos") "todo" else "note"}/$project") }, enabled = !model.busy) { Text(if (view == "todos") "Add todo" else "Add") }
                }
                if (filters) {
                    Choice("Project", project, listOf("" to "Any project") + projects.map { p ->
                        p.text("slug") to if (projects.count { it.text("name") == p.text("name") } > 1) "${p.text("name")} (${p.text("slug")})" else p.text("name")
                    }, setProject)
                    if (view == "todos") Choice("State", status, listOf("open" to "Open", "done" to "Done", "" to "Open and done")) { status = it }
                    if (view == "activity") Choice("Kind", kind, listOf("" to "Any kind") + entryKinds) { kind = it }
                }
            }
            Load(model, "table:$view:$query:$before", { it.request("GET", "/entries?limit=100&$query${if (before.isBlank()) "" else "&before=${segment(before)}"}") }) { data ->
                val entries = data.rows("entries")
                // Fold while the response is still newest first, so each head is
                // the newest copy; only then order todo heads by project and priority.
                val folded = foldRepeats(entries).let { heads ->
                    if (view == "todos") heads.sortedWith(compareBy({ it.entry.text("project_name") }, { it.entry.text("slug") }, { priorityRank(it.entry) })) else heads
                }
                val groups = runsBy(folded) { if (view == "todos") it.entry.text("slug") else dayLabel(it.entry.text("created_at")) }
                Page {
                    if (filters) {
                        if (data.strings("sources").isNotEmpty()) item { Choice("Agent", source, listOf("" to "Any agent") + data.strings("sources").map { it to it }) { source = it } }
                        if (data.strings("tags").isNotEmpty() || tag.isNotBlank()) item { Choice("Tag", tag, listOf("" to "Any tag") + (listOf(tag).filter { it.isNotBlank() } + data.strings("tags")).distinct().map { it to it }) { tag = it } }
                    }
                    if (entries.isEmpty()) item { Empty(if (view == "todos" && status == "open") "No open todos. Nice." else "No entries match.") }
                    groups.forEach { (_, group) ->
                        val heading = if (view == "todos") group.first().entry.text("project_name") else dayLabel(group.first().entry.text("created_at"))
                        item(key = "h:$heading:${group.first().entry.text("id")}") { Text("$heading · ${group.size}", style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.Bold) }
                        if (view == "activity") {
                            runsBy(group) { "${it.entry.text("slug")}\u0000${it.entry.text("source")}" }.forEach { (_, run) ->
                                item(key = "r:${run.first().entry.text("id")}") { ActivityRun(model, run) { tag = it; filters = true } }
                            }
                        } else items(group, key = { it.entry.text("id") }) { item -> EntryCard(model, view, item.entry, item.repeats) { tag = it; filters = true } }
                    }
                    item {
                        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                            if (before.isNotBlank()) TextButton(onClick = { before = "" }) { Text("Newest") }
                            if (data.text("next_before").isNotBlank()) TextButton(onClick = { before = data.text("next_before") }) { Text("Older entries") }
                        }
                    }
                    item { DownloadButton(model, "/entries.csv${if (query.isBlank()) "" else "?$query"}", "ledger-entries.csv", "Export CSV", "text/csv") }
                }
            }
        }
    }
}

private fun priorityRank(entry: JSONObject) = when (entry.optJSONObject("meta")?.text("priority")) { "high" -> 0; "low" -> 2; else -> 1 }

@Composable
private fun ActivityRun(model: LedgerModel, run: List<Folded>, onTag: (String) -> Unit) {
    var expanded by rememberSaveable(run.first().entry.text("id")) { mutableStateOf(false) }
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        EntryCard(model, "activity", run.first().entry, run.first().repeats, onTag)
        if (run.size > 1 && !expanded) TextButton(onClick = { expanded = true }) {
            Text("+${run.size - 1} more from ${run.first().entry.text("source")} on ${run.first().entry.text("project_name")}")
        }
        if (expanded) run.drop(1).forEach { EntryCard(model, "activity", it.entry, it.repeats, onTag) }
    }
}

@Composable
private fun EntryCard(model: LedgerModel, view: String, entry: JSONObject, repeats: List<JSONObject>, onTag: (String) -> Unit) {
    val id = entry.text("id")
    var open by rememberSaveable(id) { mutableStateOf(false) }
    var showRepeats by rememberSaveable(id) { mutableStateOf(false) }
    val meta = entry.optJSONObject("meta")
    val resolved = entry.optJSONObject("resolved_by")
    OutlinedCard(onClick = { open = !open }, modifier = Modifier.fillMaxWidth().testTag("entry-$id")) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
            Text(entryTitle(entry), style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold,
                color = if (resolved != null) MaterialTheme.colorScheme.onSurfaceVariant else MaterialTheme.colorScheme.onSurface)
            val details = buildList {
                if (view == "activity") add(label(entry.text("kind")))
                meta?.text("priority")?.takeIf { entry.text("kind") == "todo" && it.isNotBlank() && it != "normal" }?.let { add(label(it)) }
                if (view != "todos") add(entry.text("project_name"))
                add(entry.text("source"))
                add(if (view == "todos") displayTime(entry.text("created_at")) else timeOf(entry.text("created_at")))
            }
            Text(details.joinToString(" · "), style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.primary)
            val tags = meta?.strings("tags").orEmpty()
            if (tags.isNotEmpty() || repeats.isNotEmpty()) FlowRow(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                tags.forEach { tag -> AssistChip(onClick = { onTag(tag) }, label = { Text(tag) }) }
                if (repeats.isNotEmpty()) AssistChip(onClick = { showRepeats = !showRepeats }, label = { Text("+${repeats.size} ${if (repeats.size == 1) "repeat" else "repeats"}") })
            }
            if (showRepeats) repeats.forEach { Text("${entryTitle(it)} · ${it.text("source")} · ${displayTime(it.text("created_at"))}", style = MaterialTheme.typography.bodySmall) }
            if (open) {
                SelectionContainer { Text(entry.text("body"), style = MaterialTheme.typography.bodyMedium) }
                meta?.strings("refs")?.takeIf { it.isNotEmpty() }?.let { refs -> SelectionContainer { Text(refs.joinToString("\n"), style = MaterialTheme.typography.bodySmall) } }
                Text(when { meta == null -> "Summary pending."; meta.text("origin") == "model" -> "Title and tags generated by AI from the text above."; else -> "Written from the console." },
                    style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                RelatedEntries(model, id)
            }
            if (entry.text("kind") == "todo") {
                if (resolved == null) Button(onClick = { model.act("Todo marked done") { it.request("POST", "/entries/${segment(id)}/resolve") } }, enabled = !model.busy) { Text("Mark done") }
                else Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    Text("Done${if (resolved.text("origin") == "model") " (detected)" else ""} · ${displayTime(resolved.text("created_at"))}",
                        style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.tertiary, modifier = Modifier.padding(top = 12.dp))
                    TextButton(onClick = { model.act("Todo reopened") { it.request("POST", "/entries/${segment(id)}/reopen") } }, enabled = !model.busy) { Text("Reopen") }
                }
            }
        }
    }
}

@Composable
private fun RelatedEntries(model: LedgerModel, id: String) {
    val client = model.api ?: return
    var related by remember(id) { mutableStateOf<List<JSONObject>?>(null) }
    LaunchedEffect(id, client, model.revision) {
        related = try {
            kotlinx.coroutines.withContext(kotlinx.coroutines.Dispatchers.IO) { client.request("GET", "/entries/${segment(id)}/related").rows("related") }
        } catch (e: Exception) {
            if (e is kotlinx.coroutines.CancellationException) throw e
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
            TextButton(onClick = { model.go(tableRoute("activity", r.text("slug"), entryTitle(r))) }, contentPadding = PaddingValues(0.dp)) {
                Text("${entryTitle(r)} · ${r.text("project_name")}", style = MaterialTheme.typography.bodySmall, maxLines = 2, overflow = TextOverflow.Ellipsis)
            }
        }
    }
}

/** Adds an entry from the Table, choosing the project when no filter selected one. */
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
