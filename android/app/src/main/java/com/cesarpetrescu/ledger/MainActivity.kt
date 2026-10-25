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
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.saveable.rememberSaveableStateHolder
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import java.time.LocalDate
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
            LedgerTheme(dark) { LedgerApp(model, opened) }
        }
    }
}

val LocalEditingEnabled = compositionLocalOf { true }

/** The four tabs, in order. Everything else opens on top of one, or from the account sheet. */
val tabs = listOf("inbox" to "Today", "projects" to "Projects", "handoffs" to "Handoffs", "reading" to "Reading")

@Composable
fun LedgerApp(model: LedgerModel = viewModel(), opened: MutableState<Pair<String, String>?>? = null) {
    // A notification opens its entry (or Today) once the session is ready.
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
        val long = longNotice(message, undo != null)
        val result = snackbar.showSnackbar(message, actionLabel = undo?.let { "Undo" }, withDismissAction = long,
            duration = if (long) SnackbarDuration.Long else SnackbarDuration.Short)
        model.clearNotice()
        if (result == SnackbarResult.ActionPerformed && undo != null) model.undo(undo)
    }
    BackHandler(model.stack.size > 1 || model.busy) { model.back() }
    val focus = LocalFocusManager.current
    LaunchedEffect(model.busy) { if (model.busy) focus.clearFocus() }
    val session = model.api
    val route = model.route
    val passkeys = rememberPasskeys()
    var account by rememberSaveable { mutableStateOf(false) }
    if (account && session != null) AccountSheet(model) { account = false }
    if (model.reauthRequired && session != null) {
        var password by remember { mutableStateOf("") }
        AlertDialog(onDismissRequest = {}, title = { Text("Session expired") }, text = {
            Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                Text("Sign in again to keep working. Your open draft is preserved.")
                OutlinedTextField(password, { password = it }, label = { Text("Owner password") }, singleLine = true, shape = MaterialTheme.shapes.medium,
                    enabled = !model.busy, visualTransformation = PasswordVisualTransformation(),
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password))
                OutlinedButton(enabled = !model.busy, shape = ButtonShape, onClick = { model.passkeyLogin(session.origin, passkeys) }) { Text("Use a passkey instead") }
            }
        }, confirmButton = { TextButton(enabled = !model.busy && password.isNotBlank(), onClick = { model.login(session.origin, password, passkeys) }) { Text("Sign in again") } },
            dismissButton = { TextButton(enabled = !model.busy, onClick = model::forget) { Text("Discard draft and sign out") } })
    }
    val root = model.stack.size == 1
    Scaffold(
        topBar = {
            if (session != null && root) RootHeader(screenTitle(route), if (route == "inbox") todayLabel() else "", model) { account = true }
            else if (session != null) TopAppBar(title = { Text(screenTitle(route, model.projectNames), maxLines = 1, overflow = TextOverflow.Ellipsis) },
                navigationIcon = { IconButton(onClick = model::back, enabled = !model.busy) { Glyph("back", "Back") } })
        },
        bottomBar = {
            if (session != null && root) Column {
                HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                NavigationBar(containerColor = MaterialTheme.colorScheme.surfaceContainerLowest, tonalElevation = 0.dp) {
                    tabs.forEach { (key, label) -> NavigationBarItem(selected = route == key,
                        onClick = { model.tab(key) }, enabled = !model.busy,
                        icon = { Glyph(key, label) }, label = { Text(label, maxLines = 1) }) }
                }
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
                        val arg = java.net.URLDecoder.decode(route.substringAfter('/', ""), Charsets.UTF_8.name())
                        when (route.substringBefore('/')) {
                            "inbox" -> InboxScreen(model)
                            "projects" -> ProjectsHome(model)
                            "project" -> projectScreenArgs(route).let { (slug, tab, q) -> ProjectScreen(model, slug, tab, q) }
                            // A project's About tab opens these three on top of it.
                            "project-files" -> ProjectFiles(model, arg)
                            "project-repos" -> ProjectRepos(model, arg)
                            "project-handoffs" -> HandoffList(model, "archive=all&project=${segment(arg)}", empty = "No handoffs are linked to this project.", showProject = false)
                            "reading" -> ReadingScreen(model)
                            "todos" -> TodosScreen(model)
                            // Search took over the Table: an agent's "All of its activity" opens it filtered to that agent.
                            "search", "table" -> SearchScreen(model, arg)
                            "agents" -> AgentsScreen(model)
                            "help" -> HelpScreen()
                            "entry-view" -> EntryScreen(model, route.substringAfter('/'))
                            "history" -> HistoryScreen(model)
                            "trash" -> TrashScreen(model)
                            "project-edit" -> ProjectEditor(model, route.substringAfter('/', ""))
                            "entry" -> EntryEditor(model, route.substringAfter('/'))
                            "handoffs" -> Handoffs(model)
                            "handoff" -> HandoffDetail(model, route.substringAfter('/'))
                            "handoff-new" -> HandoffEditor(model)
                            "handoff-edit" -> HandoffEditor(model, route.substringAfter('/'))
                            "message-new" -> route.split('/').let { MessageEditor(model, it.getOrElse(1) { "" }, reply = it.getOrNull(2) == "reply") }
                            "calendar" -> CalendarScreen(model)
                            "event" -> EventEditor(model, route.substringAfter('/'))
                            "event-new" -> EventEditor(model)
                            "calendar-settings" -> CalendarSettings(model)
                            "clients" -> Clients(model)
                            "connect" -> ConnectScreen(model)
                            "api-keys" -> ApiKeys(model)
                            "device" -> Device(model)
                            "passkeys" -> PasskeysScreen(model)
                            "devices" -> SignedInDevices(model)
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

/** Today's date over the Today title, as "Sunday 25 October". */
fun todayLabel(today: LocalDate = LocalDate.now()): String = today.format(DateTimeFormatter.ofPattern("EEEE d MMMM"))

@Composable
fun Login(model: LedgerModel) {
    var origin by rememberSaveable { mutableStateOf("") }
    // The owner password is deliberately excluded from saved state and persistent storage.
    var password by remember { mutableStateOf("") }
    val passkeys = rememberPasskeys()
    Page {
        item {
            Spacer(Modifier.height(32.dp))
            // The same mark and wordmark as the web console's sign-in.
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                Image(painterResource(R.drawable.ledger_mark), contentDescription = null, modifier = Modifier.size(40.dp).testTag("ledger-logo"))
                Text("Ledger", style = MaterialTheme.typography.titleLarge)
            }
        }
        item { Text("Your work,\nwithin reach.", style = MaterialTheme.typography.displaySmall) }
        item { Text("Sign in to your Ledger owner console.", style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant) }
        item { Field("Server address", origin, { origin = it }, placeholder = "https://ledger.example.com", keyboard = KeyboardType.Uri) }
        item { OutlinedTextField(password, { password = it }, label = { Text("Owner password") }, singleLine = true, shape = MaterialTheme.shapes.medium,
            visualTransformation = PasswordVisualTransformation(), keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password),
            modifier = Modifier.fillMaxWidth(), enabled = !model.busy) }
        item { Button(onClick = { model.login(origin, password, passkeys) }, enabled = !model.busy && origin.isNotBlank() && password.isNotEmpty(),
            shape = ButtonShape, modifier = Modifier.fillMaxWidth().heightIn(min = 48.dp)) { Text("Sign in") } }
        // A passkey alone is enough: its provider's unlock is the second factor.
        // Enabled before an address is typed, so it is plain to see; tapping it then asks for the address.
        item { OutlinedButton(onClick = { model.passkeyLogin(origin, passkeys) }, enabled = !model.busy, shape = ButtonShape,
            modifier = Modifier.fillMaxWidth().heightIn(min = 48.dp)) { Text("Sign in with a passkey") } }
        item { Text("Use the same owner password as the web console; once you have added a passkey, it is asked for next. Your password stays off disk.",
            style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
    }
}

/** Whether a notice stays about 10 s with a close button instead of about 4 s: one with Undo, or one too long to read in 4 s (most errors, such as a missing passkey provider). */
fun longNotice(message: String, undoable: Boolean) = undoable || message.length > 60

/** The top bar's title. A project and its screens carry the project's name, once a list has loaded it. */
fun screenTitle(route: String, projectNames: Map<String, String> = emptyMap()): String {
    val parts = route.split('/').map { java.net.URLDecoder.decode(it, Charsets.UTF_8.name()) }
    val slug = parts.getOrElse(1) { "" }
    val project = projectNames[slug] ?: "Project"
    return when (parts.first()) {
        "inbox" -> "Today"
        "project", "project-repos", "project-files", "project-handoffs" -> project
        "project-edit" -> if (slug.isBlank()) "Projects" else "$project · Edit"
        "entry" -> "$project · New entry"
        "projects" -> "Projects"
        "reading" -> "Reading"
        "todos" -> "Todos"
        "handoffs", "handoff", "handoff-new", "handoff-edit", "message-new" -> "Handoffs"
        "calendar", "event", "event-new" -> "Calendar"
        "calendar-settings" -> "Calendars"
        "search", "table" -> "Search"
        "connect" -> "Connect an agent"
        "clients" -> "Connected apps"
        "api-keys" -> "API keys"
        "agents" -> "Agents"
        "help" -> "Help"
        "device" -> "Approve a device"
        "passkeys" -> "Passkeys"
        "devices" -> "Signed-in devices"
        "entry-view" -> "Entry"
        "history" -> "Recent actions"
        "trash" -> "Trash"
        else -> "Settings"
    }
}

fun displayTime(value: String): String = runCatching {
    OffsetDateTime.parse(value).atZoneSameInstant(java.time.ZoneId.systemDefault()).format(DateTimeFormatter.ofPattern("d MMM yyyy · HH:mm"))
}.getOrDefault(value)
