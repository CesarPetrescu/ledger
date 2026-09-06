package com.cesarpetrescu.ledger

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import org.json.JSONObject

private val tiers = listOf("focus", "maintain", "park").map { it to label(it) }
private val kinds = listOf("decision", "note", "todo", "status").map { it to label(it) }

@Composable
fun Overview(model: LedgerModel) = Load(model, "overview", { it.request("GET", "/overview") }) { data ->
    Page {
        item { Text("A clear view of your work.", style = MaterialTheme.typography.headlineMedium) }
        item {
            val counts = data.getJSONObject("counts")
            SummaryCard("${counts.optInt("projects")} projects · ${counts.optInt("entries")} entries", "${counts.optInt("oauth_clients")} connected clients")
        }
        item { Text("Focus", style = MaterialTheme.typography.titleLarge) }
        val focus = data.rows("projects").filter { it.text("tier") == "focus" }
        if (focus.isEmpty()) item { Empty("No focus projects yet. Create one from Projects.") }
        items(focus) { p -> ProjectCard(p) { model.go("project/${p.text("slug")}") } }
        item { Text("Recent entries", style = MaterialTheme.typography.titleLarge) }
        if (data.rows("recent_entries").isEmpty()) item { Empty("Your latest notes and decisions will appear here.") }
        items(data.rows("recent_entries")) { e -> SummaryCard(e.text("project_name"), "${label(e.text("kind"))} · ${displayTime(e.text("created_at"))}", e.text("body")) { model.go("project/${e.text("slug")}") } }
    }
}

@Composable
fun ProjectCard(p: JSONObject, open: () -> Unit) = SummaryCard(p.text("name"), "${label(p.text("tier"))} · ${p.optInt("hours_wk")} h/week", p.text("goal").ifBlank { p.text("description") }, open)

@Composable
fun Projects(model: LedgerModel) {
    var tier by rememberSaveable { mutableStateOf("") }
    Column {
        Row(Modifier.padding(horizontal = 20.dp), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            Box(Modifier.weight(1f)) { Choice("Tier", tier, listOf("" to "All projects") + tiers) { tier = it } }
            Button(onClick = { model.go("project-edit/") }, enabled = !model.busy, modifier = Modifier.padding(top = 12.dp)) { Text("New") }
        }
        Load(model, "projects:$tier", { it.request("GET", "/projects${if (tier.isBlank()) "" else "?tier=$tier"}") }) { data ->
            Page {
                if (data.rows("projects").isEmpty()) item { Empty("No projects in this tier.") }
                items(data.rows("projects")) { p -> ProjectCard(p) { model.go("project/${p.text("slug")}") } }
            }
        }
    }
}

@Composable
fun ProjectDetail(model: LedgerModel, slug: String) {
    var before by rememberSaveable { mutableStateOf("") }
    Load(model, "project:$slug:$before", { it.request("GET", "/projects/${segment(slug)}?entries=50${if (before.isBlank()) "" else "&before=${segment(before)}"}") }) { data ->
        val p = data.getJSONObject("project")
        Page {
            item { ProjectCard(p) { model.go("project-edit/$slug") } }
            item { Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Button(onClick = { model.go("entry/$slug") }, enabled = !model.busy) { Text("Add entry") }
                OutlinedButton(onClick = { model.go("project-edit/$slug") }, enabled = !model.busy) { Text("Edit") }
                TextButton(onClick = { model.go("project-files/$slug") }) { Text("Files") }
            } }
            item {
                Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                    listOf("description", "type", "deadline", "needs_me", "automate", "stack").forEach { key ->
                        if (p.text(key).isNotBlank()) Text("${label(key)}: ${p.text(key)}", style = MaterialTheme.typography.bodyMedium)
                    }
                }
            }
            item { Text("Entries", style = MaterialTheme.typography.titleLarge) }
            if (data.rows("entries").isEmpty()) item { Empty("Add a note, decision, task, or status update.") }
            items(data.rows("entries")) { e -> SummaryCard(label(e.text("kind")), "${displayTime(e.text("created_at"))} · ${e.text("source")}", e.text("body")) }
            item { Row {
                if (before.isNotBlank()) TextButton(onClick = { before = "" }) { Text("Latest entries") }
                if (data.text("next_before").isNotBlank()) TextButton(onClick = { before = data.text("next_before") }) { Text("Older entries") }
            } }
        }
    }
}

@Composable
fun ProjectEditor(model: LedgerModel, slug: String) {
    if (slug.isBlank()) ProjectForm(model, "", JSONObject())
    else Load(model, "edit-project:$slug", { it.request("GET", "/projects/${segment(slug)}?entries=1") }) { ProjectForm(model, slug, it.getJSONObject("project")) }
}

