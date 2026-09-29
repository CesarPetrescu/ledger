package com.cesarpetrescu.ledger

import android.content.Context
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.Layout
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.LinkAnnotation
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextLinkStyles
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.text.withLink
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import org.commonmark.ext.gfm.strikethrough.Strikethrough
import org.commonmark.ext.gfm.strikethrough.StrikethroughExtension
import org.commonmark.ext.gfm.tables.TableBlock
import org.commonmark.ext.gfm.tables.TableCell
import org.commonmark.ext.gfm.tables.TableRow
import org.commonmark.ext.gfm.tables.TablesExtension
import org.commonmark.ext.task.list.items.TaskListItemMarker
import org.commonmark.ext.task.list.items.TaskListItemsExtension
import org.commonmark.node.*
import org.commonmark.parser.Parser

// Handoff text comes from agents. It is drawn from the parsed Markdown only:
// raw HTML is never shown, links open only for http(s) and mailto, and images
// appear as links, so opening a message never loads anything by itself.

private val parser: Parser = Parser.builder()
    .extensions(listOf(TablesExtension.create(), StrikethroughExtension.create(), TaskListItemsExtension.create()))
    .build()

// A message may be 100,000 characters from an agent: past these, formatting it
// could stall the phone, so it is shown as written instead.
private const val MAX_NODES = 3000
private const val MAX_DEPTH = 12

/** The parsed document, or null when it is too big or too deeply nested to format safely. */
fun parseBounded(text: String): Node? {
    val document = parser.parse(text)
    var count = 0
    // Walk without recursion, so depth itself cannot overflow the stack.
    val stack = ArrayDeque<Pair<Node, Int>>().apply { add(document to 0) }
    while (stack.isNotEmpty()) {
        val (node, depth) = stack.removeLast()
        if (++count > MAX_NODES || depth > MAX_DEPTH) return null
        var child = node.firstChild
        while (child != null) { stack.add(child to depth + 1); child = child.next }
    }
    return document
}

/** Whether a link may open: web and email only. */
fun safeLink(url: String?): Boolean = url != null && Regex("^(https?://|mailto:)", RegexOption.IGNORE_CASE).containsMatchIn(url.trim())

/** Whether handoffs show formatted Markdown; remembered on this phone. */
object MarkdownPreference {
    private fun prefs(context: Context) = context.getSharedPreferences("ui", Context.MODE_PRIVATE)
    fun on(context: Context) = prefs(context).getBoolean("markdown", true)
    fun set(context: Context, on: Boolean) = prefs(context).edit().putBoolean("markdown", on).apply()
}

@Composable
fun rememberMarkdownPreview(): MutableState<Boolean> {
    val context = LocalContext.current
    val state = remember { mutableStateOf(MarkdownPreference.on(context)) }
    return remember(state) {
        object : MutableState<Boolean> by state {
            override var value: Boolean
                get() = state.value
                set(v) { state.value = v; MarkdownPreference.set(context, v) }
        }
    }
}

@Composable
fun MarkdownSwitch(on: MutableState<Boolean>) {
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp)) {
        Switch(on.value, onCheckedChange = { on.value = it })
        Text("Markdown preview", style = MaterialTheme.typography.labelLarge)
    }
}

