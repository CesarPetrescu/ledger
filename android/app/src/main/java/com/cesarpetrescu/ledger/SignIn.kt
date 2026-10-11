package com.cesarpetrescu.ledger

import android.content.Context
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import androidx.credentials.CreatePublicKeyCredentialRequest
import androidx.credentials.CreatePublicKeyCredentialResponse
import androidx.credentials.CredentialManager
import androidx.credentials.GetCredentialRequest
import androidx.credentials.GetPublicKeyCredentialOption
import androidx.credentials.PublicKeyCredential
import androidx.credentials.exceptions.*
import androidx.credentials.exceptions.domerrors.DomError
import androidx.credentials.exceptions.domerrors.InvalidStateError
import androidx.credentials.exceptions.domerrors.NotAllowedError
import androidx.credentials.exceptions.domerrors.SecurityError
import androidx.credentials.exceptions.publickeycredential.CreatePublicKeyCredentialDomException
import androidx.credentials.exceptions.publickeycredential.GetPublicKeyCredentialDomException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import org.json.JSONObject

const val NO_PASSKEY_PROVIDER = "This phone has no passkey provider turned on. Turn on Google Password Manager, or on Android 14 or newer Bitwarden, as a passkey provider in Android's settings."

/**
 * The phone's passkey providers (Bitwarden, Google Password Manager) through Android's Credential Manager.
 * Options and results are the standard WebAuthn JSON the server sends and reads. [context] must be the
 * activity: the provider shows its sheet over it.
 */
class Passkeys(private val context: Context) {
    private val manager by lazy { CredentialManager.create(context) }

    /** Answers the server's request options with a passkey the owner unlocks; returns the PublicKeyCredential JSON. */
    suspend fun get(options: JSONObject): JSONObject = provider {
        val credential = manager.getCredential(context, GetCredentialRequest(listOf(GetPublicKeyCredentialOption(options.toString())))).credential
        JSONObject((credential as? PublicKeyCredential ?: throw IllegalStateException("The provider did not return a passkey.")).authenticationResponseJson)
    }

    /** Creates a passkey from the server's creation options; returns the PublicKeyCredential JSON. */
    suspend fun create(options: JSONObject): JSONObject = provider {
        val response = manager.createCredential(context, CreatePublicKeyCredentialRequest(options.toString()))
        JSONObject((response as? CreatePublicKeyCredentialResponse ?: throw IllegalStateException("The provider did not create a passkey.")).registrationResponseJson)
    }

    // The sheet opens over the activity, so ask from the main thread; a provider's error becomes the sentence the owner reads.
    private suspend fun provider(call: suspend () -> JSONObject) = withContext(Dispatchers.Main) {
        try { call() }
        catch (e: GetCredentialException) { throw IllegalStateException(passkeyProblem(e)) }
        catch (e: CreateCredentialException) { throw IllegalStateException(passkeyProblem(e)) }
    }
}

/** What went wrong with a passkey request, in words the owner can act on. */
fun passkeyProblem(error: Exception): String = when (error) {
    is GetCredentialCancellationException, is CreateCredentialCancellationException -> "Passkey request cancelled."
    is NoCredentialException -> "No passkey for this Ledger server on this phone. Check that Bitwarden or Google Password Manager holds it and is turned on as a passkey provider."
    is GetCredentialProviderConfigurationException, is CreateCredentialProviderConfigurationException,
    is GetCredentialUnsupportedException, is CreateCredentialUnsupportedException, is CreateCredentialNoCreateOptionException -> NO_PASSKEY_PROVIDER
    is GetCredentialInterruptedException, is CreateCredentialInterruptedException -> "The passkey request was interrupted. Try again."
    is GetPublicKeyCredentialDomException -> domProblem(error.domError)
    is CreatePublicKeyCredentialDomException -> domProblem(error.domError)
    else -> "The passkey provider could not complete the request. Try again."
}

