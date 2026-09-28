package com.cesarpetrescu.ledger

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import org.json.JSONObject

/** What each label on an entry means, in the order they appear on a row. */
val labelGuide = listOf(
    Triple("Asks you", Tone.Warn, "An agent asked you a question or needs something from you. It stays in your Inbox until you mark it handled."),
    Triple("Important", Tone.Accent, "The AI rated it important: a decision that changes direction, a blocker, a production problem, a deadline, or something you must act on."),
    Triple("High · Low", Tone.Danger, "A todo's priority, read from its text: high when urgent, blocking, or broken; low when nice to have."),
    Triple("S · M · L", Tone.Neutral, "A todo's estimated size: under an hour, about a day, or several days."),
    Triple("Due · Overdue", Tone.Neutral, "A deadline stated in the todo. Overdue once the date has passed."),
    Triple("Stale", Tone.Neutral, "A todo that has been open for more than 14 days."),
    Triple("Blocked · In progress · Done", Tone.Danger, "The state a status update reports. A project whose latest update is blocked is listed under Blocked in the Inbox."),
    Triple("Check", Tone.Warn, "The AI was unsure about some of the labels. Open the entry and correct them; saving a label unchanged confirms it."),
)

@Composable
fun LabelLegend() {
    Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
        labelGuide.forEach { (name, tone, meaning) ->
            Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
                Tag(name, tone)
                Text(meaning, style = MaterialTheme.typography.bodySmall)
            }
        }
    }
}

/** Explains the labels in a dialog. */
@Composable
fun LegendButton() {
    var open by remember { mutableStateOf(false) }
    TextButton(onClick = { open = true }, contentPadding = PaddingValues(vertical = 8.dp)) { Text("What do the labels mean?") }
    if (open) AlertDialog(onDismissRequest = { open = false }, title = { Text("What the labels mean") },
        text = { Column(Modifier.verticalScroll(rememberScrollState())) { LabelLegend() } },
        confirmButton = { TextButton(onClick = { open = false }) { Text("Close") } })
}

/**
 * AI labelling status: progress while a labeller works, or why it is paused
 * while entries wait. Null when there is nothing to say.
 */
fun aiStatusText(progress: JSONObject?): Pair<String, Boolean>? {
    progress ?: return null
    val waiting = progress.optInt("total") - progress.optInt("ready") - progress.optInt("failed")
    if (waiting <= 0) return null
    val problem = progress.text("problem")
    if (progress.optBoolean("configured") && (!progress.optBoolean("active") || problem.isNotBlank())) {
        return "AI labelling is paused: ${problem.ifBlank { "the labelling service is not running" }}. $waiting ${if (waiting == 1) "entry is" else "entries are"} waiting and will get titles and labels when it is back." to true
    }
    if (!progress.optBoolean("active")) return null
    return "AI summaries: ${progress.optInt("ready")} of ${progress.optInt("total")} entries processed." to false
}

