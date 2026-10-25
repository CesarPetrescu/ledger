package com.cesarpetrescu.ledger

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import org.json.JSONObject

/** What each label on an entry means, in the order they appear on a row. */
val labelGuide = listOf(
    Triple("Asks you", Tone.Warn, "An agent asked you a question or needs something from you. It stays on Today until you mark it handled."),
    Triple("Important", Tone.Accent, "The AI rated it important: a decision that changes direction, a blocker, a production problem, a deadline, or something you must act on."),
    Triple("High · Low", Tone.Danger, "A todo's priority, read from its text: high when urgent, blocking, or broken; low when nice to have. A row shows only the first that applies of High, Important, and Low."),
    Triple("S · M · L", Tone.Neutral, "A todo's estimated size: under an hour, about a day, or several days."),
    Triple("Due · Overdue", Tone.Neutral, "A deadline stated in the todo. Overdue once the date has passed."),
    Triple("Stale", Tone.Neutral, "A todo that has been open for more than 14 days."),
    Triple("Blocked · In progress · Done", Tone.Danger, "The state a status update reports. A project whose latest update is blocked is listed under Blocked on Today."),
    Triple("Check", Tone.Warn, "The AI was unsure about some of the labels. Open the entry and correct them; saving a label unchanged confirms it."),
)

@Composable
fun LabelLegend() {
    Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
        labelGuide.forEach { (name, tone, meaning) ->
            Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
                Tag(name, tone)
                Text(meaning, style = MaterialTheme.typography.bodySmall, color = ledgerColors.body)
            }
        }
    }
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
    return "AI summaries: ${progress.optInt("ready")} of ${plural(progress.optInt("total"), "entry", "entries")} processed." to false
}

/** The one line a screen shows for [aiStatusText]: "AI labels paused · 3 entries waiting", or the progress itself. */
fun aiStatusLine(progress: JSONObject?): String? {
    val (text, paused) = aiStatusText(progress) ?: return null
    if (!paused) return text
    val waiting = progress!!.optInt("total") - progress.optInt("ready") - progress.optInt("failed")
    return "AI labels paused · ${plural(waiting, "entry", "entries")} waiting"
}

