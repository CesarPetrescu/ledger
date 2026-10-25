@file:OptIn(androidx.compose.material3.ExperimentalMaterial3Api::class, androidx.compose.foundation.layout.ExperimentalLayoutApi::class)
package com.cesarpetrescu.ledger

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.*
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.rotate
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.PathFillType
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.graphics.vector.PathParser
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.text
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import org.json.JSONObject

// The app's building blocks. Screens compose these instead of styling Material components themselves.

/** Space between the items of a [Page]. */
val PageSpacing = 16.dp

@Composable
fun Page(state: LazyListState = rememberLazyListState(), content: LazyListScope.() -> Unit) {
    LazyColumn(Modifier.fillMaxSize().testTag("page"), state = state, contentPadding = PaddingValues(Gutter),
        verticalArrangement = Arrangement.spacedBy(PageSpacing), content = content)
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
                Column(Modifier.padding(Gutter)) { Text(message, color = MaterialTheme.colorScheme.error); TextButton(onClick = model::refresh) { Text("Retry") } }
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
        placeholder = { Text(placeholder) }, singleLine = !multiline, minLines = if (multiline) 3 else 1, shape = MaterialTheme.shapes.medium,
        keyboardOptions = KeyboardOptions(keyboardType = keyboard), modifier = Modifier.fillMaxWidth(), enabled = enabled && LocalEditingEnabled.current)
}

@Composable
fun Choice(label: String, value: String, options: List<Pair<String, String>>, change: (String) -> Unit) {
    var expanded by remember { mutableStateOf(false) }
    val enabled = LocalEditingEnabled.current
    ExposedDropdownMenuBox(expanded, { if (enabled) expanded = it }) {
        OutlinedTextField(options.firstOrNull { it.first == value }?.second ?: value, {}, readOnly = true, shape = MaterialTheme.shapes.medium,
            enabled = enabled, label = { Text(label) }, trailingIcon = { ExposedDropdownMenuDefaults.TrailingIcon(expanded) },
            modifier = Modifier.fillMaxWidth().menuAnchor(ExposedDropdownMenuAnchorType.PrimaryNotEditable))
        ExposedDropdownMenu(expanded, { expanded = false }) {
            options.forEach { (id, name) -> DropdownMenuItem(text = { Text(name) }, onClick = { change(id); expanded = false }) }
        }
    }
}

/** A chip that names a filter and opens its choices; it reads as selected once a choice is made. */
@Composable
fun MenuChip(label: String, value: String, options: List<Pair<String, String>>, change: (String) -> Unit) {
    var open by remember { mutableStateOf(false) }
    Box {
        FilterChip(selected = value.isNotBlank(), onClick = { open = true }, enabled = LocalEditingEnabled.current, shape = MaterialTheme.shapes.small,
            label = { Text(options.firstOrNull { it.first == value && value.isNotBlank() }?.second ?: label, maxLines = 1) },
            trailingIcon = { Glyph("expand", null) })
        DropdownMenu(open, { open = false }) {
            options.forEach { (id, name) -> DropdownMenuItem(text = { Text(name) }, onClick = { change(id); open = false }) }
        }
    }
}

/** A white panel on the paper ground; rows inside it are separated by [GroupDivider]. */
@Composable
fun Group(modifier: Modifier = Modifier, content: @Composable ColumnScope.() -> Unit) {
    Surface(modifier.fillMaxWidth(), shape = MaterialTheme.shapes.medium, color = MaterialTheme.colorScheme.surfaceContainerLowest,
        border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant)) { Column(content = content) }
}

@Composable
fun GroupDivider() = HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)