private fun domProblem(error: DomError) = when (error) {
    // Android checks the server's /.well-known/assetlinks.json before a provider may use the passkey in this app.
    is SecurityError -> "Android could not link this app to the server, so passkeys cannot be used here. The server's assetlinks.json must list this app (LEDGER_ANDROID_APPS)."
    is InvalidStateError -> "This provider already holds a Ledger passkey. Use that one, or remove it first."
    is NotAllowedError -> "The passkey provider declined or timed out. Try again."
    else -> "The passkey provider could not complete the request. Try again."
}

@Composable
fun rememberPasskeys(): Passkeys {
    val context = LocalContext.current
    return remember(context) { Passkeys(context) }
}

/** How a session signed in, as the server records it. */
fun signInMethod(method: String) = when (method) {
    "password" -> "Password"
    "passkey" -> "Passkey"
    "password+passkey" -> "Password and passkey"
    else -> label(method)
}

/** A signed-in device's card: how it signed in and whether it is remembered, then when it signed in, was last active, and expires. */
fun sessionSummary(session: JSONObject): Pair<String, String> =
    "${signInMethod(session.text("method"))} · ${if (session.optBoolean("remember")) "Remembered" else "12 hours"}" to
        listOf("Signed in" to "created_at", "Last active" to "last_seen_at", "Expires" to "expires_at")
            .joinToString("\n") { (name, key) -> "$name: ${displayTime(session.text(key))}" }

/** The notice after adding a passkey; the server signs out the other devices that had signed in with only the password. */
fun passkeyAdded(first: Boolean, revoked: Int) = buildString {
    append("Passkey added")
    if (first) append(". Signing in now needs it.")
    if (revoked > 0) append("${if (first) "" else "."} Signed out ${plural(revoked, "other device")} that used only the password.")
}

fun passkeyDetails(passkey: JSONObject) =
    "Added: ${displayTime(passkey.text("created_at"))}\nLast used: ${displayTime(passkey.text("last_used_at")).ifBlank { "Never" }}"

/** Asks for the owner password before a change to how the console is signed in to, so a stolen session cookie alone cannot make it. */
@Composable
fun PasswordDialog(title: String, explanation: String, confirm: String, dismiss: () -> Unit, danger: Boolean = false, action: (String) -> Unit) {
    // Held only while the dialog is open, never in saved state.
    var password by remember { mutableStateOf("") }
    AlertDialog(onDismissRequest = dismiss, title = { Text(title) }, text = {
        Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Text(explanation)
            OutlinedTextField(password, { password = it }, label = { Text("Owner password") }, singleLine = true,
                visualTransformation = PasswordVisualTransformation(), keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password))
        }
    }, confirmButton = {
        TextButton(enabled = password.isNotEmpty(), onClick = { dismiss(); action(password) }) {
            Text(confirm, color = if (danger) MaterialTheme.colorScheme.error else Color.Unspecified)
        }
    }, dismissButton = { TextButton(onClick = dismiss) { Text("Cancel") } })
}

