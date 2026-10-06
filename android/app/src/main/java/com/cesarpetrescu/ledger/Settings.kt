package com.cesarpetrescu.ledger

import android.Manifest
import android.app.Activity
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.provider.Settings as AndroidSettings
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import org.json.JSONObject
import java.net.URI

/** Opens an HTTPS link in the browser; reports whether it launched. */
fun openBrowser(context: Context, address: String, model: LedgerModel): Boolean {
    return try {
        val uri = URI(address)
        require(uri.scheme == "https" && !uri.host.isNullOrBlank() && uri.rawUserInfo == null) { "The server returned an unsafe link." }
        context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(address)))
        true
    } catch (_: Exception) {
        model.notice = "Could not open this HTTPS link. Check that a browser is installed."
        false
    }
}

@Composable
fun Settings(model: LedgerModel) {
    val context = LocalContext.current
    Page {
        item { SummaryCard("Ledger ${BuildConfig.VERSION_NAME}", "Owner console", model.api?.origin ?: "") }
        item { ThemeChoice(model) }
        item { NotificationsChoice(model) }
        item { SummaryCard("Connected apps", body = "Review ChatGPT, Claude, CLI, and other apps. Revoke access when needed.") { model.go("clients") } }
        item { SummaryCard("API keys", body = "Keys that let a server such as Adastrion Core pick up research. Create them in the web console; revoke them here.") { model.go("api-keys") } }
        item { SummaryCard("Approve a device", body = "Enter the code shown by the Ledger CLI.") { model.go("device") } }
        item { SummaryCard("Calendars", body = "Connect Nextcloud and choose visible calendars.") { model.go("calendar-settings") } }
        item { OutlinedButton(onClick = { openBrowser(context, "https://github.com/CesarPetrescu/ledger/releases/latest", model) }) { Text("Check for updates") } }
        item { ConfirmButton("Sign out", "Sign out and revoke this phone's owner session?", !model.busy, model::logout) }
        item { ConfirmButton("Forget this phone", "Remove the saved session from this phone without contacting the server. Use this if the server is unreachable. The server session remains valid until it expires.", !model.busy, model::forget) }
    }
}

/** Turns notifications on or off, asking Android for permission when it must. */
@Composable
fun rememberNotificationSwitch(model: LedgerModel, changed: (Boolean) -> Unit): (Boolean) -> Unit {
    val context = LocalContext.current
    // Opted in, but Android keeps them from showing: keep what was seen so nothing is lost; only Android's settings can allow them.
    val blocked = {
        if (!Notifier.enabled(context)) Notifier.setEnabled(context, true)
        model.notice = "Turn on Ledger's notifications in Android settings to see them."
        context.startActivity(Intent(AndroidSettings.ACTION_APP_NOTIFICATION_SETTINGS).putExtra(AndroidSettings.EXTRA_APP_PACKAGE, context.packageName).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
    }
    val rationale = { (context as? Activity)?.shouldShowRequestPermissionRationale(Manifest.permission.POST_NOTIFICATIONS) == true }
    var deniedBefore by rememberSaveable { mutableStateOf(false) }
    val ask = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        when {
            granted -> { Notifier.setEnabled(context, true); changed(true) }
            // Not denied before and still no reason to explain: Android showed no dialog, after
            // repeated denials or by policy. Asking again would do nothing.
            !deniedBefore && !rationale() -> blocked()
            else -> model.notice = "Notifications stay off. Allow them for Ledger in Android settings to turn them on."
        }
    }
    return { want ->
        when {
            !want -> { Notifier.setEnabled(context, false); changed(false) }
            Notifier.permissionMissing(context) -> { deniedBefore = rationale(); ask.launch(Manifest.permission.POST_NOTIFICATIONS) }
            else -> {
                // Already on but blocked in Android: keep what was seen so nothing is lost.
                if (!Notifier.enabled(context)) Notifier.setEnabled(context, true)
                changed(Notifier.allowed(context))
                // Permitted, but switched off for Ledger or its channel: that is an Android setting.
                if (!Notifier.allowed(context)) blocked()
            }
        }
    }
}

@Composable
fun NotificationsChoice(model: LedgerModel) {
    val context = LocalContext.current
    var on by remember { mutableStateOf(Notifier.enabled(context) && Notifier.allowed(context)) }
    // Back from Android's notification settings, show what they now allow.
    LifecycleResumeEffect(Unit) {
        on = Notifier.enabled(context) && Notifier.allowed(context)
        onPauseOrDispose {}
    }
    val toggle = rememberNotificationSwitch(model) { on = it }
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Text("Notifications", style = MaterialTheme.typography.titleSmall)
            Text("Tell me when an agent asks something or a todo becomes overdue. Your phone asks your own server about every 15 minutes; nothing goes through Google.",
                style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        Switch(checked = on, onCheckedChange = toggle)
    }
}

@Composable
fun Clients(model: LedgerModel) {
    var offset by rememberSaveable { mutableStateOf(0) }
    Load(model, "clients:$offset", { it.request("GET", "/oauth/clients?limit=50&offset=$offset") }) { data ->
        Page {
            if (data.rows("clients").isEmpty()) item { Empty("No apps have connected yet. More › Agents shows how to connect one.") }
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

/** API keys: listed and revocable here; created in the web console, which can show the secret once. */
@Composable
fun ApiKeys(model: LedgerModel) {
    Load(model, "api-keys", { it.request("GET", "/api-keys") }) { data ->
        Page {
            if (data.rows("keys").isEmpty()) item { Empty("No API keys yet. Create one in the web console under Agents › API keys.") }
            items(data.rows("keys"), key = { it.text("id") }) { key ->
                val revoked = key.text("revoked_at").isNotBlank()
                Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    SummaryCard(key.text("name"), if (revoked) "Revoked ${displayTime(key.text("revoked_at"))}" else "Dispatch research",
                        "Key ${key.text("prefix")}…\nCreated: ${displayTime(key.text("created_at"))}\nLast used: ${displayTime(key.text("last_used_at")).ifBlank { "Never" }}")
                    if (!revoked) ConfirmButton("Revoke key", "Revoke ${key.text("name")}? It stops working immediately.", !model.busy) {
                        model.act("API key revoked") { it.request("DELETE", "/api-keys/${segment(key.text("id"))}") }
                    }
                }
            }
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