@Composable
fun AiStatus(progress: JSONObject?) {
    val (text, paused) = aiStatusText(progress) ?: return
    if (paused) Surface(Modifier.fillMaxWidth().padding(horizontal = 20.dp, vertical = 8.dp), color = MaterialTheme.colorScheme.errorContainer, contentColor = MaterialTheme.colorScheme.onErrorContainer, shape = MaterialTheme.shapes.small) {
        Text(text, Modifier.padding(12.dp), style = MaterialTheme.typography.bodySmall)
    } else Text(text, Modifier.padding(horizontal = 20.dp, vertical = 8.dp), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
}

/** How Ledger works and how it decides what to show. */
@Composable
fun HelpScreen() {
    val sections = listOf(
        "How Ledger works" to "Your agents write down what they did, decided, and need from you. Ledger keeps all of it, labels it with AI, and shows you what matters first.",
        "Where to look" to "Inbox: questions agents are waiting on you to answer, the most urgent todos, blocked projects, and each project's week.\n" +
            "Projects: one screen per project with its week, activity, todos, and decisions.\n" +
            "Reading: linked articles your agents found, to read and star.\n" +
            "Handoffs: work passed from one agent, or from you, to another.\n" +
            "More › Table: every project's entries in one list, with filters.\n" +
            "More › Agents: what each agent did lately and how to connect a new one.",
        "How the Inbox decides" to "Needs you lists entries where the AI found a question or request for you; each stays until you mark it handled, and a snoozed one comes back the next day.\n" +
            "Todos shows open todos, the most urgent first: due within a week, then high priority, then the oldest.\n" +
            "Blocked lists projects whose latest status update says they are blocked.\n" +
            "This week is an AI summary of each active project's last seven days.",
        "AI labels" to "Titles, summaries, tags, and labels are written by AI from each entry's text; the text itself is never changed. Open an entry and tap Edit labels to correct anything: your corrections are kept, and the AI learns from them. Routine entries such as checkpoints are hidden unless you ask for them, and repeats are folded under one row.",
        "Undo and Trash" to "Every quick action shows Undo, and More › Recent actions lets you undo any of the last seven days' actions. Deleted entries and projects stay in More › Trash for 30 days.",
    )
    Page {
        items(sections) { (title, body) ->
            Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                Text(title, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
                body.split('\n').forEach { line ->
                    val name = line.substringBefore(": ", "")
                    Text(buildAnnotatedString {
                        if (name.isNotEmpty() && name.length < 30) {
                            withStyle(SpanStyle(fontWeight = FontWeight.SemiBold)) { append(name) }
                            append(": " + line.substringAfter(": "))
                        } else append(line)
                    }, style = MaterialTheme.typography.bodyMedium)
                }
            }
        }
        item { Text("What the labels mean", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold) }
        item { LabelLegend() }
    }
}

/** Agents: what each one did lately, and how to connect one. */
@Composable
fun AgentsScreen(model: LedgerModel) = Load(model, "agents", { it.request("GET", "/agents") }) { data ->
    val origin = model.api?.origin.orEmpty()
    val agents = data.rows("agents")
    Page {
        item { Text("What each agent did lately and what it is waiting on you for.", color = MaterialTheme.colorScheme.onSurfaceVariant) }
        if (agents.isEmpty()) item { Empty("No agent has written to Ledger yet. Connect one using the steps below.") }
        items(agents, key = { it.text("name") }) { agent -> AgentCard(model, agent) }
        item {
            Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                Text("Connect an agent", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
                Text("Claude, ChatGPT, or another MCP app: add this address as a remote MCP server, then approve it in your browser.", style = MaterialTheme.typography.bodyMedium)
                SelectionContainer { Text("$origin/mcp", fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodySmall) }
                Text("Codex: install the Ledger CLI and run this on the computer, then approve its code under Settings › Approve a device.", style = MaterialTheme.typography.bodyMedium)
                SelectionContainer { Text("ledger connect codex --server $origin", fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodySmall) }
            }
        }
        item { SummaryCard("Connected apps", body = "Apps with access to every project, and their tokens. Revoke one to cut its access.") { model.go("clients") } }
    }
}

/** A full-width, compact link line; the whole row is the touch target. */
@Composable
private fun LinkLine(text: String, bold: Boolean = false, onClick: () -> Unit) {
    Text(text, Modifier.fillMaxWidth().clickable(onClick = onClick).padding(vertical = 6.dp), color = MaterialTheme.colorScheme.primary,
        style = MaterialTheme.typography.bodyMedium, fontWeight = if (bold) FontWeight.SemiBold else null, maxLines = 2, overflow = TextOverflow.Ellipsis)
}

@Composable
private fun AgentCard(model: LedgerModel, agent: JSONObject) {
    val name = agent.text("name")
    OutlinedCard(Modifier.fillMaxWidth()) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Row(verticalAlignment = androidx.compose.ui.Alignment.CenterVertically) {
                Text(name, Modifier.weight(1f), style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold, fontFamily = FontFamily.Monospace)
                agent.text("last_active").takeIf { it.isNotBlank() }?.let { Text("Active ${ago(it)} ago", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
            }
            val week = agent.optInt("week_entries")
            val projects = agent.rows("projects").joinToString(", ") { it.text("name") }
            Text((if (week > 0) "$week ${if (week == 1) "entry" else "entries"} this week" else "Quiet this week") + " · ${agent.optInt("entries")} in total" + if (projects.isNotBlank()) " · $projects" else "",
                style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            val asks = agent.optInt("open_asks")
            if (asks > 0) LinkLine("$asks ${if (asks == 1) "question waits" else "questions wait"} for your answer", bold = true) { model.tab("inbox") }
            val handoffs = agent.optInt("handoffs")
            if (handoffs > 0) LinkLine("Working on $handoffs ${if (handoffs == 1) "handoff" else "handoffs"}", bold = true) { model.tab("handoffs") }
            agent.rows("latest").forEach { entry ->
                LinkLine("${entryTitle(entry)} · ${entry.text("project_name")}") { model.go("entry-view/${segment(entry.text("id"))}") }
            }
            LinkLine("All of its activity") { model.go("table/${segment(name)}") }
        }
    }
}
