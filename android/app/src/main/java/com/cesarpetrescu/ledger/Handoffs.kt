@file:OptIn(androidx.compose.material3.ExperimentalMaterial3Api::class)
package com.cesarpetrescu.ledger

import android.provider.OpenableColumns
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import org.json.JSONObject

@Composable
fun Handoffs(model: LedgerModel) {
    var query by rememberSaveable { mutableStateOf("") }
    var archive by rememberSaveable { mutableStateOf("active") }
    var status by rememberSaveable { mutableStateOf("") }
    var target by rememberSaveable { mutableStateOf("") }
    var project by rememberSaveable { mutableStateOf("") }
    var filters by rememberSaveable { mutableStateOf(false) }
    var filter by rememberSaveable { mutableStateOf("archive=active") }
    Column {
        Column(Modifier.padding(horizontal = 20.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Button(onClick = { model.go("handoff-new") }, enabled = !model.busy) { Text("New handoff") }
                TextButton(onClick = { filters = !filters }) { Text("Filter") }
            }
            if (filters) ModalBottomSheet(onDismissRequest = { filters = false }) {
                Column(Modifier.padding(20.dp).imePadding().verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Field("Find handoffs", query, { query = it }, max = 1000)
                Choice("Archive", archive, listOf("active" to "Active", "archived" to "Archived", "all" to "All")) { archive = it }
                Choice("Status", status, listOf("" to "All statuses") + listOf("draft", "ready", "in_progress", "blocked", "done").map { it to label(it) }) { status = it }
                Field("Project slug (optional)", project, { project = it }, max = 64)
                Field("Target (optional)", target, { target = it }, max = 100)
                Button(onClick = {
                    filter = "archive=$archive&q=${segment(query)}&status=$status&project=${segment(project)}&target=${segment(target)}"
                    filters = false
                }) { Text("Apply filters") }
                }
            }
        }
        HandoffList(model, filter)
    }
}

/** A page of handoffs matching [filter], each opening its thread; research ones carry their state. A new filter starts from the latest. */
@Composable
fun HandoffList(model: LedgerModel, filter: String, empty: String = "No handoffs match this view.", showProject: Boolean = true) {
    var before by rememberSaveable(filter) { mutableStateOf("") }
    Load(model, "handoffs:$filter:$before", { it.request("GET", "/handoffs?$filter&before=${segment(before)}") }) { data ->
        Page {
            if (data.rows("handoffs").isEmpty()) item { Empty(empty) }
            items(data.rows("handoffs")) { h ->
                val updated = displayTime(h.text("updated_at"))
                SummaryCard(h.text("title"), if (showProject) h.text("project_name").ifBlank { "General" } + " · " + updated else updated,
                    listOf(handoffProgress(h), h.text("description")).filter { it.isNotBlank() }.joinToString("\n"), researchTags(h)) { model.go("handoff/${h.text("id")}") }
            }
            item { Row {
                if (before.isNotBlank()) TextButton(onClick = { before = "" }) { Text("Latest") }
                if (data.text("next_before").isNotBlank()) TextButton(onClick = { before = data.text("next_before") }) { Text("Older handoffs") }
            } }
        }
    }
}

