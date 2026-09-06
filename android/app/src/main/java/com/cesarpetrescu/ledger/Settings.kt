package com.cesarpetrescu.ledger

import android.content.Context
import android.content.Intent
import android.net.Uri
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import org.json.JSONObject
import java.net.URI

fun openBrowser(context: Context, address: String, model: LedgerModel) {
    try {
        val uri = URI(address)
        require(uri.scheme == "https" && !uri.host.isNullOrBlank() && uri.rawUserInfo == null) { "The server returned an unsafe link." }
        context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(address)))
    } catch (_: Exception) { model.notice = "Could not open this HTTPS link. Check that a browser is installed." }
}

@Composable
fun Settings(model: LedgerModel) {
    val context = LocalContext.current
    Page {
        item { SummaryCard("Ledger ${BuildConfig.VERSION_NAME}", "Owner console", model.api?.origin ?: "") }
        item { SummaryCard("Connected clients", body = "Review ChatGPT, Claude, CLI, and other clients. Revoke access when needed.") { model.go("clients") } }
        item { SummaryCard("Approve a device", body = "Enter the code shown by the Ledger CLI.") { model.go("device") } }
        item { SummaryCard("Calendars", body = "Connect Nextcloud and choose visible calendars.") { model.go("calendar-settings") } }
        item { OutlinedButton(onClick = { openBrowser(context, "https://github.com/CesarPetrescu/ledger/releases/latest", model) }) { Text("Check for updates") } }
        item { ConfirmButton("Sign out", "Sign out and revoke this phone's owner session?", !model.busy, model::logout) }
        item { ConfirmButton("Forget this phone", "Remove the saved session from this phone without contacting the server. Use this if the server is unreachable. The server session remains valid until it expires.", !model.busy, model::forget) }
    }
}

@Composable
fun Clients(model: LedgerModel) {
    var offset by rememberSaveable { mutableStateOf(0) }
    Load(model, "clients:$offset", { it.request("GET", "/oauth/clients?limit=50&offset=$offset") }) { data ->
        Page {
            if (data.rows("clients").isEmpty()) item { Empty("No registered clients.") }
            items(data.rows("clients")) { client ->
                Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    SummaryCard(client.text("client_name").ifBlank { "Unnamed client" }, label(client.text("kind")),
                        "${client.optInt("active_access_tokens")} access tokens · ${client.optInt("active_refresh_tokens")} refresh tokens\nLast used: ${displayTime(client.text("last_used_at")).ifBlank { "Never" }}\n${client.strings("redirect_uris").joinToString("\n")}")
                    ConfirmButton("Revoke access", "Revoke all access and refresh tokens for ${client.text("client_name")}? It will need to reconnect.", !model.busy) {
                        model.act("Client access revoked") { it.request("POST", "/oauth/revoke", json("client_id" to client.text("client_id"))) }
                    }
                }
            }
            item { Row {
                if (offset > 0) TextButton(onClick = { offset = (offset - 50).coerceAtLeast(0) }) { Text("Previous") }
                if (!data.isNull("next_offset")) TextButton(onClick = { offset = data.getInt("next_offset") }) { Text("Next") }
            } }
        }
    }
}

@Composable
fun Device(model: LedgerModel) {
    var code by rememberSaveable { mutableStateOf("") }
    var result by remember { mutableStateOf<JSONObject?>(null) }
    var verifiedCode by remember { mutableStateOf("") }
    Page {
        item { Text("Connect the Ledger CLI", style = MaterialTheme.typography.headlineSmall) }
        item { Text("Run ledger login on your computer, then enter its device code here.") }
        item { Field("Device code", code, { code = it.uppercase(); result = null }, max = 12, placeholder = "ABCD-EFGH") }
        item { Button(enabled = !model.busy && code.replace("-", "").length == 8, onClick = {
            var response = JSONObject()
            val submitted = code
            model.act("Review this request before approving", after = { result = response; verifiedCode = submitted }) { response = it.request("POST", "/oauth/device", json("user_code" to submitted, "action" to "lookup")) }
        }) { Text("Look up request") } }
        result?.let { request ->
            item { SummaryCard(request.text("client_name"), "Expires ${displayTime(request.text("expires_at"))}", "Requested scope: ${request.text("scope")}") }
            item { Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                ConfirmButton("Approve", "Allow ${request.text("client_name")} the scope shown above? Only approve a code from a device you control.", !model.busy) {
                    model.act("Device approved", after = model::back) { it.request("POST", "/oauth/device", json("user_code" to verifiedCode, "action" to "approve")) }
                }
                OutlinedButton(enabled = !model.busy, onClick = {
                    model.act("Request denied", after = { result = null; code = "" }) { it.request("POST", "/oauth/device", json("user_code" to verifiedCode, "action" to "deny")) }
                }) { Text("Deny") }
            } }
        }
    }
}