/** [text] as formatted Markdown, or exactly as written when [formatted] is off. */
@Composable
fun MarkdownText(text: String, formatted: Boolean = true, modifier: Modifier = Modifier) {
    if (!formatted) {
        SelectionContainer(modifier) { Text(text, style = MaterialTheme.typography.bodyMedium) }
        return
    }
    val document = remember(text) { parseBounded(text) }
    if (document == null) {
        Column(modifier, verticalArrangement = Arrangement.spacedBy(6.dp)) {
            Text("Too large to format; shown as written.", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            SelectionContainer { Text(text, style = MaterialTheme.typography.bodyMedium) }
        }
        return
    }
    SelectionContainer(modifier) {
        Column(verticalArrangement = Arrangement.spacedBy(10.dp)) { Blocks(document) }
    }
}

private fun Node.children(): List<Node> = generateSequence(firstChild) { it.next }.toList()

@Composable
private fun Blocks(parent: Node) {
    parent.children().forEach { Block(it) }
}

@Composable
private fun Block(node: Node) {
    val scheme = MaterialTheme.colorScheme
    val type = MaterialTheme.typography
    when (node) {
        is Heading -> Text(inline(node), style = when (node.level) { 1 -> type.headlineSmall; 2 -> type.titleLarge; 3 -> type.titleMedium; else -> type.titleSmall }, fontWeight = FontWeight.SemiBold)
        is Paragraph -> Text(inline(node), style = type.bodyMedium)
        is BulletList, is OrderedList -> ListBlock(node as ListBlock)
        is FencedCodeBlock -> Code(node.literal)
        is IndentedCodeBlock -> Code(node.literal)
        is BlockQuote -> Row(Modifier.height(IntrinsicSize.Min)) {
            Box(Modifier.width(3.dp).fillMaxHeight().background(scheme.outlineVariant))
            Column(Modifier.padding(start = 12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                CompositionLocalProvider(LocalContentColor provides scheme.onSurfaceVariant) { Blocks(node) }
            }
        }
        is ThematicBreak -> HorizontalDivider()
        is TableBlock -> Table(node)
        is HtmlBlock -> Unit // never rendered
        else -> Blocks(node)
    }
}

@Composable
private fun Code(literal: String) {
    Surface(color = MaterialTheme.colorScheme.surfaceVariant, shape = MaterialTheme.shapes.small, modifier = Modifier.fillMaxWidth()) {
        Text(literal.trimEnd('\n'), Modifier.horizontalScroll(rememberScrollState()).padding(12.dp),
            fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodySmall, softWrap = false)
    }
}

@Composable
private fun ListBlock(list: ListBlock) {
    val start = (list as? OrderedList)?.markerStartNumber ?: 1
    Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
        list.children().forEachIndexed { index, item ->
            // The checkbox marker sits on the item itself or at the start of its first paragraph.
            val task = (item.firstChild as? TaskListItemMarker) ?: (item.firstChild?.firstChild as? TaskListItemMarker)
            val marker = when {
                task != null -> if (task.isChecked) "☑" else "☐"
                list is OrderedList -> "${start + index}."
                else -> "•"
            }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(marker, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.primary)
                Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(6.dp)) { Blocks(item) }
            }
        }
    }
}

/** A table whose columns line up: each as wide as its widest cell, up to a limit; it scrolls sideways. */
@Composable
private fun Table(table: TableBlock) {
    val rows = mutableListOf<TableRow>()
    fun collect(node: Node) { node.children().forEach { if (it is TableRow) rows += it else collect(it) } }
    collect(table)
    val border = MaterialTheme.colorScheme.outlineVariant
    val header = MaterialTheme.colorScheme.surfaceVariant
    val columns = rows.maxOfOrNull { it.children().size } ?: 0
    if (columns == 0) return
    Box(Modifier.horizontalScroll(rememberScrollState()).border(1.dp, border)) {
        Layout(content = {
            rows.forEachIndexed { r, row ->
                val cells = row.children().filterIsInstance<TableCell>()
                for (c in 0 until columns) {
                    val cell = cells.getOrNull(c)
                    Box(Modifier.background(if (cell?.isHeader == true || r == 0) header else Color.Transparent).border(0.5.dp, border).padding(horizontal = 10.dp, vertical = 6.dp)) {
                        if (cell != null) Text(inline(cell), style = MaterialTheme.typography.bodySmall, fontWeight = if (cell.isHeader) FontWeight.SemiBold else null)
                    }
                }
            }
        }) { measurables, _ ->
            // Size from each cell's natural size, then measure every cell once at its column's width and row's height.
            val maxCell = 240.dp.roundToPx()
            val widths = IntArray(columns) { c -> measurables.filterIndexed { i, _ -> i % columns == c }.maxOf { minOf(it.maxIntrinsicWidth(Int.MAX_VALUE), maxCell) } }
            val heights = rows.indices.map { r -> (0 until columns).maxOf { c -> measurables[r * columns + c].minIntrinsicHeight(widths[c]) } }
            val fixed = rows.indices.flatMap { r -> (0 until columns).map { c ->
                measurables[r * columns + c].measure(androidx.compose.ui.unit.Constraints.fixed(widths[c], heights[r])) } }
            layout(widths.sum(), heights.sum()) {
                var y = 0
                rows.indices.forEach { r ->
                    var x = 0
                    (0 until columns).forEach { c -> fixed[r * columns + c].place(x, y); x += widths[c] }
                    y += heights[r]
                }
            }
        }
    }
}