@Composable
fun HandoffDetail(model: LedgerModel, id: String) {
    var before by rememberSaveable { mutableStateOf("") }
    val markdown = rememberMarkdownPreview()
    Load(model, "handoff:$id:$before", { it.request("GET", handoffPath(id, before)) }) { data ->
        val h = data.getJSONObject("handoff")
        val research = data.optJSONObject("research")
        Page {
            item { MarkdownCard(h.text("title"), h.text("project_name").ifBlank { "General" }, listOf(h.text("description"), h.text("scope")).filter { it.isNotBlank() }.joinToString("\n\n"), markdown.value) }
            if (research != null) item {
                val (state, phase) = research.text("state") to research.text("phase")
                // The state is the same coloured label as on the Handoffs list.
                SummaryCard("Research task", body = researchHint(state, phase, research.optInt("attempt"), research.optInt("failures"), research.optInt("max_attempts"), research.text("progress"), research.text("last_error")),
                    tags = listOfNotNull(researchStatusTag(researchStatus(state, phase))))
                // The task's buttons live here: the brief is the oldest message and a long thread pages it out.
                Row(Modifier.padding(top = 12.dp).horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    researchActions(state, phase, true).forEach { (action, name) ->
                        val run: () -> Unit = { model.act("$name applied") { it.request("POST", "/handoff-messages/${segment(research.text("message_id"))}/actions", json("action" to action)) } }
                        // Accept and Queue lead; Send back and the other moves are secondary.
                        if (isResearchPrimary(action)) Button(onClick = run, enabled = !model.busy) { Text(name) }
                        else OutlinedButton(onClick = run, enabled = !model.busy) { Text(name) }
                    }
                }
            }
            item {
                val export = rememberDownload(model, "/handoffs/${segment(id)}/export", "handoff-$id.md", "text/markdown")
                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    // A research thread takes the owner's feedback or answer; the run reads it on Send back or Resume.
                    val write = { model.go(if (research != null) "message-new/$id/reply" else "message-new/$id") }
                    val label = if (research != null) "Reply" else "Add message"
                    // Under Accept or Queue, Reply steps back so one button leads; answering a question, Reply is the next step.
                    if (research != null && researchLeads(research.text("state"), research.text("phase"))) OutlinedButton(onClick = write, enabled = !model.busy) { Text(label) }
                    else Button(onClick = write, enabled = !model.busy) { Text(label) }
                    Overflow("More actions for this handoff", listOf(MenuAction("Edit details") { model.go("handoff-edit/$id") }, MenuAction("Export", run = export)), enabled = !model.busy)
                }
            }
            item { MarkdownSwitch(markdown) }
            items(data.rows("messages"), key = { it.text("id") }) { message -> MessageCard(model, message, markdown.value, research) }
            item { Row {
                if (before.isNotBlank()) TextButton(onClick = { before = "" }) { Text("Latest messages") }
                if (data.text("next_before").isNotBlank()) TextButton(onClick = { before = data.text("next_before") }) { Text("Older messages") }
            } }
        }
    }
}

@Composable
private fun MessageCard(model: LedgerModel, message: JSONObject, markdown: Boolean, research: JSONObject?) {
    val id = message.text("id")
    var retarget by remember { mutableStateOf(false) }
    var target by rememberSaveable { mutableStateOf(message.text("target")) }
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        val to = if (message.text("target").isNotBlank()) " → ${message.text("target")}" else ""
        // A research thread is notes around one brief: who wrote each, not its delivery and work states.
        if (research != null) MarkdownCard(researchNoteTitle(message), displayTime(message.text("created_at")) + to, message.text("body"), markdown)
        else MarkdownCard("${label(message.text("work_state"))} · ${label(message.text("delivery_state"))}",
            "${displayTime(message.text("created_at"))} · ${message.text("source")}$to", message.text("body"), markdown)
        Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            val actions = if (research != null) researchActions(message.text("work_state"), research.text("phase"), brief = false)
                else messageActions(message.text("work_state"), message.text("delivery_state")).map { it to label(it) }
            actions.forEach { (action, name) ->
                OutlinedButton(enabled = !model.busy, onClick = { model.act("$name applied") { it.request("POST", "/handoff-messages/${segment(id)}/actions", json("action" to action)) } }) { Text(name) }
            }
            if (research == null && canRetarget(message.text("work_state"), message.text("claimed_at").isNotBlank())) TextButton(onClick = { retarget = true }, enabled = !model.busy) { Text("Retarget") }
        }
        message.rows("files").forEach { file -> FileRow(model, file, message.text("work_state") == "draft") }
        if (message.text("work_state") == "draft") UploadButton(model, id)
        HorizontalDivider()
    }
    if (retarget) AlertDialog(onDismissRequest = { retarget = false }, title = { Text("Retarget message") },
        text = { Field("Target", target, { target = it }, max = 100) },
        confirmButton = { TextButton(enabled = !model.busy, onClick = {
            model.act(after = { retarget = false }) { it.request("POST", "/handoff-messages/${segment(id)}/actions", json("action" to "retarget", "target" to target.trim())) }
        }) { Text("Save") } }, dismissButton = { TextButton(onClick = { retarget = false }) { Text("Cancel") } })
}

