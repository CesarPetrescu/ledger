@file:OptIn(androidx.compose.material3.ExperimentalMaterial3Api::class)
package com.cesarpetrescu.ledger

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.BackHandler
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.saveable.rememberSaveableStateHolder
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.graphics.vector.PathParser
import androidx.compose.ui.graphics.vector.path
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.time.OffsetDateTime
import java.time.format.DateTimeFormatter

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        setContent {
            val dark = isSystemInDarkTheme()
            MaterialTheme(colorScheme = if (dark) darkColorScheme(primary = Color(0xFFACC7FF)) else lightColorScheme(
                primary = Color(0xFF1769E0), background = Color(0xFFF7F6F2), surface = Color(0xFFF7F6F2),
                onSurface = Color(0xFF172033), primaryContainer = Color(0xFFDCE8FF))) {
                LedgerApp()
            }
        }
    }
}

val LocalEditingEnabled = compositionLocalOf { true }

@Composable
fun LedgerApp(model: LedgerModel = viewModel()) {
    val snackbar = remember { SnackbarHostState() }
    LaunchedEffect(model.notice) { model.notice?.let { snackbar.showSnackbar(it); model.clearNotice() } }
    BackHandler(model.stack.size > 1 || model.busy) { model.back() }
    val focus = LocalFocusManager.current
    LaunchedEffect(model.busy) { if (model.busy) focus.clearFocus() }
    val session = model.api
    val route = model.route
    val title = when (route.substringBefore('/')) {
        "home" -> "Ledger"
        "projects", "project", "project-edit", "entry", "project-files" -> "Projects"
        "handoffs", "handoff", "handoff-new", "handoff-edit", "message-new" -> "Handoffs"
        "calendar", "event", "event-new", "calendar-settings" -> "Calendar"
        "search" -> "Search"
        "clients" -> "Connected clients"
        "device" -> "Approve a device"
        else -> "Settings"
    }
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
    val tabs = listOf("home" to "Home", "projects" to "Projects", "handoffs" to "Handoffs", "calendar" to "Calendar", "search" to "Search")
    Scaffold(
        topBar = {
            if (session != null) TopAppBar(title = { Text(title, fontWeight = FontWeight.Bold) }, navigationIcon = {
                if (model.stack.size > 1) IconButton(onClick = model::back, enabled = !model.busy) { Glyph("back", "Back") }
            }, actions = {
                IconButton(onClick = model::refresh, enabled = !model.busy) { Glyph("refresh", "Refresh") }
                IconButton(onClick = { model.go("settings") }, enabled = !model.busy) { Glyph("settings", "Settings") }
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
                            "home" -> Overview(model)
                            "projects" -> Projects(model)
                            "project" -> ProjectDetail(model, route.substringAfter('/'))
                            "project-edit" -> ProjectEditor(model, route.substringAfter('/', ""))
                            "entry" -> EntryEditor(model, route.substringAfter('/'))
                            "project-files" -> ProjectFiles(model, route.substringAfter('/'))
                            "handoffs" -> Handoffs(model)
                            "handoff" -> HandoffDetail(model, route.substringAfter('/'))
                            "handoff-new" -> HandoffEditor(model)
                            "handoff-edit" -> HandoffEditor(model, route.substringAfter('/'))
                            "message-new" -> MessageEditor(model, route.substringAfter('/'))
                            "calendar" -> CalendarScreen(model)
                            "event" -> EventEditor(model, route.substringAfter('/'))
                            "event-new" -> EventEditor(model)
                            "calendar-settings" -> CalendarSettings(model)
                            "search" -> SearchScreen(model)
                            "clients" -> Clients(model)
                            "device" -> Device(model)
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
        item { Spacer(Modifier.height(44.dp)); Text("L /", style = MaterialTheme.typography.displayLarge, color = MaterialTheme.colorScheme.primary) }
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

@Composable
fun Page(content: LazyListScope.() -> Unit) {
    LazyColumn(Modifier.fillMaxSize().testTag("page"), contentPadding = PaddingValues(20.dp), verticalArrangement = Arrangement.spacedBy(16.dp), content = content)
}

@Composable
fun Load(model: LedgerModel, key: String, fetch: (Api) -> JSONObject, content: @Composable (JSONObject) -> Unit) {
    val client = model.api ?: return
    var value by remember(key) { mutableStateOf<JSONObject?>(null) }
    var error by remember(key) { mutableStateOf<String?>(null) }
    var loading by remember(key) { mutableStateOf(true) }
    LaunchedEffect(key, client, model.revision) {
        loading = true
        error = null
        try { value = withContext(Dispatchers.IO) { fetch(client) } }
        catch (e: Exception) {
            if (e is CancellationException) throw e
            error = errorMessage(e)
            if (e is ApiError && e.status == 401) model.failed(e, client)
        } finally { loading = false }
    }
    Column(Modifier.fillMaxSize()) {
        if (loading) LinearProgressIndicator(Modifier.fillMaxWidth())
        error?.let { message ->
            Column(Modifier.padding(20.dp)) { Text(message, color = MaterialTheme.colorScheme.error); TextButton(onClick = model::refresh) { Text("Retry") } }
        }
        value?.let { content(it) }
    }
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

@Composable
fun SummaryCard(title: String, subtitle: String = "", body: String = "", onClick: (() -> Unit)? = null) {
    val contents: @Composable ColumnScope.() -> Unit = {
        Column(Modifier.padding(18.dp), verticalArrangement = Arrangement.spacedBy(7.dp)) {
            Text(title, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
            if (subtitle.isNotBlank()) Text(subtitle, style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.primary)
            if (body.isNotBlank()) SelectionContainer { Text(body, style = MaterialTheme.typography.bodyMedium) }
        }
    }
    if (onClick == null) OutlinedCard(Modifier.fillMaxWidth(), content = contents)
    else OutlinedCard(onClick = onClick, modifier = Modifier.fillMaxWidth(), content = contents)
}

@Composable
fun Empty(text: String) { Text(text, Modifier.padding(vertical = 24.dp), color = MaterialTheme.colorScheme.onSurfaceVariant) }

@Composable
fun ConfirmButton(label: String, explanation: String, enabled: Boolean = true, action: () -> Unit) {
    var confirm by remember { mutableStateOf(false) }
    OutlinedButton(onClick = { confirm = true }, enabled = enabled) { Text(label) }
    if (confirm) AlertDialog(onDismissRequest = { confirm = false }, title = { Text(label) }, text = { Text(explanation) },
        confirmButton = { TextButton(onClick = { confirm = false; action() }) { Text(label) } },
        dismissButton = { TextButton(onClick = { confirm = false }) { Text("Cancel") } })
}

fun displayTime(value: String): String = runCatching {
    OffsetDateTime.parse(value).atZoneSameInstant(java.time.ZoneId.systemDefault()).format(DateTimeFormatter.ofPattern("d MMM yyyy · HH:mm"))
}.getOrDefault(value)
fun label(value: String) = value.replace('_', ' ').replaceFirstChar { it.uppercase() }

@Composable
fun Glyph(name: String, description: String) {
    val data = when (name) {
        "home" -> "M10,20v-6h4v6h5v-8h3L12,3 2,12h3v8z"
        "projects" -> "M3,3h7v7H3zM14,3h7v7h-7zM3,14h7v7H3zM14,14h7v7h-7z"
        "handoffs" -> "M2,3h20v14H6l-4,4zM6,7v2h12V7zM6,11v2h8v-2z"
        "calendar" -> "M19,4h-1V2h-2v2H8V2H6v2H5c-1.1,0 -2,.9 -2,2v14c0,1.1 .9,2 2,2h14c1.1,0 2,-.9 2,-2V6c0,-1.1 -.9,-2 -2,-2zM19,20H5V9h14z"
        "search" -> "M9.5,3a6.5,6.5 0,1 0,3.9,11.7L20,21l1,-1 -6.3,-6.6A6.5,6.5 0,0 0,9.5,3zM9.5,5a4.5,4.5 0,1 1,0,9 4.5,4.5 0,0 1,0,-9z"
        "back" -> "M20,11H7.83l5.59,-5.59L12,4 4,12l8,8 1.41,-1.41L7.83,13H20z"
        "refresh" -> "M17.65,6.35A7.95,7.95 0,0 0,12,4a8,8 0,1 0,7.93,9h-2.02A6,6 0,1 1,12,6c1.66,0 3.14,.69 4.22,1.78L13,11h8V3z"
        else -> "M4,5h16v2H4zM4,11h16v2H4zM4,17h16v2H4zM8,3h2v6H8zM14,9h2v6h-2zM7,15h2v6H7z"
    }
    val vector = remember(data) { ImageVector.Builder(name, 24.dp, 24.dp, 24f, 24f).addPath(PathParser().parsePathString(data).toNodes(), fill = androidx.compose.ui.graphics.SolidColor(Color.Black)).build() }
    Icon(vector, contentDescription = description)
}
