package com.cesarpetrescu.ledger

import android.app.DatePickerDialog
import android.app.TimePickerDialog
import android.text.format.DateFormat
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import org.json.JSONArray
import org.json.JSONObject
import java.time.LocalDate
import java.time.LocalDateTime
import java.time.OffsetDateTime
import java.time.YearMonth
import java.time.ZoneId
import java.time.format.DateTimeFormatter

/** One thing on a calendar day: an event, a todo due, a project deadline, or a snoozed item waking. */
data class DayItem(val kind: String, val time: String, val title: String, val detail: String, val route: String, val sort: String, val overdue: Boolean = false)

/** The six weeks, Monday first, that show a month. */
fun monthDays(month: YearMonth): List<LocalDate> {
    val first = month.atDay(1)
    val start = first.minusDays(((first.dayOfWeek.value + 6) % 7).toLong())
    return (0 until 42).map { start.plusDays(it.toLong()) }
}

private val shortTime: DateTimeFormatter = DateTimeFormatter.ofPattern("HH:mm")

/** Everything on each day between [first] and [last) from the calendar and from Ledger itself. */
fun dayItems(events: List<JSONObject>, due: List<JSONObject>, waking: List<JSONObject>, projects: List<JSONObject>,
             first: LocalDate, last: LocalDate, today: LocalDate, zone: ZoneId = ZoneId.systemDefault()): Map<LocalDate, List<DayItem>> {
    val out = mutableMapOf<LocalDate, MutableList<DayItem>>()
    fun add(day: LocalDate, item: DayItem) { if (!day.isBefore(first) && day.isBefore(last)) out.getOrPut(day) { mutableListOf() } += item }
    for (event in events) {
        val allDay = event.optBoolean("all_day")
        val start = runCatching { if (allDay) LocalDate.parse(event.text("start")).atStartOfDay(zone) else OffsetDateTime.parse(event.text("start")).atZoneSameInstant(zone) }.getOrNull() ?: continue
        val end = runCatching { if (allDay) LocalDate.parse(event.text("end")).atStartOfDay(zone) else OffsetDateTime.parse(event.text("end")).atZoneSameInstant(zone) }.getOrNull() ?: start
        // Every day it covers; an end at midnight does not reach that day.
        val lastDay = maxOf(start.toLocalDate(), end.minusNanos(1).toLocalDate())
        var day = maxOf(start.toLocalDate(), first)
        while (!day.isAfter(lastDay) && day.isBefore(last)) {
            val time = when { allDay -> "All day"; day != start.toLocalDate() -> "Continues"; else -> "${start.format(shortTime)}–${end.format(shortTime)}" }
            add(day, DayItem("event", time, event.text("title").ifBlank { "Untitled event" }, listOf(event.text("calendar_name"), event.text("location")).filter { it.isNotBlank() }.joinToString(" · "),
                "event/${segment(event.text("id"))}", "1${if (allDay) "0" else start.toLocalTime()}"))
            day = day.plusDays(1)
        }
    }
    for (todo in due) {
        val day = runCatching { LocalDate.parse(todo.optJSONObject("meta")?.text("due")) }.getOrNull() ?: continue
        add(day, DayItem("todo", if (day.isBefore(today)) "Overdue" else "Todo due", entryTitle(todo), todo.text("project_name"), "entry-view/${segment(todo.text("id"))}", "2${entryTitle(todo)}", day.isBefore(today)))
    }
    for (entry in waking) {
        val day = runCatching { LocalDate.parse(entry.optJSONObject("owner")?.text("snoozed_until")) }.getOrNull() ?: continue
        add(day, DayItem("wake", "Wakes up", entryTitle(entry), entry.text("project_name"), "entry-view/${segment(entry.text("id"))}", "3${entryTitle(entry)}"))
    }
    for (project in projects) {
        // Only a real date counts; some deadlines are words like "daily".
        val day = runCatching { LocalDate.parse(project.text("deadline")) }.getOrNull() ?: continue
        add(day, DayItem("deadline", "Deadline", project.text("name"), "Project deadline", projectRoute(project.text("slug")), "0${project.text("name")}"))
    }
    return out.mapValues { (_, items) -> items.sortedBy { it.sort } }
}

