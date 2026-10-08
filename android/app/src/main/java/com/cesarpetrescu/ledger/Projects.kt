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
        item { Text("Entries can't be edited. Add a correction as a new entry.", style = MaterialTheme.typography.bodySmall) }
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
                    // An entry hit opens the entry itself; a project hit opens the project.
                    val entryId = hit.text("entry_id")
                    SummaryCard(hit.text("project_name").ifBlank { hit.text("ref") }, label(hit.text("kind")), hit.text("snippet"),
                        when {
                            entryId.isNotBlank() -> ({ model.go("entry-view/${segment(entryId)}") })
                            hit.text("project_slug").isNotBlank() -> ({ model.go(projectRoute(hit.text("project_slug"))) })
                            else -> null
                        })
                }
            }
        }
    }
}

/** The Git repositories a project spans. Agents see them with the project and clone with their own access. */
@Composable
fun ProjectRepos(model: LedgerModel, slug: String) {
    var url by rememberSaveable { mutableStateOf("") }
    var role by rememberSaveable { mutableStateOf("") }
    var branch by rememberSaveable { mutableStateOf("") }
    Load(model, "repos:$slug", { it.request("GET", "/projects/${segment(slug)}/repos") }) { data ->
        Page {
            item { Text("Where this project's code lives; link every repository it spans. Agents see them with the project and clone with their own Git access.",
                style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant) }
            if (data.rows("repos").isEmpty()) item { Empty("No repositories linked yet.") }
            items(data.rows("repos"), key = { it.text("id") }) { repo ->
                val (subtitle, body) = repoSummary(repo)
                Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    SummaryCard(repo.text("repo"), subtitle, body)
                    ConfirmButton("Unlink", "Unlink ${repo.text("repo")}? Agents stop seeing it with this project. The repository itself is not touched.", !model.busy) {
                        model.act("Repository unlinked") { it.request("DELETE", "/repos/${segment(repo.text("id"))}") }
                    }
                }
            }
            item { Text("Link a repository", style = MaterialTheme.typography.titleMedium) }
            item { Field("Repository URL", url, { url = it }, max = 500, placeholder = "https://github.com/owner/repo", keyboard = KeyboardType.Uri) }
            item { Field("Role", role, { role = it }, max = 60, placeholder = "backend, android, docs…") }
            item { Field("Branch", branch, { branch = it }, max = 200, placeholder = "Default branch") }
            item {
                Button(enabled = !model.busy && url.isNotBlank(), onClick = {
                    model.act("Repository linked", after = { url = ""; role = ""; branch = "" }) {
                        it.request("POST", "/projects/${segment(slug)}/repos", json("url" to url.trim(), "role" to role.trim(), "branch" to branch.trim(), "path" to "", "note" to ""))
                    }
                }) { Text("Link repository") }
            }
        }
    }
}
