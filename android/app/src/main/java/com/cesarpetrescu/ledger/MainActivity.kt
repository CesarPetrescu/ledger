@file:OptIn(androidx.compose.material3.ExperimentalMaterial3Api::class)
package com.cesarpetrescu.ledger

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.BackHandler
import androidx.activity.compose.setContent
import androidx.activity.SystemBarStyle
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.Image
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.*
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.saveable.rememberSaveableStateHolder
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.PathFillType
import androidx.compose.ui.graphics.luminance
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.graphics.vector.PathParser
import androidx.compose.ui.graphics.vector.path
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.time.OffsetDateTime
import java.time.format.DateTimeFormatter

class MainActivity : ComponentActivity() {
    companion object {
        /** A screen to open, from a notification, and the server it belongs to. */
        const val ROUTE = "route"
        const val ORIGIN = "origin"
    }
    private val opened = mutableStateOf<Pair<String, String>?>(null)

    private fun requested(intent: android.content.Intent?): Pair<String, String>? {
        val route = intent?.getStringExtra(ROUTE) ?: return null
        return route to intent.getStringExtra(ORIGIN).orEmpty()
    }

    override fun onNewIntent(intent: android.content.Intent) {
        super.onNewIntent(intent)
        opened.value = requested(intent)
    }

    // A route not yet opened (the app was starting or busy) survives a rotation.
    override fun onSaveInstanceState(outState: Bundle) {
        super.onSaveInstanceState(outState)
        opened.value?.let { (route, origin) -> outState.putString(ROUTE, route); outState.putString(ORIGIN, origin) }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        opened.value = if (savedInstanceState == null) requested(intent)
            else savedInstanceState.getString(ROUTE)?.let { it to savedInstanceState.getString(ORIGIN).orEmpty() }
        enableEdgeToEdge()
        setContent {
            val model: LedgerModel = viewModel()
            val dark = when (model.theme) { "light" -> false; "dark" -> true; else -> isSystemInDarkTheme() }
            DisposableEffect(dark) {
                val bars = SystemBarStyle.auto(android.graphics.Color.TRANSPARENT, android.graphics.Color.TRANSPARENT) { dark }
                enableEdgeToEdge(bars, bars)
                onDispose {}
            }
            MaterialTheme(colorScheme = if (dark) DarkColors else LightColors) { LedgerApp(model, opened) }
        }
    }
}

// Matches the web console's palette.
private val LightColors = lightColorScheme(
    primary = Color(0xFF1769E0), background = Color(0xFFF7F6F2), surface = Color(0xFFF7F6F2),
    onSurface = Color(0xFF172033), primaryContainer = Color(0xFFDCE8FF))
private val DarkColors = darkColorScheme(
    primary = Color(0xFF74AAFF), onPrimary = Color(0xFF0B1220), primaryContainer = Color(0xFF1A2940), onPrimaryContainer = Color(0xFF93BDFF),
    secondaryContainer = Color(0xFF1A2940), onSecondaryContainer = Color(0xFFD5DCE6),
    background = Color(0xFF0F141B), onBackground = Color(0xFFE7ECF3), surface = Color(0xFF0F141B), onSurface = Color(0xFFE7ECF3),
    surfaceVariant = Color(0xFF1D2430), onSurfaceVariant = Color(0xFFA0ABBC), outline = Color(0xFF3A4556), outlineVariant = Color(0xFF283140),
    surfaceContainerLowest = Color(0xFF0B1016), surfaceContainerLow = Color(0xFF131922), surfaceContainer = Color(0xFF161C25),
    surfaceContainerHigh = Color(0xFF1D2430), surfaceContainerHighest = Color(0xFF242C39),
    error = Color(0xFFFF8A93), errorContainer = Color(0xFF3A1D22), onErrorContainer = Color(0xFFFFC2C7), tertiary = Color(0xFFF0A64A))

/** Whether the app is drawing its dark palette, for the few colors outside the scheme. */
@Composable
fun isDark() = MaterialTheme.colorScheme.background.luminance() < 0.5f

val LocalEditingEnabled = compositionLocalOf { true }

