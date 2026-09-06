package com.cesarpetrescu.ledger

import android.app.DatePickerDialog
import android.app.TimePickerDialog
import android.text.format.DateFormat
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import org.json.JSONArray
import org.json.JSONObject
import java.time.LocalDate
import java.time.LocalDateTime
import java.time.OffsetDateTime
import java.time.ZoneId
import java.time.format.DateTimeFormatter

@Composable
fun CalendarScreen(model: LedgerModel) {
    var day by rememberSaveable { mutableStateOf(LocalDate.now().toString()) }
    val date = LocalDate.parse(day)
    val zone = ZoneId.systemDefault()
    Load(model, "calendar:$day", { api ->
        val connection = api.request("GET", "/calendar/connection")
        if (!connection.optBoolean("connected")) json("connection" to connection)
        else api.request("GET", "/calendar/events?start=${segment(date.atStartOfDay(zone).format(DateTimeFormatter.ISO_OFFSET_DATE_TIME))}&end=${segment(date.plusDays(7).atStartOfDay(zone).format(DateTimeFormatter.ISO_OFFSET_DATE_TIME))}")
            .put("connection", connection)
    }) { data ->
        Page {
            item { Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Button(onClick = { model.go("event-new") }, enabled = data.getJSONObject("connection").optBoolean("connected") && !model.busy) { Text("New event") }
                OutlinedButton(onClick = { model.go("calendar-settings") }) { Text("Calendars") }
            } }
            if (!data.getJSONObject("connection").optBoolean("connected")) item { Empty("Connect your Nextcloud calendar to see and manage events.") }
            else {
                item { DateTimeField("Week starting", day + "T00:00", true) { day = LocalDateTime.parse(it).toLocalDate().toString() } }
                item { Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    TextButton(onClick = { day = date.minusWeeks(1).toString() }) { Text("Previous") }
                    TextButton(onClick = { day = LocalDate.now().toString() }) { Text("Today") }
                    TextButton(onClick = { day = date.plusWeeks(1).toString() }) { Text("Next") }
                } }
                if (data.rows("events").isEmpty()) item { Empty("No events this week.") }
                items(data.rows("events")) { event ->
                    SummaryCard(event.text("title"), event.text("calendar_name"),
                        (if (event.optBoolean("all_day")) "${event.text("start")} · All day" else "${displayTime(event.text("start"))} – ${displayTime(event.text("end"))}") +
                            if (event.text("location").isNotBlank()) "\n${event.text("location")}" else "") { model.go("event/${event.text("id")}") }
                }
            }
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
        if (recurring) item { Text("This event belongs to a recurring series. Manage the series in your calendar provider.", color = MaterialTheme.colorScheme.primary) }
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
        if (id.isNotBlank()) item { ConfirmButton("Delete event", "Delete this event from your calendar?", !model.busy && !recurring) {
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
                item { ConfirmButton("Disconnect calendar", "Disconnect this calendar account from Ledger? Events remain with the provider.", !model.busy) {
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