/** Settings › Access › Passkeys: once there is one, signing in anywhere needs it. */
@Composable
fun PasskeysScreen(model: LedgerModel) {
    val passkeys = rememberPasskeys()
    var adding by remember { mutableStateOf(false) }
    var renaming by remember { mutableStateOf<JSONObject?>(null) }
    var removing by remember { mutableStateOf<JSONObject?>(null) }
    Load(model, "passkeys", { it.request("GET", "/passkeys") }) { data ->
        val rows = data.rows("passkeys")
        Page {
            item {
                Text(if (data.optBoolean("required")) "Signing in needs a passkey. The password alone no longer opens the console or approves apps."
                    else "No passkeys yet, so the password alone signs in. Once you add one, signing in needs it.")
            }
            items(rows, key = { it.text("id") }) { passkey ->
                SummaryCard(passkey.text("name"), passkey.text("provider"), passkeyDetails(passkey), actions = {
                    OutlinedButton(onClick = { renaming = passkey }, enabled = !model.busy) { Text("Rename") }
                    OutlinedButton(onClick = { removing = passkey }, enabled = !model.busy,
                        colors = ButtonDefaults.outlinedButtonColors(contentColor = MaterialTheme.colorScheme.error)) { Text("Remove") }
                })
            }
            item { Button(onClick = { adding = true }, enabled = !model.busy) { Text("Add a passkey") } }
            item {
                Text("Passkeys work with Google Password Manager on any supported Android, and with Bitwarden or other providers on Android 14 or newer. A passkey saved in Bitwarden also works in the web console.",
                    style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        }
        if (adding) PasswordDialog("Add a passkey", "Enter the owner password, then choose where to save the passkey.", "Continue", { adding = false }) { password ->
            val first = rows.isEmpty()
            var revoked = 0
            model.act(after = { model.notice = passkeyAdded(first, revoked) }) { client ->
                val start = client.request("POST", "/passkeys/options", json("password" to password))
                revoked = client.request("POST", "/passkeys", json("challenge_id" to start.getString("challenge_id"), "credential" to passkeys.create(start.getJSONObject("options")))).optInt("revoked")
            }
        }
        removing?.let { passkey ->
            val last = rows.size == 1
            PasswordDialog("Remove passkey",
                if (last) "${passkey.text("name")} is your last passkey. Without it the password alone signs in again, here, on the web, and when approving apps."
                else "Remove ${passkey.text("name")}? It can no longer sign in. Delete it from your passkey provider too.",
                "Remove", { removing = null }, danger = true) { password ->
                model.act(if (last) "Passkey removed. The password alone signs in again." else "Passkey removed") {
                    it.request("DELETE", "/passkeys/${segment(passkey.text("id"))}", json("password" to password))
                }
            }
        }
        renaming?.let { passkey ->
            var name by remember(passkey) { mutableStateOf(passkey.text("name")) }
            AlertDialog(onDismissRequest = { renaming = null }, title = { Text("Rename passkey") }, text = { Field("Name", name, { name = it }, max = 60) },
                confirmButton = {
                    TextButton(enabled = name.isNotBlank(), onClick = {
                        renaming = null
                        model.act("Passkey renamed") { it.request("PATCH", "/passkeys/${segment(passkey.text("id"))}", json("name" to name.trim())) }
                    }) { Text("Save") }
                }, dismissButton = { TextButton(onClick = { renaming = null }) { Text("Cancel") } })
        }
    }
}

/** Settings › Access › Signed-in devices: every browser and phone with an owner session. */
@Composable
fun SignedInDevices(model: LedgerModel) {
    Load(model, "devices", { it.request("GET", "/sessions") }) { data ->
        // This phone first; the rest keep the server's order.
        val sessions = data.rows("sessions").sortedByDescending { it.optBoolean("current") }
        Page {
            item { Text("Remembered devices stay signed in for 30 days after their last use, 90 days at most; others for 12 hours. Sign out any you do not recognise.") }
            items(sessions, key = { it.text("id") }) { session ->
                val name = session.text("label").ifBlank { "Unknown device" }
                val (subtitle, body) = sessionSummary(session)
                if (session.optBoolean("current")) SummaryCard(name, subtitle, body, tags = listOf("This device" to Tone.Accent), actions = {
                    ConfirmButton("Sign out", "Sign out and revoke this phone's owner session?", !model.busy, action = model::logout)
                }) else SummaryCard(name, subtitle, body, actions = {
                    ConfirmButton("Sign out", "Sign out $name? It will need to sign in again.", !model.busy, danger = true) {
                        model.act("Signed out $name") { it.request("DELETE", "/sessions/${segment(session.text("id"))}") }
                    }
                })
            }
            if (sessions.any { !it.optBoolean("current") }) item {
                ConfirmButton("Sign out everywhere else", "Sign out every other browser and phone? This phone stays signed in.", !model.busy, danger = true) {
                    var revoked = 0
                    model.act(after = { model.notice = "Signed out ${plural(revoked, "other device")}" }) {
                        revoked = it.request("POST", "/sessions/revoke-others").optInt("revoked")
                    }
                }
            }
        }
    }
}
