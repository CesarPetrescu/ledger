@file:OptIn(androidx.compose.material3.ExperimentalMaterial3Api::class, androidx.compose.foundation.layout.ExperimentalLayoutApi::class)
package com.cesarpetrescu.ledger

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.input.nestedscroll.NestedScrollConnection
import androidx.compose.ui.input.nestedscroll.NestedScrollSource
import androidx.compose.ui.input.nestedscroll.nestedScroll
import androidx.compose.ui.layout.layout
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Constraints
import androidx.compose.ui.unit.dp
import org.json.JSONObject
import java.time.LocalDate
import java.time.OffsetDateTime
import java.time.format.DateTimeFormatter
import kotlin.math.roundToInt

private val tiers = listOf("focus", "maintain", "park").map { it to label(it) }

// ---- Projects ----

/** Projects by tier, each with what needs you, its open work, and its week; parked ones fold away. */
@Composable
fun ProjectsHome(model: LedgerModel) = Load(model, "table-projects", { it.request("GET", "/table/projects") }) { data ->
    val projects = data.rows("projects")
    LaunchedEffect(data) { model.knowProjects(projects) }
    var filter by rememberSaveable { mutableStateOf("all") }
    var parked by rememberSaveable { mutableStateOf(false) }
    val needs = projects.filter { it.optInt("needs_you") > 0 }
    val blocked = projects.filter { it.text("status_state") == "blocked" }
    val shown = when (filter) { "needs" -> needs; "blocked" -> blocked; else -> projects }
    Box(Modifier.fillMaxSize()) {
        LazyColumn(Modifier.fillMaxSize().testTag("page"), contentPadding = PaddingValues(bottom = 96.dp)) {
            item { AiStatus(data.optJSONObject("metadata")) }
            if (projects.isNotEmpty()) item {
                FilterChips(listOf("all" to "All ${projects.size}", "needs" to "Needs you ${needs.size}", "blocked" to "Blocked ${blocked.size}"), filter) { filter = it }
            }
            if (shown.isEmpty()) item { Box(Modifier.padding(horizontal = Gutter)) { Empty(if (projects.isEmpty()) "No projects yet." else "No projects match.") } }
            tiers.forEach { (tier, name) ->
                // A tier the app does not know is listed with Maintain rather than hidden.
                val group = shown.filter { p -> p.text("tier").let { if (tiers.any { t -> t.first == it }) it else "maintain" } == tier }
                if (group.isEmpty()) return@forEach
                val folded = tier == "park" && filter == "all" && !parked
                item(key = "h:$tier") {
                    SectionLabel(name, count = group.size, action = if (tier == "park" && filter == "all") ({
                        TextButton(onClick = { parked = !parked }) { Text(if (parked) "Hide" else "Show") }
                    }) else null)
                }
                if (!folded) items(group, key = { it.text("slug") }) { p -> ProjectRow(model, p) }
            }
        }
        LedgerFab("New project", Modifier.align(Alignment.BottomEnd)) { model.go("project-edit/") }
    }
}

