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
    var before by rememberSaveable { mutableStateOf("") }
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
                Field("Target (optional)", target, { target = it }, max = 200)
                Button(onClick = {
                    filter = "archive=$archive&q=${segment(query)}&status=$status&project=${segment(project)}&target=${segment(target)}"
                    before = ""; filters = false
                }) { Text("Apply filters") }
                }
            }
        }
        Load(model, "handoffs:$filter:$before", { it.request("GET", "/handoffs?$filter&before=${segment(before)}") }) { data ->
            Page {
                if (data.rows("handoffs").isEmpty()) item { Empty("No handoffs match this view.") }
                items(data.rows("handoffs")) { h ->
                    SummaryCard(h.text("title"), h.text("project_name").ifBlank { "General" } + " · " + displayTime(h.text("updated_at")),
                        "${h.optInt("ready_count")} ready · ${h.optInt("in_progress_count")} in progress · ${h.optInt("done_count")} done\n${h.text("description")}") { model.go("handoff/${h.text("id")}") }
                }
                item { Row {
                    if (before.isNotBlank()) TextButton(onClick = { before = "" }) { Text("Latest") }
                    if (data.text("next_before").isNotBlank()) TextButton(onClick = { before = data.text("next_before") }) { Text("Older handoffs") }
                } }
            }
        }
    }
}

@Composable
fun HandoffDetail(model: LedgerModel, id: String) {
    var before by rememberSaveable { mutableStateOf("") }
    Load(model, "handoff:$id:$before", { it.request("GET", "/handoffs/${segment(id)}?messages=50&before=${segment(before)}") }) { data ->
        val h = data.getJSONObject("handoff")
        Page {
            item { SummaryCard(h.text("title"), h.text("project_name").ifBlank { "General" }, listOf(h.text("description"), h.text("scope")).filter { it.isNotBlank() }.joinToString("\n\n")) }
            item {
                Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    Button(onClick = { model.go("message-new/$id") }, enabled = !model.busy) { Text("Add message") }
                    OutlinedButton(onClick = { model.go("handoff-edit/$id") }, enabled = !model.busy) { Text("Edit details") }
                    DownloadButton(model, "/handoffs/${segment(id)}/export", "handoff-$id.md", "Export", "text/markdown")
                }
            }
            items(data.rows("messages"), key = { it.text("id") }) { message -> MessageCard(model, message) }
            item { Row {
                if (before.isNotBlank()) TextButton(onClick = { before = "" }) { Text("Latest messages") }
                if (data.text("next_before").isNotBlank()) TextButton(onClick = { before = data.text("next_before") }) { Text("Older messages") }
            } }
        }
    }
}

@Composable
private fun MessageCard(model: LedgerModel, message: JSONObject) {
    val id = message.text("id")
    var retarget by remember { mutableStateOf(false) }
    var target by rememberSaveable { mutableStateOf(message.text("target")) }
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        SummaryCard("${label(message.text("work_state"))} · ${label(message.text("delivery_state"))}",
            "${displayTime(message.text("created_at"))} · ${message.text("source")}" + if (message.text("target").isNotBlank()) " → ${message.text("target")}" else "", message.text("body"))
        Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            messageActions(message.text("work_state"), message.text("delivery_state")).forEach { action ->
                OutlinedButton(enabled = !model.busy, onClick = { model.act("${label(action)} applied") { it.request("POST", "/handoff-messages/${segment(id)}/actions", json("action" to action)) } }) { Text(label(action)) }
            }
            TextButton(onClick = { retarget = true }, enabled = !model.busy) { Text("Retarget") }
        }
        message.rows("files").forEach { file -> FileRow(model, file, message.text("work_state") == "draft") }
        if (message.text("work_state") == "draft") UploadButton(model, id)
        HorizontalDivider()
    }
    if (retarget) AlertDialog(onDismissRequest = { retarget = false }, title = { Text("Retarget message") },
        text = { Field("Target", target, { target = it }, max = 200) },
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
            item { Field("Target (optional)", target, { target = it }, max = 200) }
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
        Text(if (draft) "Save as draft · attach files before publishing" else "Publish immediately")
    }
}

@Composable
fun MessageEditor(model: LedgerModel, id: String) {
    var body by rememberSaveable { mutableStateOf("") }
    var target by rememberSaveable { mutableStateOf("") }
    var draft by rememberSaveable { mutableStateOf(true) }
    Page {
        item { Text("Add a message", style = MaterialTheme.typography.headlineSmall) }
        item { Field("Message", body, { body = it }, multiline = true, max = 100000) }
        item { Field("Target (optional)", target, { target = it }, max = 200) }
        item { DraftSwitch(draft) { draft = it } }
        item { Text("Messages are permanent. Add a correction as a new message.", style = MaterialTheme.typography.bodySmall) }
        item { Button(enabled = !model.busy && body.isNotBlank(), modifier = Modifier.fillMaxWidth(), onClick = {
            model.act("Message added", after = model::back) { it.request("POST", "/handoffs/${segment(id)}/messages", json("body" to body.trim(), "target" to target.trim(), "draft" to draft)) }
        }) { Text("Add message") } }
    }
}

@Composable
fun DownloadButton(model: LedgerModel, path: String, filename: String, text: String = "Save file", mime: String = "application/octet-stream") {
    val resolver = LocalContext.current.contentResolver
    val launcher = rememberLauncherForActivityResult(ActivityResultContracts.CreateDocument(mime)) { uri ->
        if (uri != null) model.act("File saved") { api ->
            val bytes = api.download(path)
            val stream = resolver.openOutputStream(uri, "w") ?: throw IllegalStateException("Cannot write to this location.")
            stream.use { it.write(bytes) }
        }
    }
    OutlinedButton(onClick = { launcher.launch(filename.substringAfterLast('/').substringAfterLast('\\').take(200).ifBlank { "attachment" }) }, enabled = !model.busy) { Text(text) }
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
            DownloadButton(model, "/handoff-files/${segment(file.text("id"))}", file.text("filename"))
            if (removable) ConfirmButton("Remove", "Remove this attachment from the draft?", !model.busy) {
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