@Composable
fun LedgerApp(model: LedgerModel = viewModel(), opened: MutableState<Pair<String, String>?>? = null) {
    // A notification opens its entry (or the inbox) once the session is ready.
    val request = opened?.value
    LaunchedEffect(request, model.api, model.starting, model.busy) {
        // Navigation is ignored while an action runs; wait for it instead of dropping the request.
        if (request == null || model.starting || model.api == null || model.busy) return@LaunchedEffect
        opened.value = null
        val (requested, origin) = request
        // A notification from another server's session must not open this server's entry.
        if (origin != model.api?.origin || !Regex("inbox|entry-view/[0-9]+").matches(requested)) return@LaunchedEffect
        model.tab("inbox")
        if (requested != "inbox") model.go(requested)
    }
    val snackbar = remember { SnackbarHostState() }
    LaunchedEffect(model.notice, model.undoId) {
        val message = model.notice ?: return@LaunchedEffect
        val undo = model.undoId
        val result = snackbar.showSnackbar(message, actionLabel = undo?.let { "Undo" }, withDismissAction = undo != null,
            duration = if (undo != null) SnackbarDuration.Long else SnackbarDuration.Short)
        model.clearNotice()
        if (result == SnackbarResult.ActionPerformed && undo != null) model.undo(undo)
    }
    BackHandler(model.stack.size > 1 || model.busy) { model.back() }
    val focus = LocalFocusManager.current
    LaunchedEffect(model.busy) { if (model.busy) focus.clearFocus() }
    val session = model.api
    val route = model.route
    if (model.reauthRequired && session != null) {
        var password by remember { mutableStateOf("") }
        AlertDialog(onDismissRequest = {}, title = { Text("Session expired") }, text = {
            Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                Text("Sign in again to keep working. Your open draft is preserved.")
                OutlinedTextField(password, { password = it }, label = { Text("Owner password") }, singleLine = true,
                    enabled = !model.busy, visualTransformation = PasswordVisualTransformation(),
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password))
            }
        }, confirmButton = { TextButton(enabled = !model.busy && password.isNotBlank(), onClick = { model.login(session.origin, password) }) { Text("Sign in again") } },
            dismissButton = { TextButton(enabled = !model.busy, onClick = model::forget) { Text("Discard draft and sign out") } })
    }
    val tabs = listOf("inbox" to "Inbox", "projects" to "Projects", "reading" to "Reading", "handoffs" to "Handoffs", "more" to "More")
    Scaffold(
        topBar = {
            // Lists reload with a pull, so the bar keeps only search and settings.
            if (session != null) TopAppBar(title = { Text(screenTitle(route, model.projectNames), fontWeight = FontWeight.Bold, maxLines = 1, overflow = TextOverflow.Ellipsis) }, navigationIcon = {
                if (model.stack.size > 1) IconButton(onClick = model::back, enabled = !model.busy) { Glyph("back", "Back") }
            }, actions = {
                IconButton(onClick = { model.go("search") }, enabled = !model.busy && route != "search") { Glyph("search", "Search") }
                IconButton(onClick = { model.go("settings") }, enabled = !model.busy && route.substringBefore('/') != "settings") { Glyph("settings", "Settings") }
            })
        },
        bottomBar = {
            if (session != null && model.stack.size == 1) NavigationBar {
                tabs.forEach { (key, label) -> NavigationBarItem(selected = route == key,
                    onClick = { model.tab(key) }, enabled = !model.busy,
                    icon = { Glyph(key, label) }, label = { Text(label, maxLines = 1) }) }
            }
        }, snackbarHost = { SnackbarHost(snackbar) }
    ) { padding ->
        Box(Modifier.fillMaxSize().padding(padding).imePadding(), contentAlignment = Alignment.TopCenter) {
            Column(Modifier.widthIn(max = 900.dp).fillMaxSize()) {
                if (model.busy) LinearProgressIndicator(Modifier.fillMaxWidth())
                if (model.starting) Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) { CircularProgressIndicator() }
                else if (session == null) Login(model)
                else key(session.origin) {
                    val holder = rememberSaveableStateHolder()
                    var previousRoutes by remember { mutableStateOf(emptySet<String>()) }
                    LaunchedEffect(model.stack) {
                        (previousRoutes - model.stack.toSet()).forEach(holder::removeState)
                        previousRoutes = model.stack.toSet()
                    }
                    CompositionLocalProvider(LocalEditingEnabled provides !model.busy) {
                    holder.SaveableStateProvider(route) {
                        when (route.substringBefore('/')) {
                            "inbox" -> InboxScreen(model)
                            "projects" -> ProjectsHome(model)
                            "project" -> route.split('/').map { java.net.URLDecoder.decode(it, Charsets.UTF_8.name()) }.let { ProjectScreen(model, it.getOrElse(1) { "" }, it.getOrElse(2) { "activity" }, it.getOrElse(3) { "" }) }
                            "reading" -> ReadingScreen(model)
                            "todos" -> TodosScreen(model)
                            "more" -> MoreScreen(model)
                            "table" -> TableScreen(model, java.net.URLDecoder.decode(route.substringAfter('/', ""), Charsets.UTF_8.name()))
                            "agents" -> AgentsScreen(model)
                            "help" -> HelpScreen()
                            "entry-view" -> EntryScreen(model, route.substringAfter('/'))
                            "history" -> HistoryScreen(model)
                            "trash" -> TrashScreen(model)
                            "table-add" -> route.split('/').let { TableAdd(model, it.getOrElse(1) { "note" }, it.getOrElse(2) { "" }) }
                            "project-edit" -> ProjectEditor(model, route.substringAfter('/', ""))
                            "entry" -> EntryEditor(model, route.substringAfter('/'))
                            "project-files" -> ProjectFiles(model, route.substringAfter('/'))
                            "project-repos" -> ProjectRepos(model, route.substringAfter('/'))
                            "handoffs" -> Handoffs(model)
                            "handoff" -> HandoffDetail(model, route.substringAfter('/'))
                            "handoff-new" -> HandoffEditor(model)
                            "handoff-edit" -> HandoffEditor(model, route.substringAfter('/'))
                            "message-new" -> route.split('/').let { MessageEditor(model, it.getOrElse(1) { "" }, reply = it.getOrNull(2) == "reply") }
                            "calendar" -> CalendarScreen(model)
                            "event" -> EventEditor(model, route.substringAfter('/'))
                            "event-new" -> EventEditor(model)
                            "calendar-settings" -> CalendarSettings(model)
                            "search" -> SearchScreen(model)
                            "clients" -> Clients(model)
                            "connect" -> ConnectScreen(model)
                            "api-keys" -> ApiKeys(model)
                            "device" -> Device(model)
                            "settings" -> Settings(model, atAccess = route == "settings/access")
                            else -> Settings(model)
                        }
                    }
                    }
                }
            }
        }
    }
}

