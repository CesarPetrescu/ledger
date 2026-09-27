package com.cesarpetrescu.ledger

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import org.json.JSONArray
import org.json.JSONObject

private class LabelSpec(val field: String, val label: String, val kinds: List<String>? = null, val options: List<String>? = null, val long: Boolean = false, val max: Int = 300)

private val labelSpecs = listOf(
    LabelSpec("title", "Title", max = 120),
    LabelSpec("importance", "Importance", options = listOf("routine", "useful", "important")),
    LabelSpec("gist", "Summary", long = true, max = 240),
    LabelSpec("category", "Category", max = 40),
    LabelSpec("tags", "Tags (comma separated)"),
    LabelSpec("ask", "Asks you"),
    LabelSpec("priority", "Priority", listOf("todo"), listOf("low", "normal", "high")),
    LabelSpec("size", "Size", listOf("todo"), listOf("", "S", "M", "L")),
    LabelSpec("due", "Due (YYYY-MM-DD)", listOf("todo"), max = 10),
    LabelSpec("state", "State", listOf("status"), listOf("", "done", "in_progress", "blocked")),
    LabelSpec("next_step", "Next step", max = 240),
    LabelSpec("blocker", "Blocked by", max = 240),
    LabelSpec("why", "Why", long = true),
)

/** The labels request body: changed fields to override, and fields to hand back to the AI. */
fun labelPatch(current: Map<String, String>, edited: Map<String, String>, reset: List<String> = emptyList()): JSONObject {
    val set = JSONObject()
    edited.forEach { (field, raw) ->
        val value = raw.trim()
        if (value != current[field].orEmpty()) set.put(field, if (field == "tags") JSONArray(value.split(Regex("[,\\s]+")).filter { it.isNotBlank() }) else value)
    }
    return json("set" to set, "reset" to JSONArray(reset))
}

/** Says which labels the AI doubted and which the owner corrected. */
@Composable
fun LabelNotes(meta: JSONObject) {
    val unsure = meta.strings("unsure")
    val edited = meta.strings("edited")
    if (unsure.isNotEmpty()) Text("The AI was unsure of: ${unsure.joinToString { label(it) }}. Check and correct if needed.",
        style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.tertiary)
    if (edited.isNotEmpty()) Text("Edited by you: ${edited.joinToString { label(it) }}", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
}

/** Lets the owner correct the AI's labels; corrections survive re-extraction and teach the labeller. */
@Composable
fun LabelEditor(model: LedgerModel, entry: JSONObject, close: () -> Unit) {
    val meta = entry.optJSONObject("meta") ?: return
    val specs = labelSpecs.filter { it.kinds == null || entry.text("kind") in it.kinds }
    val current = specs.associate { it.field to if (it.field == "tags") meta.strings("tags").joinToString(", ") else meta.text(it.field) }
    val edited = meta.strings("edited")
    val unsure = meta.strings("unsure")
    var open by remember { mutableStateOf(false) }
    var values by remember { mutableStateOf(current) }
    OutlinedButton(onClick = { values = current; open = true }, enabled = !model.busy) { Text("Edit labels") }
    if (!open) return
    val path = "/entries/${segment(entry.text("id"))}/labels"
    AlertDialog(onDismissRequest = { open = false }, title = { Text("Edit labels") }, text = {
        Column(Modifier.verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            specs.forEach { spec ->
                val value = values[spec.field].orEmpty()
                val change: (String) -> Unit = { values = values + (spec.field to it) }
                val name = spec.label + if (spec.field in unsure) " · AI unsure" else ""
                if (spec.options != null) Choice(name, value, spec.options.map { it to label(it).ifBlank { "None" } }, change)
                else Field(name, value, change, multiline = spec.long, max = spec.max)
            }
        }
    }, confirmButton = {
        TextButton(enabled = !model.busy && values["title"].orEmpty().isNotBlank(), onClick = {
            val body = labelPatch(current, values)
            open = false
            if (body.getJSONObject("set").length() > 0) model.act("Labels saved. Similar entries will be labelled this way.", after = close) { it.request("POST", path, body) }
        }) { Text("Save") }
    }, dismissButton = {
        Row {
            if (edited.isNotEmpty()) TextButton(enabled = !model.busy, onClick = {
                open = false
                model.act("Back to the AI's labels.", after = close) { it.request("POST", path, labelPatch(current, emptyMap(), edited)) }
            }) { Text("Reset to AI") }
            TextButton(onClick = { open = false }) { Text("Cancel") }
        }
    })
}
