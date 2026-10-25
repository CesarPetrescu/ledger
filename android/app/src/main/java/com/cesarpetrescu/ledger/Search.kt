package com.cesarpetrescu.ledger

import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import org.json.JSONObject

private val searchKinds = listOf("" to "All kinds", "decision" to "Decisions", "todo" to "Todos", "note" to "Notes", "status" to "Status updates")

/** A project's name, with its slug when another project shares the name. */
private fun projectName(p: JSONObject, all: List<JSONObject>) =
    if (all.count { it.text("name") == p.text("name") } > 1) "${p.text("name")} (${p.text("slug")})" else p.text("name")

/**
 * One place to find anything. Words alone search everything, projects included, by meaning and by text. With no words,
 * or with an agent or tag chosen, it lists every project's entries newest first, filtered like the web console's Table.
 */
@Composable
fun SearchScreen(model: LedgerModel, initialSource: String = "") = Load(model, "search-projects", { it.request("GET", "/projects") }) { data ->
    val projects = data.rows("projects")
    var typed by rememberSaveable { mutableStateOf("") }
    var q by rememberSaveable { mutableStateOf("") }
    var project by rememberSaveable { mutableStateOf("") }
    var kind by rememberSaveable { mutableStateOf("") }
    var source by rememberSaveable { mutableStateOf(initialSource) }
    var tag by rememberSaveable { mutableStateOf("") }
    var todoState by rememberSaveable { mutableStateOf("open") }
    var showRoutine by rememberSaveable { mutableStateOf(false) }
    // The agents and tags to choose from, as the last entry list reported them.
    var sources by remember { mutableStateOf(listOf<String>()) }
    var tags by remember { mutableStateOf(listOf<String>()) }
    val hits = q.isNotBlank() && source.isBlank() && tag.isBlank()
    Column {
        OutlinedTextField(typed, { if (validFieldText(it, 1000, false)) typed = it }, Modifier.fillMaxWidth().padding(start = Gutter, end = Gutter, top = 4.dp),
            label = { Text("Search your work") }, singleLine = true, shape = MaterialTheme.shapes.medium,
            keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search), keyboardActions = KeyboardActions(onSearch = { q = typed.trim() }),
            trailingIcon = {
                Row {
                    if (typed.isNotEmpty()) IconButton(onClick = { typed = ""; q = "" }) { Glyph("close", "Clear search") }
                    IconButton(onClick = { q = typed.trim() }, modifier = Modifier.testTag("search-submit")) { Glyph("search", "Search") }
                }
            })
        Row(Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()).padding(horizontal = Gutter, vertical = 8.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            MenuChip("Project", project, listOf("" to "All projects") + projects.map { p -> p.text("slug") to projectName(p, projects) }) { project = it }
            MenuChip("Kind", kind, searchKinds) { kind = it }
            MenuChip("Agent", source, listOf("" to "All agents") + (sources + source).filter { it.isNotBlank() }.distinct().map { it to writerName(it) }) { source = it }
            MenuChip("Tag", tag, listOf("" to "All tags") + (tags + tag).filter { it.isNotBlank() }.distinct().map { it to it }) { tag = it }
            if (!hits && kind == "todo") listOf("open" to "Open", "done" to "Done", "" to "All").forEach { (id, name) ->
                FilterChip(selected = todoState == id, onClick = { todoState = id }, label = { Text(name) }, shape = MaterialTheme.shapes.small)
            } else if (!hits) FilterChip(selected = showRoutine, onClick = { showRoutine = !showRoutine }, label = { Text("Show routine") }, shape = MaterialTheme.shapes.small)
        }
        HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
        if (hits) SearchHits(model, json("q" to q, "limit" to 20, "project" to project, "kind" to kind).toString())
        else {
            val view = when (kind) { "todo" -> "todos"; "decision" -> "decisions"; else -> "activity" }
            // Words search everywhere, routine entries included.
            val query = tableQuery(view, project = project, source = source, tag = tag, status = todoState, q = q,
                kind = if (view == "activity") kind else "", hideRoutine = !showRoutine && q.isBlank())
            val pager = rememberPager(model, query)
            LaunchedEffect(pager.data) { pager.data?.let { sources = it.strings("sources"); tags = it.strings("tags") } }
            PagedEntries(model, pager, query, empty = if (view == "todos" && todoState == "open") "No open todos. Nice." else "No entries match.") { entries ->
                val folded = foldRepeats(entries).let { heads -> if (view == "todos") heads.sortedWith(compareBy({ it.entry.text("project_name") }, { it.entry.text("slug") }, { priorityRank(it.entry) })) else heads }
                runsBy(folded) { if (view == "todos") it.entry.text("slug") else dayLabel(it.entry.text("created_at")) }.forEach { (key, group) ->
                    item(key = "h:$key:${group.first().entry.text("id")}") {
                        SectionLabel(if (view == "todos") projects.firstOrNull { it.text("slug") == key }?.let { projectName(it, projects) } ?: group.first().entry.text("project_name") else key)
                    }
                    items(group, key = { it.entry.text("id") }) { f ->
                        EntryItem(model, f.entry, view, f.repeats, showProject = view != "todos" && project.isBlank(), check = view == "todos") { e, _ -> openEntry(model, e) }
                    }
                }
            }
        }
    }
}