@Composable
fun HandoffEditor(model: LedgerModel, id: String = "") {
    if (id.isEmpty()) HandoffForm(model, "", JSONObject())
    else Load(model, "handoff-edit:$id", { it.request("GET", "/handoffs/${segment(id)}?messages=1") }) { HandoffForm(model, id, it.getJSONObject("handoff")) }
}

@Composable
private fun HandoffForm(model: LedgerModel, id: String, h: JSONObject) {
    val markdown = rememberMarkdownPreview()
    var title by rememberSaveable { mutableStateOf(h.text("title")) }
    var project by rememberSaveable { mutableStateOf(h.text("project_slug")) }
    var description by rememberSaveable { mutableStateOf(h.text("description")) }
    var scope by rememberSaveable { mutableStateOf(h.text("scope")) }
    var body by rememberSaveable { mutableStateOf("") }
    var target by rememberSaveable { mutableStateOf("") }
    var draft by rememberSaveable { mutableStateOf(true) }
    Page {
        item { Text(if (id.isBlank()) "New handoff" else "Edit handoff", style = MaterialTheme.typography.headlineSmall) }
        item { Field("Title", title, { title = it }, max = 200) }
        item { Field("Project slug (optional)", project, { project = it }, max = 64) }
        item { Field("Description", description, { description = it }, multiline = true, max = 2000) }
        item { Field("Scope", scope, { scope = it }, max = 500) }
        if (id.isBlank()) {
            item { Field("First message", body, { body = it }, multiline = true, max = 100000) }
            item { MarkdownSwitch(markdown) }
            if (markdown.value) item { MarkdownPreviewBox(body) }
            item { Field("Target (optional)", target, { target = it }, max = 100) }
            item { DraftSwitch(draft) { draft = it } }
        }
        item { Button(enabled = !model.busy && title.isNotBlank() && (id.isNotBlank() || body.isNotBlank()), modifier = Modifier.fillMaxWidth(), onClick = {
            model.act(after = model::back) { api ->
                val payload = json("title" to title.trim(), "project_slug" to project.trim(), "description" to description, "scope" to scope)
                if (id.isBlank()) api.request("POST", "/handoffs", payload.put("body", body.trim()).put("target", target.trim()).put("draft", draft))
                else api.request("PUT", "/handoffs/${segment(id)}", payload)
            }
        }) { Text(if (id.isBlank()) "Create handoff" else "Save details") } }
    }
}

@Composable
fun DraftSwitch(draft: Boolean, change: (Boolean) -> Unit) {
    Row(horizontalArrangement = Arrangement.spacedBy(12.dp), verticalAlignment = androidx.compose.ui.Alignment.CenterVertically) {
        Switch(checked = draft, onCheckedChange = change, enabled = LocalEditingEnabled.current)
        // A fixed label with checked = draft; "Publish immediately" switched off read as publishing turned off.
        Text("Save as draft · attach files before publishing")
    }
}

