package com.cesarpetrescu.ledger

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import java.time.OffsetDateTime
import java.time.temporal.ChronoUnit

/** The owner's quick actions from the last week, each undoable on its own. */
@Composable
fun HistoryScreen(model: LedgerModel) = Load(model, "actions", { it.request("GET", "/actions") }) { data ->
    Page {
        if (data.rows("actions").isEmpty()) item { Empty("Nothing to undo. Actions you take in the last 7 days appear here.") }
        items(data.rows("actions"), key = { it.text("id") }) { action ->
            ListItem(headlineContent = { Text(action.text("label")) },
                supportingContent = { Text(listOf(action.text("project_slug"), ago(action.text("created_at"))).filter { it.isNotBlank() }.joinToString(" · ")) },
                trailingContent = {
                    when {
                        action.optBoolean("undoable") -> TextButton(onClick = { model.undo(action.text("id")) }, enabled = !model.busy) { Text("Undo") }
                        !action.isNull("undone_at") -> Text("Undone", style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                })
            HorizontalDivider()
        }
    }
}

/** Deleted projects and entries, restorable until they are purged after 30 days. */
@Composable
fun TrashScreen(model: LedgerModel) = Load(model, "trash", { it.request("GET", "/trash") }) { data ->
    Page {
        if (data.rows("items").isEmpty()) item { Empty("Trash is empty.") }
        items(data.rows("items"), key = { it.text("id") }) { item ->
            val id = item.text("id")
            val days = runCatching { ChronoUnit.DAYS.between(OffsetDateTime.now(), OffsetDateTime.parse(item.text("purge_at"))).coerceAtLeast(0) }.getOrDefault(0L)
            val what = if (item.text("kind") == "project") "Project · ${plural(item.optInt("entry_count"), "entry", "entries")}" else "Entry in ${item.text("project_slug")}"
            SummaryCard(item.text("label"), what, "Deleted ${ago(item.text("deleted_at"))} · removed for good in ${plural(days.toInt(), "day")}", actions = {
                Button(onClick = { model.act("Restored") { it.request("POST", "/trash/${segment(id)}/restore") } }, enabled = !model.busy) { Text("Restore") }
                ConfirmButton("Delete forever", "Remove \"${item.text("label")}\" permanently? This cannot be undone.", !model.busy, danger = true) {
                    model.act("Deleted forever") { it.request("DELETE", "/trash/${segment(id)}") }
                }
            })
        }
    }
}

/** Moves a project to Trash once the owner types its slug. */
@Composable
fun DeleteProjectDialog(model: LedgerModel, slug: String, close: () -> Unit) {
    var typed by remember { mutableStateOf("") }
    AlertDialog(onDismissRequest = close, title = { Text("Delete project?") }, text = {
        // Filling the page, Load stretched the dialog to the screen's height.
        Load(model, "deletion:$slug", { it.request("GET", "/projects/${segment(slug)}/deletion") }, wrap = true) { p ->
            Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                Text("${p.text("name")} and its ${plural(p.optInt("entries"), "entry", "entries")} move to Trash. You can undo this or restore it for 30 days." +
                    (if (p.optInt("handoffs") > 0) " ${plural(p.optInt("handoffs"), "handoff stays", "handoffs stay")}, without the project link until restored." else ""))
                OutlinedTextField(typed, { typed = it.trim() }, label = { Text("Type $slug to confirm") }, singleLine = true, modifier = Modifier.fillMaxWidth())
            }
        }
    }, confirmButton = {
        TextButton(enabled = typed == slug && !model.busy, onClick = {
            close()
            model.undoable("Project moved to Trash", after = { model.tab("projects") }) { it.request("DELETE", "/projects/${segment(slug)}", json("confirm" to slug)) }
        }) { Text("Delete", color = MaterialTheme.colorScheme.error) }
    }, dismissButton = { TextButton(onClick = close) { Text("Cancel") } })
}

/** Light, dark, or follow the phone's setting. */
@Composable
fun ThemeChoice(model: LedgerModel) {
    Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Text("Appearance", style = MaterialTheme.typography.titleSmall)
        SingleChoiceSegmentedButtonRow(Modifier.fillMaxWidth()) {
            THEMES.forEachIndexed { index, theme ->
                SegmentedButton(selected = model.theme == theme, onClick = { model.chooseTheme(theme) }, shape = SegmentedButtonDefaults.itemShape(index, THEMES.size)) {
                    Text(label(theme))
                }
            }
        }
    }
}