/** Every page of an entry list. */
private fun allEntries(api: Api, query: String): List<JSONObject> {
    val out = mutableListOf<JSONObject>()
    var before = ""
    while (true) {
        val page = api.request("GET", "/entries?limit=200&$query" + if (before.isBlank()) "" else "&before=${segment(before)}")
        out += page.rows("entries")
        val next = page.text("next_before")
        if (next.isBlank() || next == before) return out
        before = next
    }
}

@Composable
private fun kindColor(item: DayItem): Color = when {
    item.overdue || item.kind == "deadline" -> MaterialTheme.colorScheme.error
    item.kind == "todo" -> if (isDark()) Color(0xFFF3C46E) else Color(0xFFB66A0A)
    item.kind == "wake" -> if (isDark()) Color(0xFFB9A5F0) else Color(0xFF6B3FA0)
    else -> MaterialTheme.colorScheme.primary
}

@Composable
fun CalendarScreen(model: LedgerModel) {
    var monthText by rememberSaveable { mutableStateOf(YearMonth.now().toString()) }
    var selectedText by rememberSaveable { mutableStateOf(LocalDate.now().toString()) }
    val month = YearMonth.parse(monthText)
    val selected = LocalDate.parse(selectedText)
    val today = LocalDate.now()
    val days = monthDays(month)
    val first = days.first()
    val last = days.last().plusDays(1)
    val zone = ZoneId.systemDefault()
    Load(model, "calendar:$month", { api ->
        val connection = api.request("GET", "/calendar/connection")
        // Nextcloud being down must not hide Ledger's own dates; say so instead.
        var eventsFailed = false
        val events = if (connection.optBoolean("connected") && connection.optInt("selected_calendars") > 0) try {
            api.request("GET", "/calendar/events?start=${segment(first.atStartOfDay(zone).format(DateTimeFormatter.ISO_OFFSET_DATE_TIME))}&end=${segment(last.atStartOfDay(zone).format(DateTimeFormatter.ISO_OFFSET_DATE_TIME))}").optJSONArray("events") ?: JSONArray()
        } catch (e: Exception) {
            // A lost sign-in and a cancelled load still go the usual way.
            if (e is kotlinx.coroutines.CancellationException || (e is ApiError && e.status == 401)) throw e
            eventsFailed = true
            JSONArray()
        } else JSONArray()
        json("connection" to connection, "events" to events, "events_failed" to eventsFailed,
            "due" to JSONArray(allEntries(api, "kind=todo&status=open&due_from=$first&due_before=$last")),
            "waking" to JSONArray(allEntries(api, "wakes_from=$first&wakes_before=$last")),
            "projects" to (api.request("GET", "/projects").optJSONArray("projects") ?: JSONArray()))
    }) { data ->
        val connected = data.getJSONObject("connection").optBoolean("connected")
        val items = dayItems(data.rows("events"), data.rows("due"), data.rows("waking"), data.rows("projects"), first, last, today, zone)
        val show = { m: YearMonth -> monthText = m.toString(); selectedText = (if (m == YearMonth.from(today)) today else m.atDay(1)).toString() }
        Page {
            item {
                Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                    Text(month.format(DateTimeFormatter.ofPattern("MMMM yyyy")), Modifier.weight(1f), style = MaterialTheme.typography.headlineSmall, fontWeight = FontWeight.SemiBold)
                    TextButton(onClick = { show(month.minusMonths(1)) }, modifier = Modifier.semantics { contentDescription = "Previous month" }) { Text("‹", style = MaterialTheme.typography.headlineSmall) }
                    OutlinedButton(onClick = { show(YearMonth.from(today)) }, contentPadding = PaddingValues(horizontal = 14.dp)) { Text("Today") }
                    TextButton(onClick = { show(month.plusMonths(1)) }, modifier = Modifier.semantics { contentDescription = "Next month" }) { Text("›", style = MaterialTheme.typography.headlineSmall) }
                }
            }
            if (data.optBoolean("events_failed")) item {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text("Couldn't reach your Nextcloud calendar; showing Ledger's own dates.", Modifier.weight(1f), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
                    TextButton(onClick = model::refresh) { Text("Retry") }
                }
            }
            item { MonthGrid(days, month, selected, today, items) { selectedText = it.toString() } }
            item {
                FlowRow(horizontalArrangement = Arrangement.spacedBy(14.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    listOf(DayItem("event", "", "Events", "", "", ""), DayItem("todo", "", "Todos due", "", "", ""), DayItem("deadline", "", "Deadlines", "", "", ""), DayItem("wake", "", "Waking up", "", "", "")).forEach { key ->
                        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                            Box(Modifier.size(8.dp).clip(CircleShape).background(kindColor(key)))
                            Text(key.title, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        }
                    }
                }
            }
            item { Text(selected.format(DateTimeFormatter.ofPattern("EEEE, d MMMM")), style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold, modifier = Modifier.padding(top = 4.dp)) }
            val onDay = items[selected].orEmpty()
            if (onDay.isEmpty()) item { Empty("Nothing on this day.") }
            items(onDay) { item -> DayRow(item) { model.go(item.route) } }
            item {
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    if (connected) Button(onClick = { model.go("event-new") }, enabled = !model.busy) { Text("New event") }
                    OutlinedButton(onClick = { model.go("calendar-settings") }) { Text(if (connected) "Calendars" else "Connect Nextcloud") }
                }
            }
            if (!connected) item { Text("Connect a Nextcloud calendar to see your events here too. Todos due, deadlines, and snoozed items show either way.", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
        }
    }
}

@Composable
private fun MonthGrid(days: List<LocalDate>, month: YearMonth, selected: LocalDate, today: LocalDate, items: Map<LocalDate, List<DayItem>>, pick: (LocalDate) -> Unit) {
    val scheme = MaterialTheme.colorScheme
    val spoken = DateTimeFormatter.ofPattern("EEEE d MMMM")
    Column(Modifier.fillMaxWidth()) {
        Row(Modifier.fillMaxWidth()) {
            days.take(7).forEach { day ->
                Text(day.dayOfWeek.getDisplayName(java.time.format.TextStyle.NARROW, java.util.Locale.getDefault()), Modifier.weight(1f).padding(bottom = 6.dp),
                    textAlign = TextAlign.Center, style = MaterialTheme.typography.labelSmall, color = scheme.onSurfaceVariant)
            }
        }
        days.chunked(7).forEach { week ->
            Row(Modifier.fillMaxWidth()) {
                week.forEach { day ->
                    val list = items[day].orEmpty()
                    val isSelected = day == selected
                    val inMonth = YearMonth.from(day) == month
                    Column(Modifier.weight(1f).height(52.dp).clip(RoundedCornerShape(10.dp)).clickable { pick(day) }
                        .semantics(mergeDescendants = true) { contentDescription = "${day.format(spoken)}, ${list.size} ${if (list.size == 1) "item" else "items"}" },
                        horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.Center) {
                        Box(Modifier.size(32.dp).clip(CircleShape).background(if (isSelected) scheme.primary else Color.Transparent)
                            .then(if (day == today && !isSelected) Modifier.border(1.5.dp, scheme.primary, CircleShape) else Modifier), contentAlignment = Alignment.Center) {
                            Text("${day.dayOfMonth}", style = MaterialTheme.typography.bodyMedium, fontWeight = if (day == today || isSelected) FontWeight.Bold else FontWeight.Normal,
                                color = when { isSelected -> scheme.onPrimary; !inMonth -> scheme.onSurfaceVariant.copy(alpha = 0.45f); day == today -> scheme.primary; else -> scheme.onSurface })
                        }
                        Row(Modifier.height(8.dp).padding(top = 3.dp), horizontalArrangement = Arrangement.spacedBy(3.dp)) {
                            list.distinctBy { it.kind to it.overdue }.take(3).forEach { Box(Modifier.size(5.dp).clip(CircleShape).background(kindColor(it))) }
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun DayRow(item: DayItem, open: () -> Unit) {
    Row(Modifier.fillMaxWidth().clip(RoundedCornerShape(12.dp)).background(MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.5f)).clickable(onClick = open).padding(end = 14.dp),
        verticalAlignment = Alignment.CenterVertically) {
        Box(Modifier.width(4.dp).height(56.dp).background(kindColor(item)))
        Column(Modifier.padding(start = 12.dp, top = 10.dp, bottom = 10.dp).weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Text(item.time, style = MaterialTheme.typography.labelSmall, color = kindColor(item), fontWeight = FontWeight.SemiBold)
            Text(item.title, style = MaterialTheme.typography.bodyLarge, fontWeight = FontWeight.SemiBold, maxLines = 2, overflow = TextOverflow.Ellipsis)
            if (item.detail.isNotBlank()) Text(item.detail, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
    }
}

@Composable
fun EventEditor(model: LedgerModel, id: String = "") = Load(model, "event-edit:$id", { api ->
    api.request("GET", "/calendar/calendars").put("event", if (id.isBlank()) JSONObject() else api.request("GET", "/calendar/events/${segment(id)}"))
}) { data -> EventForm(model, id, data.getJSONObject("event"), data.rows("calendars")) }

private fun localTime(value: String, fallback: LocalDateTime): String = runCatching {
    if (value.length == 10) LocalDate.parse(value).atStartOfDay().toString()
    else OffsetDateTime.parse(value).atZoneSameInstant(ZoneId.systemDefault()).toLocalDateTime().toString()
}.getOrDefault(fallback.withSecond(0).withNano(0).toString())

fun eventTimestamp(value: String, allDay: Boolean): String {
    val time = LocalDateTime.parse(value)
    if (allDay) return time.toLocalDate().toString()
    val zone = ZoneId.systemDefault()
    require(zone.rules.getValidOffsets(time).isNotEmpty()) { "This time falls in a daylight-saving gap. Choose another time." }
    return time.atZone(zone).toOffsetDateTime().format(DateTimeFormatter.ISO_OFFSET_DATE_TIME)
}

@Composable
private fun EventForm(model: LedgerModel, id: String, event: JSONObject, calendars: List<JSONObject>) {
    var title by rememberSaveable { mutableStateOf(event.text("title")) }
    var calendar by rememberSaveable { mutableStateOf(event.text("calendar_id").ifBlank { calendars.firstOrNull { it.optBoolean("selected") }?.text("id") ?: "" }) }
    var allDay by rememberSaveable { mutableStateOf(event.optBoolean("all_day")) }
    var start by rememberSaveable { mutableStateOf(localTime(event.text("start"), LocalDateTime.now().plusHours(1))) }
    var end by rememberSaveable { mutableStateOf(localTime(event.text("end"), LocalDateTime.now().plusHours(2))) }
    var location by rememberSaveable { mutableStateOf(event.text("location")) }
    var description by rememberSaveable { mutableStateOf(event.text("description")) }
    // Keep the ETag paired with the draft; refreshing must not silently overwrite concurrent edits.
    val etag = rememberSaveable { event.text("etag") }
    val recurring = event.optBoolean("recurring")
    Page {
        item { Text(if (id.isBlank()) "New event" else "Event details", style = MaterialTheme.typography.headlineSmall) }
        if (recurring) item { Text("This event belongs to a recurring series. Manage the series in your calendar provider.", color = MaterialTheme.colorScheme.onSurfaceVariant) }
        item { Field("Title", title, { title = it }, max = 200) }
        if (id.isBlank()) item { Choice("Calendar", calendar, calendars.filter { it.optBoolean("selected") }.map { it.text("id") to it.text("name") }) { calendar = it } }
        else item { Text(event.text("calendar_name")) }
        item { Row(verticalAlignment = androidx.compose.ui.Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            Switch(allDay, enabled = !model.busy, onCheckedChange = { allDay = it; if (it && LocalDateTime.parse(end).toLocalDate() <= LocalDateTime.parse(start).toLocalDate()) end = LocalDateTime.parse(start).plusDays(1).toString() })
            Text("All day")
        } }
        item { DateTimeField("Starts", start, allDay) { start = it } }
        item { DateTimeField(if (allDay) "Ends (exclusive)" else "Ends", end, allDay) { end = it } }
        item { Text(if (allDay) "The end date is the first day after the event." else "Time zone: ${ZoneId.systemDefault()}", style = MaterialTheme.typography.bodySmall) }
        item { Field("Location", location, { location = it }, max = 500) }
        item { Field("Description", description, { description = it }, multiline = true, max = 4000) }
        item { Button(enabled = !model.busy && title.isNotBlank() && calendar.isNotBlank() && !recurring, modifier = Modifier.fillMaxWidth(), onClick = {
            model.act(after = model::back) { api ->
                val payload = json("title" to title.trim(), "start" to eventTimestamp(start, allDay), "end" to eventTimestamp(end, allDay), "all_day" to allDay, "location" to location, "description" to description)
                require(if (allDay) LocalDateTime.parse(end).toLocalDate() > LocalDateTime.parse(start).toLocalDate() else LocalDateTime.parse(end) > LocalDateTime.parse(start)) { "End must be after start." }
                if (id.isBlank()) api.request("POST", "/calendar/events", payload.put("calendar_id", calendar))
                else api.request("PUT", "/calendar/events/${segment(id)}", payload.put("etag", etag))
            }
        }) { Text("Save event") } }
        if (id.isNotBlank()) item { ConfirmButton("Delete event", "Delete this event from your calendar?", !model.busy && !recurring, danger = true) {
            model.act("Event deleted", after = model::back) { it.request("DELETE", "/calendar/events/${segment(id)}", json("etag" to etag)) }
        } }
    }
}

@Composable
fun DateTimeField(label: String, value: String, allDay: Boolean, change: (String) -> Unit) {
    val context = LocalContext.current
    val date = LocalDateTime.parse(value)
    Column {
        Text(label, style = MaterialTheme.typography.labelLarge)
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            OutlinedButton(onClick = {
                DatePickerDialog(context, { _, y, m, d -> change(LocalDate.of(y, m + 1, d).atTime(date.toLocalTime()).toString()) }, date.year, date.monthValue - 1, date.dayOfMonth).show()
            }) { Text(date.format(DateTimeFormatter.ofPattern("EEE, d MMM yyyy"))) }
            if (!allDay) OutlinedButton(onClick = {
                TimePickerDialog(context, { _, h, m -> change(date.withHour(h).withMinute(m).withSecond(0).withNano(0).toString()) }, date.hour, date.minute, DateFormat.is24HourFormat(context)).show()
            }) { Text(date.format(DateTimeFormatter.ofPattern("HH:mm"))) }
        }
    }
}

@Composable
fun CalendarSettings(model: LedgerModel) {
    val context = LocalContext.current
    var url by rememberSaveable { mutableStateOf("") }
    var pending by rememberSaveable { mutableStateOf("") }
    var loginURL by rememberSaveable { mutableStateOf("") }
    Load(model, "calendar-settings", { api ->
        val connection = api.request("GET", "/calendar/connection")
        if (connection.optBoolean("connected")) connection.put("calendars", api.request("GET", "/calendar/calendars").optJSONArray("calendars")) else connection
    }) { data ->
        Page {
            if (data.optBoolean("connected")) {
                item { SummaryCard("Connected", data.text("username"), data.text("server_url")) }
                item { Text("Visible calendars", style = MaterialTheme.typography.titleLarge) }
                items(data.rows("calendars")) { calendar ->
                    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(12.dp), verticalAlignment = androidx.compose.ui.Alignment.CenterVertically) {
                        Checkbox(calendar.optBoolean("selected"), enabled = !model.busy, onCheckedChange = { checked ->
                            model.act("Calendar selection updated") { api ->
                                val ids = api.request("GET", "/calendar/calendars").rows("calendars").filter { if (it.text("id") == calendar.text("id")) checked else it.optBoolean("selected") }.map { it.text("id") }
                                api.request("PUT", "/calendar/calendars", json("ids" to JSONArray(ids)))
                            }
                        })
                        Text(calendar.text("name"))
                    }
                }
                item { ConfirmButton("Disconnect calendar", "Disconnect this calendar account from Ledger? Events remain with the provider.", !model.busy, danger = true) {
                    model.act("Calendar disconnected") { it.request("DELETE", "/calendar/connection") }
                } }
            } else {
                item { Text("Connect Nextcloud", style = MaterialTheme.typography.headlineSmall) }
                item { Field("Nextcloud server address", url, { url = it }, placeholder = "https://cloud.example.com") }
                item { Button(enabled = !model.busy && url.isNotBlank(), onClick = {
                    var result = JSONObject()
                    model.act("Complete the sign-in in your browser", after = { pending = result.text("id"); loginURL = result.text("login_url"); openBrowser(context, loginURL, model) }) {
                        result = it.request("POST", "/calendar/connect", json("server_url" to url.trim()))
                    }
                }) { Text("Connect Nextcloud") } }
                if (pending.isNotBlank()) {
                    item { Text("After approving access in the browser, return here and finish connecting.") }
                    item { OutlinedButton(onClick = { openBrowser(context, loginURL, model) }) { Text("Open sign-in again") } }
                    item { Button(enabled = !model.busy, onClick = {
                        var result = JSONObject()
                        model.act("Connection checked", after = {
                            if (result.optBoolean("pending")) model.notice = "Waiting for approval. Complete sign-in in the browser first."
                            else pending = ""
                        }) { result = it.request("POST", "/calendar/connect/${segment(pending)}/poll") }
                    }) { Text("Finish connecting") } }
                }
            }
        }
    }
}