@Composable
fun Login(model: LedgerModel) {
    var origin by rememberSaveable { mutableStateOf("") }
    // The owner password is deliberately excluded from saved state and persistent storage.
    var password by remember { mutableStateOf("") }
    Page {
        item {
            Spacer(Modifier.height(44.dp))
            // The same mark and wordmark as the web console's sign-in.
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                Image(painterResource(R.drawable.ledger_mark), contentDescription = null, modifier = Modifier.size(40.dp).testTag("ledger-logo"))
                Text("Ledger", style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold)
            }
        }
        item { Text("Your work,\nwithin reach.", style = MaterialTheme.typography.headlineLarge, fontWeight = FontWeight.Bold) }
        item { Text("Sign in to your Ledger owner console.", color = MaterialTheme.colorScheme.onSurfaceVariant) }
        item { Field("Server address", origin, { origin = it }, placeholder = "https://ledger.example.com", keyboard = KeyboardType.Uri) }
        item { OutlinedTextField(password, { password = it }, label = { Text("Owner password") }, singleLine = true,
            visualTransformation = PasswordVisualTransformation(), keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password),
            modifier = Modifier.fillMaxWidth(), enabled = !model.busy) }
        item { Button(onClick = { model.login(origin, password) }, enabled = !model.busy && origin.isNotBlank() && password.isNotEmpty(), modifier = Modifier.fillMaxWidth()) { Text("Sign in") } }
        item { Text("Use the same owner password as the web console. Your password stays off disk.", style = MaterialTheme.typography.bodySmall) }
    }
}