/** Search results, best match first: entries open themselves, projects open the project. */
@Composable
private fun SearchHits(model: LedgerModel, request: String) = Load(model, "search:$request", { it.request("POST", "/search", JSONObject(request)) }) { data ->
    val rows = data.rows("hits")
    LazyColumn(Modifier.fillMaxSize().testTag("page"), contentPadding = PaddingValues(bottom = 24.dp)) {
        if (data.strings("degraded").isNotEmpty()) item {
            Text("Some search sources are unavailable: ${data.strings("degraded").joinToString()}. Showing what the rest found.",
                Modifier.padding(horizontal = Gutter, vertical = 8.dp), style = MaterialTheme.typography.bodySmall, color = toneColors(Tone.Warn).second)
        }
        if (rows.isEmpty()) item { Box(Modifier.padding(horizontal = Gutter)) { Empty("No results. Try different words or fewer filters.") } }
        else item { Text(plural(rows.size, "result"), Modifier.padding(start = Gutter, end = Gutter, top = 12.dp, bottom = 4.dp), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
        items(rows) { hit -> SearchHit(model, hit) }
    }
}

@Composable
private fun SearchHit(model: LedgerModel, hit: JSONObject) {
    val entryId = hit.text("entry_id")
    val slug = hit.text("project_slug")
    val isProject = entryId.isBlank()
    val muted = MaterialTheme.colorScheme.onSurfaceVariant
    Row(Modifier.fillMaxWidth().clickable(enabled = entryId.isNotBlank() || slug.isNotBlank()) {
        if (!isProject) model.go("entry-view/${segment(entryId)}") else model.go(projectRoute(slug))
    }.padding(horizontal = Gutter, vertical = 12.dp), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
        Box(Modifier.padding(top = 1.dp).size(20.dp), contentAlignment = Alignment.Center) {
            if (isProject) Glyph("projects", null, tint = muted, size = 16.dp) else KindMark(JSONObject().put("kind", hit.text("kind")))
        }
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(3.dp)) {
            // A project reads as its name over its matching text; an entry as its matching text over its project.
            Text(if (isProject) hit.text("project_name").ifBlank { hit.text("ref") } else hit.text("snippet"), style = MaterialTheme.typography.titleSmall,
                maxLines = 3, overflow = TextOverflow.Ellipsis)
            if (isProject && hit.text("snippet").isNotBlank()) Text(hit.text("snippet"), style = MaterialTheme.typography.bodyMedium, color = muted, maxLines = 2, overflow = TextOverflow.Ellipsis)
            Text(if (isProject) "Project" else listOf(hit.text("project_name"), label(hit.text("kind")).lowercase()).filter { it.isNotBlank() }.joinToString(" · "),
                style = MaterialTheme.typography.bodySmall, color = muted)
        }
    }
    GroupDivider()
}