/** A row inside a [Group] that opens another screen. */
@Composable
fun NavRow(title: String, subtitle: String = "", icon: String? = null, onClick: () -> Unit) {
    Row(Modifier.fillMaxWidth().clickable(enabled = LocalEditingEnabled.current, onClick = onClick).padding(horizontal = Gutter, vertical = 14.dp),
        verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(14.dp)) {
        icon?.let { Glyph(it, null, tint = MaterialTheme.colorScheme.secondary) }
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Text(title, style = MaterialTheme.typography.titleSmall)
            if (subtitle.isNotBlank()) Text(subtitle, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        Glyph("chevron", null, tint = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

/** A small caps heading over a list, with an optional count and an action on the right; [inset] is 0 inside a padded [Page]. */
@Composable
fun SectionLabel(text: String, modifier: Modifier = Modifier, count: Int? = null, countTone: Tone = Tone.Neutral, inset: Dp = Gutter,
                 action: (@Composable () -> Unit)? = null) {
    Row(modifier.fillMaxWidth().heightIn(min = 40.dp).padding(start = inset, end = 4.dp, top = 12.dp), verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        // Drawn in capitals, read (and found) as written.
        Text(text.uppercase(), Modifier.clearAndSetSemantics { this.text = AnnotatedString(text); heading() }, color = MaterialTheme.colorScheme.secondary,
            style = MaterialTheme.typography.labelMedium.copy(fontWeight = FontWeight.SemiBold, letterSpacing = 0.08.em))
        count?.let { CountBadge(it, countTone) }
        Spacer(Modifier.weight(1f))
        action?.invoke()
    }
}

@Composable
fun CountBadge(count: Int, tone: Tone = Tone.Neutral) {
    val (background, foreground) = ledgerColors.tone(tone)
    Box(Modifier.heightIn(min = 20.dp).widthIn(min = 20.dp).clip(RoundedCornerShape(10.dp)).background(background).padding(horizontal = 6.dp),
        contentAlignment = Alignment.Center) {
        Text("$count", color = foreground, style = MaterialTheme.typography.labelMedium.copy(fontWeight = FontWeight.SemiBold))
    }
}

/** A tone's background and text colours, for labels and counts. */
@Composable
fun toneColors(tone: Tone): Pair<Color, Color> = ledgerColors.tone(tone)

@Composable
fun Tag(text: String, tone: Tone) {
    val (background, foreground) = toneColors(tone)
    Surface(color = background, contentColor = foreground, shape = MaterialTheme.shapes.extraSmall) {
        Text(text, Modifier.padding(horizontal = 7.dp, vertical = 3.dp), style = MaterialTheme.typography.labelSmall, maxLines = 1)
    }
}

/** Initials for an agent: "claude-code" is CC, "codex" is CO; the owner is You. */
fun agentInitials(source: String): String {
    if (source == OWNER_SOURCE) return "You"
    val words = source.split('-', '_', ' ', '.').filter { it.isNotBlank() }
    return when {
        words.size > 1 -> "${words[0].first()}${words[1].first()}"
        else -> source.take(2)
    }.uppercase()
}

/** One colour per agent, the same everywhere: Claude purple, Codex green, ChatGPT blue, the rest by name. */
fun agentTone(source: String): Tone = when {
    source.startsWith("claude") -> Tone.Research
    source.startsWith("codex") -> Tone.Good
    source.startsWith("chatgpt") || source.startsWith("gpt") -> Tone.Accent
    else -> listOf(Tone.Warn, Tone.Neutral, Tone.Accent, Tone.Good, Tone.Research)[Math.floorMod(source.hashCode(), 5)]
}

/** An agent's monogram, so a glance tells who wrote what. */
@Composable
fun AgentMark(source: String, size: Dp = 22.dp) {
    val owner = source == OWNER_SOURCE
    val (background, foreground) = if (owner) MaterialTheme.colorScheme.onSurface to MaterialTheme.colorScheme.surface else toneColors(agentTone(source))
    Box(Modifier.size(size).clip(RoundedCornerShape(size / 4)).background(background), contentAlignment = Alignment.Center) {
        Text(agentInitials(source), color = foreground, maxLines = 1,
            style = MaterialTheme.typography.labelMedium.copy(fontWeight = FontWeight.SemiBold, fontSize = if (owner) 9.sp else 10.sp, lineHeight = 12.sp))
    }
}

/** An entry's kind at a glance: amber dot asks you, blue diamond decision, ring todo, check status, dash note. */
@Composable
fun KindMark(entry: JSONObject, done: Boolean = false) {
    val muted = MaterialTheme.colorScheme.onSurfaceVariant
    Box(Modifier.size(20.dp), contentAlignment = Alignment.Center) {
        when {
            asksYou(entry) -> Box(Modifier.size(10.dp).clip(CircleShape).background(toneColors(Tone.Warn).second))
            entry.text("kind") == "decision" -> Box(Modifier.size(9.dp).rotate(45f).background(MaterialTheme.colorScheme.primary))
            entry.text("kind") == "todo" && (done || entry.optJSONObject("resolved_by") != null) -> Glyph("check", null, tint = toneColors(Tone.Good).second, size = 16.dp)
            entry.text("kind") == "todo" -> Box(Modifier.size(14.dp).border(2.dp, muted, CircleShape))
            entry.text("kind") == "status" && entry.optJSONObject("meta")?.text("state") == "blocked" ->
                Box(Modifier.size(10.dp).clip(RoundedCornerShape(2.dp)).background(toneColors(Tone.Danger).second))
            entry.text("kind") == "status" -> Glyph("check", null, tint = toneColors(Tone.Good).second, size = 16.dp)
            else -> Box(Modifier.size(width = 10.dp, height = 2.dp).clip(RoundedCornerShape(1.dp)).background(muted))
        }
    }
}

/** A card; one that opens something shows a chevron, and its [actions] sit inside it, under the text. */
@Composable
fun SummaryCard(title: String, subtitle: String = "", body: String = "", tags: List<Pair<String, Tone>> = emptyList(),
                actions: (@Composable RowScope.() -> Unit)? = null, onClick: (() -> Unit)? = null) {
    val muted = MaterialTheme.colorScheme.onSurfaceVariant
    Group(if (onClick == null) Modifier else Modifier.clip(MaterialTheme.shapes.medium).clickable(enabled = LocalEditingEnabled.current, onClick = onClick)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f).padding(Gutter), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                Text(title, style = MaterialTheme.typography.titleMedium)
                // Metadata is muted; blue is kept for what can be tapped.
                if (subtitle.isNotBlank()) Text(subtitle, style = MaterialTheme.typography.bodySmall, color = muted)
                if (tags.isNotEmpty()) FlowRow(horizontalArrangement = Arrangement.spacedBy(4.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) { tags.forEach { (text, tone) -> Tag(text, tone) } }
                if (body.isNotBlank()) SelectionContainer { Text(body, style = MaterialTheme.typography.bodyMedium, color = ledgerColors.body) }
                if (actions != null) FlowRow(Modifier.padding(top = 4.dp), horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) { actions() }
            }
            if (onClick != null) Box(Modifier.padding(end = 12.dp)) { Glyph("chevron", null, tint = muted) }
        }
    }
}

@Composable
fun Empty(text: String) { Text(text, Modifier.padding(vertical = 24.dp), style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant) }

/** The screen's one leading action, bottom right. */
@Composable
fun LedgerFab(text: String, modifier: Modifier = Modifier, onClick: () -> Unit) {
    val enabled = LocalEditingEnabled.current
    // A FAB has no disabled state, so while an action runs it ignores taps instead.
    // The slot form: the icon-and-text form hides its text from TalkBack (and from tests).
    ExtendedFloatingActionButton(onClick = { if (enabled) onClick() }, modifier = modifier.padding(Gutter), shape = MaterialTheme.shapes.large,
        containerColor = MaterialTheme.colorScheme.primary, contentColor = MaterialTheme.colorScheme.onPrimary) {
        Glyph("add", null)
        Spacer(Modifier.width(10.dp))
        Text(text, style = MaterialTheme.typography.labelLarge)
    }
}

@Composable
fun ConfirmButton(label: String, explanation: String, enabled: Boolean = true, danger: Boolean = false, action: () -> Unit) {
    var confirm by remember { mutableStateOf(false) }
    // A dangerous action reads as one before it is tapped, not only in its dialog.
    OutlinedButton(onClick = { confirm = true }, enabled = enabled, shape = ButtonShape,
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

/** Secondary and destructive actions behind a ⋮ button, so a row keeps only what is used most. */
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

/** The title row of the four main screens: a large title, Search, and the account sheet. */
@Composable
fun RootHeader(title: String, subtitle: String, model: LedgerModel, account: () -> Unit) {
    Row(Modifier.fillMaxWidth().statusBarsPadding().padding(start = Gutter, end = 4.dp, top = 10.dp, bottom = 6.dp), verticalAlignment = Alignment.CenterVertically) {
        Column(Modifier.weight(1f)) {
            if (subtitle.isNotBlank()) Text(subtitle, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Text(title, style = MaterialTheme.typography.displaySmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
        IconButton(onClick = { model.go("search") }, enabled = !model.busy) { Glyph("search", "Search") }
        IconButton(onClick = account, enabled = !model.busy) { Glyph("account", "Account", size = 28.dp) }
    }
}

/** Everything that is not one of the four tabs, one tap from any of them. */
@Composable
fun AccountSheet(model: LedgerModel, dismiss: () -> Unit) {
    val open: (String) -> Unit = { route -> dismiss(); model.go(route) }
    ModalBottomSheet(onDismissRequest = dismiss) {
        Column(Modifier.padding(start = Gutter, end = Gutter, bottom = 24.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
                Text("Ledger ${BuildConfig.VERSION_NAME}", style = MaterialTheme.typography.titleMedium)
                Text(model.api?.origin.orEmpty(), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            Group {
                NavRow("Calendar", "Your Nextcloud events, due todos and deadlines", "calendar") { open("calendar") }
                GroupDivider()
                NavRow("Agents", "What each agent did lately", "agents") { open("agents") }
                GroupDivider()
                NavRow("Recent actions", "Undo what you marked, snoozed, or deleted", "history") { open("history") }
                GroupDivider()
                NavRow("Trash", "Restore deleted projects and entries", "trash") { open("trash") }
            }
            Group {
                NavRow("Settings", "Access, appearance, notifications, and sign out", "settings") { open("settings") }
                GroupDivider()
                NavRow("Help", "How Ledger works and what the labels mean", "help") { open("help") }
            }
        }
    }
}

fun label(value: String) = value.replace('_', ' ').replaceFirstChar { it.uppercase() }

// Material icon outlines, drawn from their path data so the app needs no icon library.
private val glyphs = mapOf(
    "inbox" to "M19,3H4.99c-1.11,0 -1.98,.89 -1.98,2L3,19c0,1.1 .88,2 1.99,2H19c1.1,0 2,-.9 2,-2V5c0,-1.11 -.9,-2 -2,-2zM19,15h-4c0,1.66 -1.35,3 -3,3s-3,-1.34 -3,-3H4.99V5H19v10z",
    "reading" to "M19,3H5c-1.1,0 -2,.9 -2,2v14c0,1.1 .9,2 2,2h14c1.1,0 2,-.9 2,-2V5c0,-1.1 -.9,-2 -2,-2zM14,17H7v-2h7v2zM17,13H7v-2h10v2zM17,9H7V7h10v2z",
    "projects" to "M3,3h7v7H3zM14,3h7v7h-7zM3,14h7v7H3zM14,14h7v7h-7z",
    "handoffs" to "M6.99,11L3,15l3.99,4v-3H14v-2H6.99v-3zM21,9l-3.99,-4v3H10v2h7.01v3L21,9z",
    "more" to "M12,8c1.1,0 2,-0.9 2,-2s-0.9,-2 -2,-2 -2,0.9 -2,2 0.9,2 2,2zM12,10c-1.1,0 -2,0.9 -2,2s0.9,2 2,2 2,-0.9 2,-2 -0.9,-2 -2,-2zM12,16c-1.1,0 -2,0.9 -2,2s0.9,2 2,2 2,-0.9 2,-2 -0.9,-2 -2,-2z",
    "calendar" to "M19,4h-1V2h-2v2H8V2H6v2H5c-1.1,0 -2,.9 -2,2v14c0,1.1 .9,2 2,2h14c1.1,0 2,-.9 2,-2V6c0,-1.1 -.9,-2 -2,-2zM19,20H5V9h14z",
    "search" to "M9.5,3a6.5,6.5 0,1 0,3.9,11.7L20,21l1,-1 -6.3,-6.6A6.5,6.5 0,0 0,9.5,3zM9.5,5a4.5,4.5 0,1 1,0,9 4.5,4.5 0,0 1,0,-9z",
    "filter" to "M10,18h4v-2h-4v2zM3,6v2h18V6H3zM6,13h12v-2H6v2z",
    "back" to "M20,11H7.83l5.59,-5.59L12,4 4,12l8,8 1.41,-1.41L7.83,13H20z",
    "chevron" to "M10,6L8.59,7.41 13.17,12l-4.58,4.59L10,18l6,-6z",
    "close" to "M19,6.41L17.59,5 12,10.59 6.41,5 5,6.41 10.59,12 5,17.59 6.41,19 12,13.41 17.59,19 19,17.59 13.41,12z",
    "expand" to "M16.59,8.59L12,13.17 7.41,8.59 6,10l6,6 6,-6z",
    "check" to "M9,16.17L4.83,12l-1.42,1.41L9,19 21,7l-1.41,-1.41z",
    "snooze" to "M11.99,2C6.47,2 2,6.48 2,12s4.47,10 9.99,10C17.52,22 22,17.52 22,12S17.52,2 11.99,2zM12,20c-4.42,0 -8,-3.58 -8,-8s3.58,-8 8,-8 8,3.58 8,8 -3.58,8 -8,8zM12.5,7H11v6l5.25,3.15 0.75,-1.23 -4.5,-2.67z",
    "reply" to "M10,9V5l-7,7 7,7v-4.1c5,0 8.5,1.6 11,5.1 -1,-5 -4,-10 -11,-11z",
    "send" to "M2.01,21L23,12 2.01,3 2,10l15,2 -15,2z",
    "add" to "M19,13h-6v6h-2v-6H5v-2h6V5h2v6h6v2z",
    "attach" to "M16.5,6v11.5c0,2.21 -1.79,4 -4,4s-4,-1.79 -4,-4V5c0,-1.38 1.12,-2.5 2.5,-2.5s2.5,1.12 2.5,2.5v10.5c0,0.55 -0.45,1 -1,1s-1,-0.45 -1,-1V6H10v9.5c0,1.38 1.12,2.5 2.5,2.5s2.5,-1.12 2.5,-2.5V5c0,-2.21 -1.79,-4 -4,-4S7,2.79 7,5v12.5c0,3.04 2.46,5.5 5.5,5.5s5.5,-2.46 5.5,-5.5V6h-1.5z",
    "star" to "M12,17.27L18.18,21l-1.64,-7.03L22,9.24l-7.19,-0.61L12,2 9.19,8.63 2,9.24l5.46,4.73L5.82,21z",
    "account" to "M12,2C6.48,2 2,6.48 2,12s4.48,10 10,10 10,-4.48 10,-10S17.52,2 12,2zM12,6c1.93,0 3.5,1.57 3.5,3.5S13.93,13 12,13s-3.5,-1.57 -3.5,-3.5S10.07,6 12,6zM12,20c-2.03,0 -4.43,-0.82 -6.14,-2.88C7.55,15.8 9.68,15 12,15s4.45,0.8 6.14,2.12C16.43,19.18 14.03,20 12,20z",
    "agents" to "M16,11c1.66,0 2.99,-1.34 2.99,-3S17.66,5 16,5c-1.66,0 -3,1.34 -3,3s1.34,3 3,3zM8,11c1.66,0 2.99,-1.34 2.99,-3S9.66,5 8,5C6.34,5 5,6.34 5,8s1.34,3 3,3zM8,13c-2.33,0 -7,1.17 -7,3.5V19h14v-2.5c0,-2.33 -4.67,-3.5 -7,-3.5zM16,13c-0.29,0 -0.62,0.02 -0.97,0.05 1.16,0.84 1.97,1.97 1.97,3.45V19h6v-2.5c0,-2.33 -4.67,-3.5 -7,-3.5z",
    "history" to "M13,3c-4.97,0 -9,4.03 -9,9H1l3.89,3.89 0.07,0.14L9,12H6c0,-3.87 3.13,-7 7,-7s7,3.13 7,7 -3.13,7 -7,7c-1.93,0 -3.68,-0.79 -4.94,-2.06l-1.42,1.42C8.27,19.99 10.51,21 13,21c4.97,0 9,-4.03 9,-9s-4.03,-9 -9,-9zM12,8v5l4.28,2.54 0.72,-1.21 -3.5,-2.08V8H12z",
    "trash" to "M6,19c0,1.1 0.9,2 2,2h8c1.1,0 2,-0.9 2,-2V7H6v12zM19,4h-3.5l-1,-1h-5l-1,1H5v2h14V4z",
    "help" to "M11,18h2v-2h-2v2zM12,2C6.48,2 2,6.48 2,12s4.48,10 10,10 10,-4.48 10,-10S17.52,2 12,2zM12,20c-4.41,0 -8,-3.59 -8,-8s3.59,-8 8,-8 8,3.59 8,8 -3.59,8 -8,8zM12,6c-2.21,0 -4,1.79 -4,4h2c0,-1.1 0.9,-2 2,-2s2,0.9 2,2c0,2 -3,1.75 -3,5h2c0,-2.25 3,-2.5 3,-5 0,-2.21 -1.79,-4 -4,-4z",
    // Settings: a gear; even-odd keeps its centre open whichever way its two paths wind.
    "settings" to "M19.14,12.94c.04,-.3 .06,-.61 .06,-.94 0,-.32 -.02,-.64 -.07,-.94l2.03,-1.58c.18,-.14 .23,-.41 .12,-.61l-1.92,-3.32c-.12,-.22 -.37,-.29 -.59,-.22l-2.39,.96c-.5,-.38 -1.03,-.7 -1.62,-.94L14.4,2.81c-.04,-.24 -.24,-.41 -.48,-.41h-3.84c-.24,0 -.43,.17 -.47,.41L9.25,5.35C8.66,5.59 8.12,5.92 7.63,6.29L5.24,5.33c-.22,-.08 -.47,0 -.59,.22L2.74,8.87C2.62,9.08 2.66,9.34 2.86,9.48l2.03,1.58C4.84,11.36 4.8,11.69 4.8,12s.02,.64 .07,.94l-2.03,1.58c-.18,.14 -.23,.41 -.12,.61l1.92,3.32c.12,.22 .37,.29 .59,.22l2.39,-.96c.5,.38 1.03,.7 1.62,.94l.36,2.54c.05,.24 .24,.41 .48,.41h3.84c.24,0 .44,-.17 .47,-.41l.36,-2.54c.59,-.24 1.13,-.56 1.62,-.94l2.39,.96c.22,.08 .47,0 .59,-.22l1.92,-3.32c.12,-.22 .07,-.47 -.12,-.61L19.14,12.94zM12,15.6c-1.98,0 -3.6,-1.62 -3.6,-3.6s1.62,-3.6 3.6,-3.6 3.6,1.62 3.6,3.6 -1.62,3.6 -3.6,3.6z",
)

@Composable
fun Glyph(name: String, description: String?, tint: Color = LocalContentColor.current, size: Dp = 24.dp) {
    val data = glyphs.getValue(name)
    val fillType = if (name == "settings") PathFillType.EvenOdd else PathFillType.NonZero
    val vector = remember(data) { ImageVector.Builder(name, 24.dp, 24.dp, 24f, 24f).addPath(PathParser().parsePathString(data).toNodes(), fillType, fill = SolidColor(Color.Black)).build() }
    Icon(vector, contentDescription = description, tint = tint, modifier = Modifier.size(size))
}