@Composable
private fun inline(parent: Node): AnnotatedString {
    val scheme = MaterialTheme.colorScheme
    val code = SpanStyle(fontFamily = FontFamily.Monospace, background = scheme.surfaceVariant)
    val linkStyle = TextLinkStyles(SpanStyle(color = scheme.primary, textDecoration = TextDecoration.Underline))
    val uriHandler = LocalUriHandler.current
    // Nothing on the phone may handle a link (say, mailto: with no email app); that must not crash the app.
    fun link(url: String) = LinkAnnotation.Url(url, linkStyle) { runCatching { uriHandler.openUri(url) } }
    fun plain(node: Node): String = buildString {
        fun collect(n: Node) { if (n is org.commonmark.node.Text) append(n.literal) else if (n is Code) append(n.literal) else n.children().forEach(::collect) }
        node.children().forEach(::collect)
    }
    return buildAnnotatedString {
        fun walk(node: Node) {
            when (node) {
                is org.commonmark.node.Text -> append(node.literal)
                is Code -> withStyle(code) { append(node.literal) }
                is Emphasis -> withStyle(SpanStyle(fontStyle = FontStyle.Italic)) { node.children().forEach(::walk) }
                is StrongEmphasis -> withStyle(SpanStyle(fontWeight = FontWeight.Bold)) { node.children().forEach(::walk) }
                is Strikethrough -> withStyle(SpanStyle(textDecoration = TextDecoration.LineThrough)) { node.children().forEach(::walk) }
                is Link -> if (safeLink(node.destination)) withLink(link(node.destination.trim())) { node.children().forEach(::walk) } else node.children().forEach(::walk)
                is Image -> {
                    // The whole label, formatting and all, as plain words.
                    val alt = plain(node).ifBlank { "Image" }
                    if (safeLink(node.destination)) withLink(link(node.destination.trim())) { append("$alt (image link)") } else append(alt)
                }
                is SoftLineBreak -> append(' ')
                is HardLineBreak -> append('\n')
                is HtmlInline, is TaskListItemMarker -> Unit // never rendered; task boxes are drawn by the list
                else -> node.children().forEach(::walk)
            }
        }
        parent.children().forEach(::walk)
    }
}

/** A card like [SummaryCard] whose body is Markdown (or plain text when [formatted] is off). */
@Composable
fun MarkdownCard(title: String, subtitle: String, body: String, formatted: Boolean) {
    OutlinedCard(Modifier.fillMaxWidth()) {
        Column(Modifier.padding(18.dp), verticalArrangement = Arrangement.spacedBy(7.dp)) {
            Text(title, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
            if (subtitle.isNotBlank()) Text(subtitle, style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.primary)
            if (body.isNotBlank()) MarkdownText(body, formatted)
        }
    }
}

/** How a message will read once sent, while it is being written. */
@Composable
fun MarkdownPreviewBox(body: String) {
    if (body.isBlank()) return
    Surface(color = MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.5f), shape = MaterialTheme.shapes.medium, modifier = Modifier.fillMaxWidth()) {
        Column(Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Text("Preview", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.primary)
            MarkdownText(body)
        }
    }
}