/** A quiet line, not an alarm: labelling pausing loses nothing. Why? tells the whole reason. */
@Composable
fun AiStatus(progress: JSONObject?) {
    val (text, paused) = aiStatusText(progress) ?: return
    var why by remember { mutableStateOf(false) }
    Row(Modifier.fillMaxWidth().padding(start = Gutter, end = 4.dp, top = 4.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        Box(Modifier.size(8.dp).clip(CircleShape).background(if (paused) toneColors(Tone.Warn).second else MaterialTheme.colorScheme.primary))
        Text(aiStatusLine(progress).orEmpty(), Modifier.weight(1f).padding(vertical = 10.dp), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        if (paused) TextButton(onClick = { why = true }) { Text("Why?") }
    }
    if (why) AlertDialog(onDismissRequest = { why = false }, title = { Text("AI labelling is paused") }, text = { Text(text) },
        confirmButton = { TextButton(onClick = { why = false }) { Text("Close") } })
}

/** The Help screen's sections; a line's short "Name: " prefix is shown in bold. */
val helpSections = listOf(
    "How Ledger works" to "Your agents write down what they did, decided, and need from you. Ledger keeps all of it, labels it with AI, and shows you what matters first.",
    "Where to look" to "Today: questions agents are waiting on you to answer, the most urgent todos, blocked projects, and each project's week.\n" +
        "Projects: one screen per project. Up top, what waits for you, its open todos, and its week; below, its log, todos, and decisions, and About for its details, repositories, files (handoff attachments), and handoffs.\n" +
        "Handoffs: work passed from one agent, or from you, to another, and research tasks.\n" +
        "Reading: linked articles your agents found, to read and star.\n" +
        "Search: words find projects, decisions, and notes by meaning. With no words, it lists every project's entries, filtered by project, kind, agent, or tag.\n" +
        "Account: the round button at the top right. It has Calendar, Agents, Recent actions, Trash, Settings, and Help.\n" +
        "Account › Calendar: your Nextcloud events, with todos that are due and project deadlines. Settings › Access › Calendars connects Nextcloud.\n" +
        "Account › Agents: what each agent did lately and what it waits on you for.\n" +
        "Settings › Access: connect an agent and manage what can reach your projects.",
    "How Today decides" to "Needs you lists entries where the AI found a question or request for you; each stays until you mark it handled, and a snoozed one comes back the next day.\n" +
        "Todos shows open todos, the most urgent first: due within a week, then high priority, then the oldest.\n" +
        "Blocked lists projects whose latest status update says they are blocked.\n" +
        "This week is an AI summary of each active project's last seven days.",
    "Research" to "A research task runs in a sandbox that your own dispatcher starts. Queue one from the web console under Handoffs › Research, or ask an agent to. It is listed under Handoffs with its state.\n" +
        "When it is ready for review, read the result: Accept publishes it to the project's log. To change something, Reply with what to change, then Send back. Answer a question the same way, then Resume.\n" +
        "Files travel both ways: attach them to a reply, and result files arrive on the thread.",
    "Repositories" to "A project's About › Repositories lists the Git repositories it spans; Link a repository adds one. Agents see them with the project and clone with their own access. With a read-only GitHub token (web console › Access › GitHub sync), Ledger checks each GitHub repository every 15 minutes for its latest commit, open pull requests, and latest release.",
    "AI labels" to "Titles, summaries, tags, and labels are written by AI from each entry's text; the text itself is never changed. Open an entry and choose Edit labels (under ⋮ unless the AI was unsure) to correct anything; your corrections are kept, and the AI learns from them. Routine entries such as checkpoints are hidden unless you ask for them, and repeats are folded under one row.",
    "Access" to "Settings › Access › Connect an agent: the address to add to Claude, ChatGPT, or another MCP app, and the Codex command.\n" +
        "Settings › Access › Connected apps: apps with access to your projects. Revoke one to cut it off.\n" +
        "Settings › Access › API keys: keys that let a server such as Adastrion Core dispatch research. Create them in the web console under Access › API keys; revoke them here.\n" +
        "Settings › Access › Approve a device: enter the code the Ledger CLI shows.\n" +
        "Settings › Access › Passkeys: sign in with a passkey from Google Password Manager, or from Bitwarden and other providers on Android 14 or newer. Once you have one, the password alone no longer signs in here, on the web, or when approving an app. Adding or removing one asks for the password. Locked out? Run ledger-admin reset-passkeys on the server.\n" +
        "Settings › Access › Signed-in devices: every browser and phone signed in to the console. Sign out one, or everywhere else. This phone stays signed in for 30 days after its last use, 90 days at most.\n" +
        "The web console keeps all of these on its Access page, with GitHub sync and the approval password.",
    "Undo and Trash" to "Every quick action shows Undo, and Account › Recent actions lets you undo any of the last seven days' actions. Delete an entry or a project from its ⋮ menu; it stays in Account › Trash for 30 days.",
)

/** How Ledger works and how it decides what to show. */
@Composable
fun HelpScreen() {
    Page {
        items(helpSections) { (title, body) ->
            Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                Text(title, style = MaterialTheme.typography.titleMedium)
                body.split('\n').forEach { line ->
                    val name = line.substringBefore(": ", "")
                    Text(buildAnnotatedString {
                        if (name.isNotEmpty() && name.length < 40) {
                            withStyle(SpanStyle(fontWeight = FontWeight.SemiBold)) { append(name) }
                            append(": " + line.substringAfter(": "))
                        } else append(line)
                    }, style = MaterialTheme.typography.bodyMedium, color = ledgerColors.body)
                }
            }
        }
        item { Text("What the labels mean", style = MaterialTheme.typography.titleMedium) }
        item { LabelLegend() }
    }
}

/** Agents: what each one did lately. Connecting one and revoking access live under Settings › Access. */
@Composable
fun AgentsScreen(model: LedgerModel) = Load(model, "agents", { it.request("GET", "/agents") }) { data ->
    val agents = data.rows("agents")
    Page {
        item { TextButton(onClick = { model.go("settings/access") }, enabled = !model.busy, contentPadding = PaddingValues(vertical = 8.dp)) { Text("Manage access →") } }
        if (agents.isEmpty()) item {
            Column {
                Empty("No agent has written to Ledger yet.")
                OutlinedButton(onClick = { model.go("connect") }, enabled = !model.busy, shape = ButtonShape) { Text("Connect an agent") }
            }
        }
        items(agents, key = { it.text("name") }) { agent -> AgentCard(model, agent) }
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
    Group {
        Column(Modifier.padding(Gutter), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                AgentMark(name, 28.dp)
                Text(name, Modifier.weight(1f), style = MaterialTheme.typography.titleMedium)
                agent.text("last_active").takeIf { it.isNotBlank() }?.let { Text("Active ${ago(it)} ago", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
            }
            val week = agent.optInt("week_entries")
            val projects = agent.rows("projects").joinToString(", ") { it.text("name") }
            Text((if (week > 0) "$week ${if (week == 1) "entry" else "entries"} this week" else "Quiet this week") + " · ${agent.optInt("entries")} in total" + if (projects.isNotBlank()) " · $projects" else "",
                style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            val asks = agent.optInt("open_asks")
            if (asks > 0) LinkLine("$asks ${if (asks == 1) "question waits" else "questions wait"} for your answer", bold = true) { model.tab("inbox") }
            val handoffs = agent.optInt("handoffs")
            if (handoffs > 0) LinkLine("Working on $handoffs ${if (handoffs == 1) "handoff" else "handoffs"}", bold = true) { model.tab("handoffs") }
            // What it wrote reads as content, with its project muted; blue is kept for the actions around it.
            // Each row opens its entry, so a chevron says so, as on the Settings cards.
            val muted = MaterialTheme.colorScheme.onSurfaceVariant
            agent.rows("latest").forEach { entry ->
                Row(Modifier.fillMaxWidth().clickable { model.go("entry-view/${segment(entry.text("id"))}") }.padding(vertical = 6.dp),
                    horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                    Text(buildAnnotatedString {
                        append(entryTitle(entry))
                        withStyle(SpanStyle(color = muted)) { append(" · ${entry.text("project_name")}") }
                    }, Modifier.weight(1f), style = MaterialTheme.typography.bodyMedium, maxLines = 2, overflow = TextOverflow.Ellipsis)
                    Glyph("chevron", null, tint = muted)
                }
            }
            LinkLine("All of its activity") { model.go("table/${segment(name)}") }
        }
    }
}