/** The top bar's title. A project and its screens carry the project's name, once a list has loaded it. */
fun screenTitle(route: String, projectNames: Map<String, String> = emptyMap()): String {
    val parts = route.split('/').map { java.net.URLDecoder.decode(it, Charsets.UTF_8.name()) }
    val slug = parts.getOrElse(1) { "" }
    val project = projectNames[slug] ?: "Project"
    return when (parts.first()) {
        "inbox" -> "Inbox"
        "project" -> project
        "project-repos" -> "$project · Repos"
        "project-files" -> "$project · Files"
        "project-edit" -> if (slug.isBlank()) "Projects" else "$project · Edit"
        "entry" -> "$project · New entry"
        "projects", "table-add" -> "Projects"
        "reading" -> "Reading"
        "todos" -> "Todos"
        "handoffs", "handoff", "handoff-new", "handoff-edit", "message-new" -> "Handoffs"
        "calendar", "event", "event-new" -> "Calendar"
        "calendar-settings" -> "Calendars"
        "search" -> "Search"
        "connect" -> "Connect an agent"
        "clients" -> "Connected apps"
        "api-keys" -> "API keys"
        "agents" -> "Agents"
        "help" -> "Help"
        "device" -> "Approve a device"
        "more" -> "More"
        "table" -> "Table"
        "entry-view" -> "Entry"
        "history" -> "Recent actions"
        "trash" -> "Trash"
        else -> "Settings"
    }
}

@Composable
fun Page(state: LazyListState = rememberLazyListState(), content: LazyListScope.() -> Unit) {
    LazyColumn(Modifier.fillMaxSize().testTag("page"), state = state, contentPadding = PaddingValues(20.dp), verticalArrangement = Arrangement.spacedBy(16.dp), content = content)
}

/** Fetches [key] and shows [content], filling the page, where a pull reloads it; inside a dialog, [wrap] sizes it to its content instead. */
@Composable
fun Load(model: LedgerModel, key: String, fetch: (Api) -> JSONObject, wrap: Boolean = false, content: @Composable (JSONObject) -> Unit) {
    val client = model.api ?: return
    var value by remember(key) { mutableStateOf<JSONObject?>(null) }
    var error by remember(key) { mutableStateOf<String?>(null) }
    var loading by remember(key) { mutableStateOf(true) }
    // Only a pull shows the pull spinner; a reload after an action keeps the thin bar.
    var pulled by remember(key) { mutableStateOf(false) }
    LaunchedEffect(key, client, model.revision) {
        loading = true
        error = null
        try { value = withContext(Dispatchers.IO) { fetch(client) } }
        catch (e: Exception) {
            if (e is CancellationException) throw e
            error = errorMessage(e)
            if (e is ApiError && e.status == 401) model.failed(e, client)
        } finally { loading = false }
        pulled = false
    }
    val body: @Composable () -> Unit = {
        Column(if (wrap) Modifier else Modifier.fillMaxSize()) {
            if (loading && !pulled) LinearProgressIndicator(Modifier.fillMaxWidth())
            error?.let { message ->
                Column(Modifier.padding(20.dp)) { Text(message, color = MaterialTheme.colorScheme.error); TextButton(onClick = model::refresh) { Text("Retry") } }
            }
            value?.let { content(it) }
        }
    }
    if (wrap) body()
    else PullToRefreshBox(isRefreshing = pulled, onRefresh = { pulled = true; model.refresh() }, modifier = Modifier.fillMaxSize()) { body() }
}

@Composable
fun Field(label: String, value: String, onChange: (String) -> Unit, multiline: Boolean = false, max: Int = 4000,
          placeholder: String = "", keyboard: KeyboardType = KeyboardType.Text, enabled: Boolean = true) {
    OutlinedTextField(value, { if (validFieldText(it, max, multiline)) onChange(it) }, label = { Text(label) },
        placeholder = { Text(placeholder) }, singleLine = !multiline, minLines = if (multiline) 3 else 1,
        keyboardOptions = KeyboardOptions(keyboardType = keyboard), modifier = Modifier.fillMaxWidth(), enabled = enabled && LocalEditingEnabled.current)
}

@Composable
fun Choice(label: String, value: String, options: List<Pair<String, String>>, change: (String) -> Unit) {
    var expanded by remember { mutableStateOf(false) }
    val enabled = LocalEditingEnabled.current
    ExposedDropdownMenuBox(expanded, { if (enabled) expanded = it }) {
        OutlinedTextField(options.firstOrNull { it.first == value }?.second ?: value, {}, readOnly = true,
            enabled = enabled, label = { Text(label) }, trailingIcon = { ExposedDropdownMenuDefaults.TrailingIcon(expanded) },
            modifier = Modifier.fillMaxWidth().menuAnchor(ExposedDropdownMenuAnchorType.PrimaryNotEditable))
        ExposedDropdownMenu(expanded, { expanded = false }) {
            options.forEach { (id, name) -> DropdownMenuItem(text = { Text(name) }, onClick = { change(id); expanded = false }) }
        }
    }
}