/** A new message on a handoff; in a research thread, the owner's [reply] to the run, which has no target. */
@Composable
fun MessageEditor(model: LedgerModel, id: String, reply: Boolean = false) {
    val markdown = rememberMarkdownPreview()
    var body by rememberSaveable { mutableStateOf("") }
    var target by rememberSaveable { mutableStateOf("") }
    // A research run never reads drafts, so a reply publishes unless the owner keeps it back to attach files.
    var draft by rememberSaveable { mutableStateOf(!reply) }
    Page {
        item { Text(if (reply) "Reply" else "Add a message", style = MaterialTheme.typography.headlineSmall) }
        item { Field(if (reply) "Feedback or answer" else "Message", body, { body = it }, multiline = true, max = 100000) }
        item { MarkdownSwitch(markdown) }
        if (markdown.value) item { MarkdownPreviewBox(body) }
        if (!reply) item { Field("Target (optional)", target, { target = it }, max = 100) }
        item { DraftSwitch(draft) { draft = it } }
        item { Text("Messages are permanent. Add a correction as a new message.", style = MaterialTheme.typography.bodySmall) }
        item { Button(enabled = !model.busy && body.isNotBlank(), modifier = Modifier.fillMaxWidth(), onClick = {
            model.act(if (reply) "Reply added" else "Message added", after = model::back) {
                it.request("POST", "/handoffs/${segment(id)}/messages", json("body" to body.trim(), "target" to if (reply) "" else target.trim(), "draft" to draft))
            }
        }) { Text(if (reply) "Add reply" else "Add message") } }
    }
}

/** Saves a server file to a document the owner picks; the returned action asks where. */
@Composable
fun rememberDownload(model: LedgerModel, path: String, filename: String, mime: String = "application/octet-stream"): () -> Unit {
    val resolver = LocalContext.current.contentResolver
    val launcher = rememberLauncherForActivityResult(ActivityResultContracts.CreateDocument(mime)) { uri ->
        if (uri != null) model.act("File saved") { api ->
            val stream = resolver.openOutputStream(uri, "w") ?: throw IllegalStateException("Cannot write to this location.")
            stream.use { api.download(path, it) }
        }
    }
    return { launcher.launch(filename.substringAfterLast('/').substringAfterLast('\\').take(200).ifBlank { "attachment" }) }
}

@Composable
fun DownloadButton(model: LedgerModel, path: String, filename: String, text: String = "Save file", mime: String = "application/octet-stream") {
    val download = rememberDownload(model, path, filename, mime)
    OutlinedButton(onClick = download, enabled = !model.busy) { Text(text) }
}

@Composable
fun UploadButton(model: LedgerModel, messageId: String) {
    val resolver = LocalContext.current.contentResolver
    val launcher = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        if (uri != null) model.act("File attached") { api ->
            val name = resolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use { cursor ->
                if (cursor.moveToFirst()) cursor.getString(0) else null
            } ?: "attachment"
            val bytes = resolver.openInputStream(uri)?.use { it.readBounded(25 * 1024 * 1024) } ?: throw IllegalArgumentException("Cannot read this file.")
            api.upload(messageId, name, bytes)
        }
    }
    OutlinedButton(onClick = { launcher.launch(arrayOf("*/*")) }, enabled = !model.busy) { Text("Attach file (up to 25 MiB)") }
}

@Composable
fun FileRow(model: LedgerModel, file: JSONObject, removable: Boolean = false) {
    Column {
        Text(file.text("filename"), style = MaterialTheme.typography.titleSmall)
        Text("${(file.optLong("size_bytes") + 1023) / 1024} KiB", style = MaterialTheme.typography.bodySmall)
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            DownloadButton(model, "/handoff-files/${segment(file.text("id"))}", file.text("filename"), mime = file.text("media_type").ifBlank { "application/octet-stream" })
            if (removable) ConfirmButton("Remove", "Remove this attachment from the draft?", !model.busy, danger = true) {
                model.act("Attachment removed") { it.request("DELETE", "/handoff-files/${segment(file.text("id"))}") }
            }
        }
    }
}

@Composable
fun ProjectFiles(model: LedgerModel, slug: String) = Load(model, "files:$slug", { it.request("GET", "/projects/${segment(slug)}/files") }) { data ->
    Page {
        if (data.rows("files").isEmpty()) item { Empty("No handoff attachments for this project.") }
        items(data.rows("files")) { FileRow(model, it) }
    }
}