@Composable
private fun ProjectForm(model: LedgerModel, existingSlug: String, p: JSONObject) {
    var slug by rememberSaveable { mutableStateOf(existingSlug) }
    var name by rememberSaveable { mutableStateOf(p.text("name")) }
    var tier by rememberSaveable { mutableStateOf(p.text("tier").ifBlank { "focus" }) }
    var hours by rememberSaveable { mutableStateOf(p.optInt("hours_wk").toString()) }
    var description by rememberSaveable { mutableStateOf(p.text("description")) }
    var goal by rememberSaveable { mutableStateOf(p.text("goal")) }
    var type by rememberSaveable { mutableStateOf(p.text("type")) }
    var deadline by rememberSaveable { mutableStateOf(p.text("deadline")) }
    var needs by rememberSaveable { mutableStateOf(p.text("needs_me")) }
    var automate by rememberSaveable { mutableStateOf(p.text("automate")) }
    var stack by rememberSaveable { mutableStateOf(p.text("stack")) }
    Page {
        item { Text(if (existingSlug.isBlank()) "New project" else "Edit project", style = MaterialTheme.typography.headlineSmall) }
        item { Field("Project slug", slug, { slug = it }, max = 64, enabled = existingSlug.isBlank(), placeholder = "atlas") }
        item { Field("Name", name, { name = it }, max = 200) }
        item { Choice("Tier", tier, tiers) { tier = it } }
        item { Field("Hours per week", hours, { hours = it }, max = 3, keyboard = KeyboardType.Number) }
        item { Field("Goal", goal, { goal = it }, multiline = true) }
        item { Field("Description", description, { description = it }, multiline = true) }
        item { Field("Type", type, { type = it }) }
        item { Field("Deadline", deadline, { deadline = it }, max = 200) }
        item { Field("Needs me", needs, { needs = it }, multiline = true) }
        item { Field("Automate", automate, { automate = it }, multiline = true) }
        item { Field("Stack", stack, { stack = it }) }
        item { Button(enabled = !model.busy && validProjectSlug(slug) && name.isNotBlank() && hours.toIntOrNull() in 0..168,
            onClick = {
                model.act(after = model::back) { api ->
                    if (existingSlug.isBlank()) {
                        try { api.request("GET", "/projects/${segment(slug)}?entries=1"); throw IllegalArgumentException("That project already exists. Open it to edit.") }
                        catch (e: ApiError) { if (e.status != 404) throw e }
                    }
                    api.request("PUT", "/projects/${segment(slug)}", json("name" to name.trim(), "tier" to tier, "hours_wk" to hours.toInt(),
                        "description" to description, "goal" to goal, "type" to type, "deadline" to deadline, "needs_me" to needs, "automate" to automate, "stack" to stack))
                }
            }, modifier = Modifier.fillMaxWidth()) { Text("Save project") } }
    }
}

@Composable
fun EntryEditor(model: LedgerModel, slug: String) {
    var kind by rememberSaveable { mutableStateOf("note") }
    var body by rememberSaveable { mutableStateOf("") }
    Page {
        item { Text("Add to $slug", style = MaterialTheme.typography.headlineSmall) }
        item { Choice("Entry kind", kind, kinds) { kind = it } }
        item { Field("Entry", body, { body = it }, multiline = true, max = 4000) }
        item { Text("Entries are permanent. Add a correction as a new entry.", style = MaterialTheme.typography.bodySmall) }
        item { Button(onClick = { model.act("Entry added", after = model::back) { it.request("POST", "/projects/${segment(slug)}/entries", json("kind" to kind, "body" to body.trim())) } },
            enabled = !model.busy && body.isNotBlank(), modifier = Modifier.fillMaxWidth()) { Text("Add entry") } }
    }
}

@Composable
fun SearchScreen(model: LedgerModel) {
    var query by rememberSaveable { mutableStateOf("") }
    var submitted by rememberSaveable { mutableStateOf("") }
    var project by rememberSaveable { mutableStateOf("") }
    var kind by rememberSaveable { mutableStateOf("") }
    var filters by rememberSaveable { mutableStateOf(false) }
    Column {
        Column(Modifier.padding(horizontal = 20.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Field("Search your work", query, { query = it }, max = 1000)
            if (filters) {
                Field("Project slug (optional)", project, { project = it }, max = 64)
                Choice("Entry kind", kind, listOf("" to "All kinds") + kinds) { kind = it }
            }
            Row {
                Button(modifier = Modifier.testTag("search-submit"), onClick = { submitted = json("q" to query.trim(), "limit" to 20, "project" to project, "kind" to kind).toString(); model.refresh() }, enabled = query.isNotBlank()) { Text("Search") }
                TextButton(onClick = { filters = !filters }) { Text("Filters") }
            }
        }
        if (submitted.isBlank()) Box(Modifier.padding(20.dp)) { Empty("Find projects, decisions, and notes.") }
        else Load(model, "search:$submitted", { it.request("POST", "/search", JSONObject(submitted)) }) { data ->
            Page {
                if (data.strings("degraded").isNotEmpty()) item { Text("Some search sources are unavailable: ${data.strings("degraded").joinToString()}", color = MaterialTheme.colorScheme.error) }
                if (data.rows("hits").isEmpty()) item { Empty("No results. Try different words or fewer filters.") }
                items(data.rows("hits")) { hit ->
                    SummaryCard(hit.text("project_name").ifBlank { hit.text("ref") }, label(hit.text("kind")), hit.text("snippet"),
                        if (hit.text("project_slug").isNotBlank()) ({ model.go("project/${hit.text("project_slug")}") }) else null)
                }
            }
        }
    }
}