@Composable
private fun ProjectRow(model: LedgerModel, p: JSONObject) {
    Column(Modifier.fillMaxWidth().clickable { model.go(projectRoute(p.text("slug"))) }.padding(horizontal = Gutter, vertical = 14.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
            Text(p.text("name"), Modifier.weight(1f), style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
            if (p.optInt("needs_you") > 0) Tag(plural(p.optInt("needs_you"), "question"), Tone.Warn)
            HealthTag(p.text("status_state"))
        }
        // Facts are metadata, not links: the whole row opens the project.
        Text(projectFacts(p, brief = true), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        val summary = p.text("digest").ifBlank { p.text("status_title").ifBlank { p.text("status_body") } }
        if (summary.isNotBlank()) Text(summary, style = MaterialTheme.typography.bodyMedium, color = ledgerColors.body, maxLines = 2, overflow = TextOverflow.Ellipsis)
    }
    GroupDivider()
}

/** A project row's facts. A date deadline reads "due 15 Nov 2026" and wraps as one piece. [brief] leaves out the tier and the
 * questions, which the row's heading and label already show. */
fun projectFacts(p: JSONObject, brief: Boolean = false): String = buildList {
    if (!brief) add(label(p.text("tier")))
    if (p.optInt("open_todos") > 0) add(plural(p.optInt("open_todos"), "open todo"))
    if (!brief && p.optInt("needs_you") > 0) add(plural(p.optInt("needs_you"), "needs you", "need you"))
    add(if (p.optInt("week_entries") > 0) "${p.optInt("week_entries")} this week" else "quiet")
    val deadline = p.text("deadline")
    // Free text stays as written; only a date is shortened and held together.
    val date = deadlineDate(deadline)?.replace(' ', ' ')
    if (deadline.isNotBlank()) add("due " + (date ?: deadline))
}.joinToString(" · ")

/** A date deadline as "15 Nov 2026"; null for free text such as "after the beta". */
fun deadlineDate(deadline: String): String? = runCatching { LocalDate.parse(deadline).format(DateTimeFormatter.ofPattern("d MMM yyyy")) }.getOrNull()

// ---- One project ----

/** A project's tabs: its log, todos and decisions, and About for its details, repositories, files and handoffs. */
val projectTabs = listOf("activity" to "Log", "todos" to "Todos", "decisions" to "Decisions", "details" to "About")

/** A project screen's slug, tab, and search from its route. Links to the older Handoffs, Files and Repos tabs open About, which leads to them. */
fun projectScreenArgs(route: String): Triple<String, String, String> {
    val parts = route.split('/').map { java.net.URLDecoder.decode(it, Charsets.UTF_8.name()) }
    val tab = parts.getOrElse(2) { "" }.let { if (it in listOf("handoffs", "files", "repos")) "details" else it }
    return Triple(parts.getOrElse(1) { "" }, tab.takeIf { t -> projectTabs.any { it.first == t } } ?: "activity", parts.getOrElse(3) { "" })
}

/** A count in a project's stat strip, what it counts, and what a tap [opens] ("inbox", a tab, "names" for the agents line, or "" for nothing). */
data class ProjectStat(val count: Int, val label: String, val opens: String)

/** The project's numbers: questions waiting for you, open todos, entries this week, and the agents who wrote them. */
fun projectStats(p: JSONObject, now: OffsetDateTime = OffsetDateTime.now()): List<ProjectStat> {
    val asks = p.optInt("needs_you")
    val todos = p.optInt("open_todos")
    val week = p.optInt("week_entries")
    val agents = p.strings("week_agents")
    return listOf(
        ProjectStat(asks, if (asks == 1) "question waits for you" else "questions wait for you", "inbox"),
        ProjectStat(todos, if (todos == 1) "open todo" else "open todos", "todos"),
        ProjectStat(week, (if (week == 1) "entry" else "entries") + " this week", "activity"),
        ProjectStat(agents.size, when {
            agents.isNotEmpty() -> agents.joinToString(", ", transform = ::writerName)
            p.text("last_entry_at").isNotBlank() -> "quiet; last entry ${ago(p.text("last_entry_at"), now)} ago"
            else -> "agents active"
        }, if (agents.isNotEmpty()) "names" else ""),
    )
}

/** Whether the project header has room for its numbers and summary. With the keyboard up or on a short (landscape)
 * screen they would leave a tab's list almost no room, so only the tabs stay. */
fun roomyProjectHeader(imeVisible: Boolean, screenHeightDp: Int) = !imeVisible && screenHeightDp >= 480

/** The first three numbers in one strip; a question waiting for you is amber. Each opens what it counts. */
@Composable
private fun StatStrip(stats: List<ProjectStat>, open: (String) -> Unit) {
    Group {
        Row(Modifier.height(IntrinsicSize.Min)) {
            stats.forEachIndexed { index, stat ->
                if (index > 0) VerticalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                val ask = stat.opens == "inbox" && stat.count > 0
                Column(Modifier.weight(1f).fillMaxHeight().clickable { open(stat.opens) }.padding(horizontal = 12.dp, vertical = 10.dp),
                    verticalArrangement = Arrangement.spacedBy(2.dp)) {
                    Text("${stat.count}", style = MaterialTheme.typography.headlineSmall, color = if (ask) toneColors(Tone.Warn).second else MaterialTheme.colorScheme.onSurface)
                    Text(stat.label, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
        }
    }
}

/** One project: where it stands, then its log, todos, decisions, and About. */
@Composable
fun ProjectScreen(model: LedgerModel, slug: String, initialTab: String = "activity", initialQuery: String = "") {
    var tab by rememberSaveable { mutableStateOf(initialTab.takeIf { t -> projectTabs.any { it.first == t } } ?: "activity") }
    var q by rememberSaveable { mutableStateOf(initialQuery) }
    var showRoutine by rememberSaveable { mutableStateOf(initialQuery.isNotBlank()) }
    var todoState by rememberSaveable { mutableStateOf("open") }
    Load(model, "project-summary:$slug", { api -> api.request("GET", "/table/projects").rows("projects").firstOrNull { it.text("slug") == slug } ?: throw ApiError(404, "Project not found.") }) { p ->
        // The top bar names the project; the header says how it is doing.
        LaunchedEffect(p) { model.knowProjects(listOf(p)) }
        // Above the tabs, the header slides away as a tab's list scrolls on and returns on any scroll back, so a small
        // phone keeps most of its height for the list; a new tab starts with it shown.
        val header = remember { SlidingHeader() }
        LaunchedEffect(tab) { header.show() }
        Box(Modifier.fillMaxSize()) {
            Column(Modifier.nestedScroll(header)) {
                Column(header.modifier.padding(start = Gutter, end = 4.dp, bottom = 12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    val roomy = roomyProjectHeader(WindowInsets.isImeVisible, LocalConfiguration.current.screenHeightDp)
                    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        val deadline = p.text("deadline").let { deadlineDate(it) ?: it }
                        Text(listOf(label(p.text("tier")), if (deadline.isBlank()) "" else "due $deadline").filter { it.isNotBlank() }.joinToString(" · "),
                            Modifier.weight(1f), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        HealthTag(p.text("status_state"))
                        var deleting by remember { mutableStateOf(false) }
                        Overflow("More actions for ${p.text("name")}", listOf(
                            MenuAction("Edit project") { model.go("project-edit/$slug") },
                            MenuAction("Delete project", danger = true) { deleting = true }), enabled = !model.busy)
                        if (deleting) DeleteProjectDialog(model, slug) { deleting = false }
                    }
                    val stats = projectStats(p)
                    if (roomy) Box(Modifier.padding(end = 12.dp)) {
                        // The question count opens Today on top of the project, so Back returns here, as it does on the web.
                        StatStrip(stats.take(3)) { target -> if (target == "inbox") model.go("inbox") else { tab = target; if (target == "todos") todoState = "open" } }
                    }
                    // Tap the summary to read it all, including what the project needs from you.
                    var expanded by rememberSaveable { mutableStateOf(false) }
                    val summary = p.text("digest").ifBlank { p.text("status_title") }
                    if (roomy) Column(Modifier.padding(end = 12.dp).clickable { expanded = !expanded }, verticalArrangement = Arrangement.spacedBy(4.dp)) {
                        val agents = stats.last()
                        Text(if (agents.opens == "names") "This week: ${agents.label}" else agents.label.replaceFirstChar { it.uppercase() },
                            style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = if (expanded) Int.MAX_VALUE else 1, overflow = TextOverflow.Ellipsis)
                        if (summary.isNotBlank()) Text(summary, style = MaterialTheme.typography.bodyMedium, color = ledgerColors.body,
                            maxLines = if (expanded) Int.MAX_VALUE else 2, overflow = TextOverflow.Ellipsis)
                        if (expanded && p.text("needs_me").isNotBlank()) Text("Needs you: ${p.text("needs_me")}", style = MaterialTheme.typography.bodySmall, color = toneColors(Tone.Warn).second)
                    }
                }
                PrimaryTabRow(selectedTabIndex = projectTabs.indexOfFirst { it.first == tab }, containerColor = MaterialTheme.colorScheme.background) {
                    projectTabs.forEach { (id, name) -> Tab(selected = tab == id, onClick = { tab = id },
                        text = { Text(name, style = MaterialTheme.typography.labelMedium.copy(fontSize = MaterialTheme.typography.labelLarge.fontSize), maxLines = 1) },
                        // Only the selected tab is blue; the rest are muted, like other metadata.
                        selectedContentColor = MaterialTheme.colorScheme.primary, unselectedContentColor = MaterialTheme.colorScheme.secondary) }
                }
                when (tab) {
                    "details" -> ProjectAbout(model, slug, p)
                    else -> {
                        // Entries load only while one of their tabs is open. A search looks everywhere, routine entries included.
                        val query = tableQuery(tab, project = slug, q = q, status = todoState, hideRoutine = !showRoutine && q.isBlank())
                        val pager = rememberPager(model, query)
                        PagedEntries(model, pager, query, bottom = 96, empty = if (tab == "todos" && todoState == "open") "No open todos. Nice." else "No entries match.", header = {
                            item {
                                Row(Modifier.padding(horizontal = Gutter, vertical = 8.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                                    if (q.isNotBlank()) AssistChip(onClick = { q = "" }, label = { Text("Search: $q ✕") }, shape = MaterialTheme.shapes.small)
                                    if (tab == "todos") listOf("open" to "Open", "done" to "Done").forEach { (id, name) ->
                                        FilterChip(selected = todoState == id, onClick = { todoState = id }, label = { Text(name) }, shape = MaterialTheme.shapes.small)
                                    } else FilterChip(selected = showRoutine, onClick = { showRoutine = !showRoutine }, label = { Text("Show routine") }, shape = MaterialTheme.shapes.small)
                                }
                            }
                        }) { entries ->
                            val folded = foldRepeats(entries).let { heads -> if (tab == "todos") heads.sortedBy { priorityRank(it.entry) } else heads }
                            runsBy(folded) { if (tab == "todos") "" else dayLabel(it.entry.text("created_at")) }.forEach { (day, group) ->
                                if (day.isNotBlank()) item(key = "h:$day:${group.first().entry.text("id")}") { SectionLabel(day) }
                                items(group, key = { it.entry.text("id") }) { f -> EntryItem(model, f.entry, tab, f.repeats, showProject = false, check = tab == "todos") { e, _ -> openEntry(model, e) } }
                            }
                        }
                    }
                }
            }
            if (tab != "details") LedgerFab("Add entry", Modifier.align(Alignment.BottomEnd)) { model.go("entry/$slug") }
        }
    }
}

/** A header that a list below it scrolls out of sight first and brings back on any scroll the other way, like Material's
 * enterAlways top app bar. Its [modifier] clips the header to the part still shown. */
class SlidingHeader : NestedScrollConnection {
    private var height = 0
    private var hidden by mutableFloatStateOf(0f)
    fun show() { hidden = 0f }
    override fun onPreScroll(available: Offset, source: NestedScrollSource): Offset {
        val before = hidden
        hidden = slideHeader(hidden, available.y, height)
        return Offset(0f, before - hidden)
    }
    val modifier = Modifier.clipToBounds().layout { measurable, constraints ->
        val placeable = measurable.measure(constraints.copy(minHeight = 0, maxHeight = Constraints.Infinity))
        height = placeable.height
        val shown = (placeable.height - hidden.roundToInt()).coerceAtLeast(0)
        layout(placeable.width, shown) { placeable.place(0, shown - placeable.height) }
    }
}

/** How much of a [height]-pixel header is hidden after a scroll of [dy] (negative as the list moves on). */
fun slideHeader(hidden: Float, dy: Float, height: Int) = (hidden - dy).coerceIn(0f, height.toFloat())

/** The Details fields, like the web's; [summary] is the project's row from /table/projects, for its latest status. */
fun projectDetails(p: JSONObject, summary: JSONObject): List<Pair<String, String>> = listOf(
    "Tier" to label(p.text("tier")),
    "Hours per week" to p.optInt("hours_wk").toString(),
    "Goal" to p.text("goal"),
    "Deadline" to p.text("deadline").let { deadlineDate(it) ?: it },
    "Type" to p.text("type"),
    "Description" to p.text("description"),
    "Needs me" to p.text("needs_me"),
    "Automate" to p.text("automate"),
    "Stack" to p.text("stack"),
    // The AI's title, or the update itself until labelling catches up; then who wrote it and when.
    "Latest status" to listOf(summary.text("status_title").ifBlank { summary.text("status_body") },
        listOf(writerName(summary.text("status_source")), displayTime(summary.text("status_at"))).filter { it.isNotBlank() }.joinToString(" · ")
    ).filter { it.isNotBlank() }.joinToString("\n"),
)

/** About: where the project's code, files and handoffs are, then its fields, read-only; Edit project changes them. */
@Composable
private fun ProjectAbout(model: LedgerModel, slug: String, summary: JSONObject) =
    Load(model, "project-details:$slug", { it.request("GET", "/projects/${segment(slug)}?entries=1") }) { data ->
        Page {
            item {
                Group {
                    NavRow("Repositories", "Where the code lives; agents clone from here") { model.go("project-repos/${segment(slug)}") }
                    GroupDivider()
                    NavRow("Files", "Attachments from this project's handoffs") { model.go("project-files/${segment(slug)}") }
                    GroupDivider()
                    NavRow("Handoffs", "Work passed between agents for this project") { model.go("project-handoffs/${segment(slug)}") }
                }
            }
            item { OutlinedButton(onClick = { model.go("project-edit/$slug") }, enabled = !model.busy, shape = ButtonShape) { Text("Edit project") } }
            items(projectDetails(data.getJSONObject("project"), summary)) { (name, value) ->
                // An empty field reads as a dash, as on the web.
                if (value.isBlank()) Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
                    MiniHeading(name)
                    Text("—", style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                } else Fact(name, value)
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
        // Editing, the top bar already says "<Project> · Edit".
        if (existingSlug.isBlank()) item { Text("New project", style = MaterialTheme.typography.headlineSmall) }
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
        item { Button(enabled = !model.busy && validProjectSlug(slug) && name.isNotBlank() && hours.toIntOrNull() in 0..168, shape = ButtonShape,
            onClick = {
                model.act(after = model::back) { api ->
                    if (existingSlug.isBlank()) {
                        try { api.request("GET", "/projects/${segment(slug)}?entries=1"); throw IllegalArgumentException("That project already exists. Open it to edit.") }
                        catch (e: ApiError) { if (e.status != 404) throw e }
                    }
                    api.request("PUT", "/projects/${segment(slug)}", json("name" to name.trim(), "tier" to tier, "hours_wk" to hours.toInt(),
                        "description" to description, "goal" to goal, "type" to type, "deadline" to deadline, "needs_me" to needs, "automate" to automate, "stack" to stack))
                }
            }, modifier = Modifier.fillMaxWidth().heightIn(min = 48.dp)) { Text("Save project") } }
    }
}

@Composable
fun EntryEditor(model: LedgerModel, slug: String) {
    var kind by rememberSaveable { mutableStateOf("note") }
    var body by rememberSaveable { mutableStateOf("") }
    Page {
        item { Choice("Entry kind", kind, entryKinds) { kind = it } }
        item { Field("Entry", body, { body = it }, multiline = true, max = 4000) }
        item { Text("Entries can't be edited. Add a correction as a new entry.", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
        item { Button(onClick = { model.act("Entry added", after = model::back) { it.request("POST", "/projects/${segment(slug)}/entries", json("kind" to kind, "body" to body.trim())) } },
            enabled = !model.busy && body.isNotBlank(), shape = ButtonShape, modifier = Modifier.fillMaxWidth().heightIn(min = 48.dp)) { Text("Add entry") } }
    }
}

/** The Git repositories a project spans. Agents see them with the project and clone with their own access. */
@Composable
fun ProjectRepos(model: LedgerModel, slug: String) {
    var url by rememberSaveable { mutableStateOf("") }
    var role by rememberSaveable { mutableStateOf("") }
    var branch by rememberSaveable { mutableStateOf("") }
    // The form stays folded until asked for, and folds again once a link succeeds.
    var linking by rememberSaveable { mutableStateOf(false) }
    Load(model, "repos:$slug", { it.request("GET", "/projects/${segment(slug)}/repos") }) { data ->
        val repos = data.rows("repos")
        Page {
            item { Text("Where this project's code lives; link every repository it spans. Agents see them with the project and clone with their own Git access.",
                style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant) }
            // The state comes before the action it motivates.
            if (repos.isEmpty()) item { Empty("No repositories linked yet.") }
            // Above the list, so the button is in sight without scrolling past every repo and the form opens where it was tapped.
            if (!linking) item {
                // With nothing linked yet, linking is the screen's one job; otherwise it is outlined, beside each card's red Unlink.
                if (repos.isEmpty()) Button(onClick = { linking = true }, enabled = !model.busy, shape = ButtonShape) { Text("Link a repository") }
                else OutlinedButton(onClick = { linking = true }, enabled = !model.busy, shape = ButtonShape) { Text("Link a repository") }
            } else {
                item { Text("Link a repository", style = MaterialTheme.typography.titleMedium) }
                item { Field("Repository URL", url, { url = it }, max = 500, placeholder = "https://github.com/owner/repo", keyboard = KeyboardType.Uri) }
                item { Field("Role", role, { role = it }, max = 60, placeholder = "backend, android, docs…") }
                item { Field("Branch", branch, { branch = it }, max = 200, placeholder = "Default branch") }
                item {
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        Button(enabled = !model.busy && url.isNotBlank(), shape = ButtonShape, onClick = {
                            model.act("Repository linked", after = { url = ""; role = ""; branch = ""; linking = false }) {
                                it.request("POST", "/projects/${segment(slug)}/repos", json("url" to url.trim(), "role" to role.trim(), "branch" to branch.trim(), "path" to "", "note" to ""))
                            }
                        }) { Text("Link repository") }
                        TextButton(onClick = { linking = false }, enabled = !model.busy) { Text("Cancel") }
                    }
                }
            }
            items(repos, key = { it.text("id") }) { repo ->
                val (subtitle, body) = repoSummary(repo)
                SummaryCard(repo.text("repo"), subtitle, body, actions = {
                    ConfirmButton("Unlink", "Unlink ${repo.text("repo")}? Agents stop seeing it with this project. The repository itself is not touched.", !model.busy, danger = true) {
                        model.act("Repository unlinked") { it.request("DELETE", "/repos/${segment(repo.text("id"))}") }
                    }
                })
            }
        }
    }
}