/** A card; one that opens something shows a chevron, and its [actions] sit inside it, under the text. */
@Composable
fun SummaryCard(title: String, subtitle: String = "", body: String = "", tags: List<Pair<String, Tone>> = emptyList(),
                actions: (@Composable RowScope.() -> Unit)? = null, onClick: (() -> Unit)? = null) {
    val muted = MaterialTheme.colorScheme.onSurfaceVariant
    val contents: @Composable ColumnScope.() -> Unit = {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f).padding(18.dp), verticalArrangement = Arrangement.spacedBy(7.dp)) {
                Text(title, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
                // Metadata is muted; blue is kept for what can be tapped.
                if (subtitle.isNotBlank()) Text(subtitle, style = MaterialTheme.typography.labelMedium, color = muted)
                if (tags.isNotEmpty()) Row(horizontalArrangement = Arrangement.spacedBy(4.dp)) { tags.forEach { (text, tone) -> Tag(text, tone) } }
                if (body.isNotBlank()) SelectionContainer { Text(body, style = MaterialTheme.typography.bodyMedium) }
                if (actions != null) Row(Modifier.padding(top = 4.dp), horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically, content = actions)
            }
            if (onClick != null) Box(Modifier.padding(end = 12.dp)) { Glyph("chevron", null, tint = muted) }
        }
    }
    if (onClick == null) OutlinedCard(Modifier.fillMaxWidth(), content = contents)
    else OutlinedCard(onClick = onClick, modifier = Modifier.fillMaxWidth(), content = contents)
}

@Composable
fun Empty(text: String) { Text(text, Modifier.padding(vertical = 24.dp), color = MaterialTheme.colorScheme.onSurfaceVariant) }

@Composable
fun ConfirmButton(label: String, explanation: String, enabled: Boolean = true, danger: Boolean = false, action: () -> Unit) {
    var confirm by remember { mutableStateOf(false) }
    // A dangerous action reads as one before it is tapped, not only in its dialog.
    OutlinedButton(onClick = { confirm = true }, enabled = enabled,
        colors = if (danger) ButtonDefaults.outlinedButtonColors(contentColor = MaterialTheme.colorScheme.error) else ButtonDefaults.outlinedButtonColors()) { Text(label) }
    if (confirm) ConfirmDialog(label, explanation, { confirm = false }, danger, action)
}

/** Asks before [action]; [label] names the dialog and its confirm button, in the error colour when the action is [danger]ous. */
@Composable
fun ConfirmDialog(label: String, explanation: String, dismiss: () -> Unit, danger: Boolean = false, action: () -> Unit) =
    AlertDialog(onDismissRequest = dismiss, title = { Text(label) }, text = { Text(explanation) },
        confirmButton = { TextButton(onClick = { dismiss(); action() }) { Text(label, color = if (danger) MaterialTheme.colorScheme.error else Color.Unspecified) } },
        dismissButton = { TextButton(onClick = dismiss) { Text("Cancel") } })

class MenuAction(val label: String, val danger: Boolean = false, val run: () -> Unit)

/** Secondary and destructive actions behind a ⋯ button, so a row keeps only what is used most. */
@Composable
fun Overflow(description: String, actions: List<MenuAction>, enabled: Boolean = true) {
    var open by remember { mutableStateOf(false) }
    Box {
        IconButton(onClick = { open = true }, enabled = enabled) { Glyph("more", description) }
        DropdownMenu(open, onDismissRequest = { open = false }) {
            actions.forEach { action ->
                DropdownMenuItem(text = { Text(action.label, color = if (action.danger) MaterialTheme.colorScheme.error else Color.Unspecified) },
                    onClick = { open = false; action.run() })
            }
        }
    }
}

fun displayTime(value: String): String = runCatching {
    OffsetDateTime.parse(value).atZoneSameInstant(java.time.ZoneId.systemDefault()).format(DateTimeFormatter.ofPattern("d MMM yyyy · HH:mm"))
}.getOrDefault(value)
fun label(value: String) = value.replace('_', ' ').replaceFirstChar { it.uppercase() }

@Composable
fun Glyph(name: String, description: String?, tint: Color = LocalContentColor.current) {
    val data = when (name) {
        "inbox" -> "M19,3H4.99c-1.11,0 -1.98,.89 -1.98,2L3,19c0,1.1 .88,2 1.99,2H19c1.1,0 2,-.9 2,-2V5c0,-1.11 -.9,-2 -2,-2zM19,15h-4c0,1.66 -1.35,3 -3,3s-3,-1.34 -3,-3H4.99V5H19v10z"
        "reading" -> "M19,3H5c-1.1,0 -2,.9 -2,2v14c0,1.1 .9,2 2,2h14c1.1,0 2,-.9 2,-2V5c0,-1.1 -.9,-2 -2,-2zM14,17H7v-2h7v2zM17,13H7v-2h10v2zM17,9H7V7h10v2z"
        "more" -> "M6,10c-1.1,0 -2,.9 -2,2s.9,2 2,2 2,-.9 2,-2 -.9,-2 -2,-2zM18,10c-1.1,0 -2,.9 -2,2s.9,2 2,2 2,-.9 2,-2 -.9,-2 -2,-2zM12,10c-1.1,0 -2,.9 -2,2s.9,2 2,2 2,-.9 2,-2 -.9,-2 -2,-2z"
        "projects" -> "M3,3h7v7H3zM14,3h7v7h-7zM3,14h7v7H3zM14,14h7v7h-7z"
        "handoffs" -> "M2,3h20v14H6l-4,4zM6,7v2h12V7zM6,11v2h8v-2z"
        "calendar" -> "M19,4h-1V2h-2v2H8V2H6v2H5c-1.1,0 -2,.9 -2,2v14c0,1.1 .9,2 2,2h14c1.1,0 2,-.9 2,-2V6c0,-1.1 -.9,-2 -2,-2zM19,20H5V9h14z"
        "search" -> "M9.5,3a6.5,6.5 0,1 0,3.9,11.7L20,21l1,-1 -6.3,-6.6A6.5,6.5 0,0 0,9.5,3zM9.5,5a4.5,4.5 0,1 1,0,9 4.5,4.5 0,0 1,0,-9z"
        "filter" -> "M10,18h4v-2h-4v2zM3,6v2h18V6H3zM6,13h12v-2H6v2z"
        "back" -> "M20,11H7.83l5.59,-5.59L12,4 4,12l8,8 1.41,-1.41L7.83,13H20z"
        "chevron" -> "M10,6L8.59,7.41 13.17,12l-4.58,4.59L10,18l6,-6z"
        // Settings: a gear; the old sliders read as filters.
        else -> "M19.14,12.94c.04,-.3 .06,-.61 .06,-.94 0,-.32 -.02,-.64 -.07,-.94l2.03,-1.58c.18,-.14 .23,-.41 .12,-.61l-1.92,-3.32c-.12,-.22 -.37,-.29 -.59,-.22l-2.39,.96c-.5,-.38 -1.03,-.7 -1.62,-.94L14.4,2.81c-.04,-.24 -.24,-.41 -.48,-.41h-3.84c-.24,0 -.43,.17 -.47,.41L9.25,5.35C8.66,5.59 8.12,5.92 7.63,6.29L5.24,5.33c-.22,-.08 -.47,0 -.59,.22L2.74,8.87C2.62,9.08 2.66,9.34 2.86,9.48l2.03,1.58C4.84,11.36 4.8,11.69 4.8,12s.02,.64 .07,.94l-2.03,1.58c-.18,.14 -.23,.41 -.12,.61l1.92,3.32c.12,.22 .37,.29 .59,.22l2.39,-.96c.5,.38 1.03,.7 1.62,.94l.36,2.54c.05,.24 .24,.41 .48,.41h3.84c.24,0 .44,-.17 .47,-.41l.36,-2.54c.59,-.24 1.13,-.56 1.62,-.94l2.39,.96c.22,.08 .47,0 .59,-.22l1.92,-3.32c.12,-.22 .07,-.47 -.12,-.61L19.14,12.94zM12,15.6c-1.98,0 -3.6,-1.62 -3.6,-3.6s1.62,-3.6 3.6,-3.6 3.6,1.62 3.6,3.6 -1.62,3.6 -3.6,3.6z"
    }
    // Even-odd keeps the gear's centre open whichever way its two paths wind.
    val fillType = if (name == "settings") PathFillType.EvenOdd else PathFillType.NonZero
    val vector = remember(data) { ImageVector.Builder(name, 24.dp, 24.dp, 24f, 24f).addPath(PathParser().parsePathString(data).toNodes(), fillType, fill = androidx.compose.ui.graphics.SolidColor(Color.Black)).build() }
    Icon(vector, contentDescription = description, tint = tint)
}
