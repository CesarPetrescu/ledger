package com.cesarpetrescu.ledger

import androidx.credentials.exceptions.CreateCredentialCancellationException
import androidx.credentials.exceptions.CreateCredentialNoCreateOptionException
import androidx.credentials.exceptions.GetCredentialCancellationException
import androidx.credentials.exceptions.GetCredentialProviderConfigurationException
import androidx.credentials.exceptions.GetCredentialUnsupportedException
import androidx.credentials.exceptions.NoCredentialException
import androidx.credentials.exceptions.domerrors.InvalidStateError
import androidx.credentials.exceptions.domerrors.SecurityError
import androidx.credentials.exceptions.publickeycredential.CreatePublicKeyCredentialDomException
import androidx.credentials.exceptions.publickeycredential.GetPublicKeyCredentialDomException
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test

class SignInTest {
    // The pattern the Android fixture server (android/test/server.py) accepts at /login.
    private val fixtureAgent = Regex("""LedgerAndroid/\S+ \(Android [^;()]+; [^;()]+\)""")

    @Test fun userAgentNamesTheAppAsOneParsableHeaderValue() {
        assertEquals("LedgerAndroid/0.20.0 (Android 16; Pixel 9)", userAgent("0.20.0", "16", "Pixel 9"))
        // Brackets and semicolons would break the server's parsing; control and non-ASCII characters are not valid header text.
        assertEquals("LedgerAndroid/0.20.0-debug (Android 9; Galaxy S10 5G)", userAgent("0.20.0-debug", "9", "Galaxy S10 (5G);"))
        assertEquals("LedgerAndroid/1.0 (Android unknown; Mate 60)", userAgent("1.0", "", "华为 Mate 60"))
        val odd = userAgent("1.0 beta", " ", "Pixel\r\nX-Injected: 1")
        assertFalse(odd.contains('\r') || odd.contains('\n'))
        listOf(userAgent("0.20.0", "16", "Pixel 9"), userAgent("0.20.0-debug", "9", "Galaxy S10 (5G);"), odd, userAgent("", "", ""))
            .forEach { assertTrue(it, fixtureAgent.matches(it)) }
    }

    @Test fun passwordStepIsASessionUntilAPasskeyExists() {
        assertNull(passkeyStep(JSONObject("""{"csrf_token":"x","expires_at":"2030-01-01T00:00:00Z","remember":true}""")))
        assertNull(passkeyStep(JSONObject("""{"passkey_required":false}""")))
        val step = passkeyStep(JSONObject("""{"passkey_required":true,"pending":"p-1","options":{"challenge":"Y2hhbGxlbmdl","rpId":"ctx.example.com","allowCredentials":[],"userVerification":"required","timeout":300000}}"""))
        assertNotNull(step)
        assertEquals("p-1", step!!.pending)
        // The options go to Credential Manager exactly as the server sent them.
        assertEquals("ctx.example.com", step.options.getString("rpId"))
        assertEquals("required", step.options.getString("userVerification"))
    }

    @Test fun passkeySignInSendsTheCredentialObjectAndAlwaysRemembers() {
        val credential = JSONObject("""{"id":"Y3JlZA","rawId":"Y3JlZA","type":"public-key","response":{"clientDataJSON":"e30","authenticatorData":"AA","signature":"AA","userHandle":"b3duZXI"},"clientExtensionResults":{}}""")
        val afterPassword = passkeySignIn("pending", "p-1", credential)
        assertEquals("p-1", afterPassword.getString("pending"))
        assertFalse(afterPassword.has("challenge_id"))
        assertTrue(afterPassword.getBoolean("remember"))
        // A JSON object, not a string holding JSON: the server decodes it as PublicKeyCredentialJSON.
        assertEquals("public-key", afterPassword.getJSONObject("credential").getString("type"))
        assertEquals("b3duZXI", afterPassword.getJSONObject("credential").getJSONObject("response").getString("userHandle"))
        val alone = passkeySignIn("challenge_id", "c-1", credential)
        assertEquals("c-1", alone.getString("challenge_id"))
        assertFalse(alone.has("pending"))
    }

    @Test fun signedInDevicesAndPasskeysReadPlainly() {
        assertEquals(listOf("Password", "Passkey", "Password and passkey"), listOf("password", "passkey", "password+passkey").map(::signInMethod))
        val laptop = JSONObject("""{"id":"7d1f","label":"Firefox on Linux","method":"password+passkey","remember":false,"current":false,
            "created_at":"2026-09-06T07:00:00Z","last_seen_at":"2026-09-06T07:30:00Z","expires_at":"2026-09-06T19:00:00Z"}""")
        val (subtitle, body) = sessionSummary(laptop)
        assertEquals("Password and passkey · 12 hours", subtitle)
        assertEquals("Signed in: ${displayTime("2026-09-06T07:00:00Z")}\nLast active: ${displayTime("2026-09-06T07:30:00Z")}\nExpires: ${displayTime("2026-09-06T19:00:00Z")}", body)
        assertEquals("Passkey · Remembered", sessionSummary(JSONObject("""{"method":"passkey","remember":true}""")).first)
        val unused = JSONObject("""{"id":"1","name":"Bitwarden","provider":"Bitwarden","created_at":"2026-10-01T09:00:00Z","last_used_at":null}""")
        assertEquals("Added: ${displayTime("2026-10-01T09:00:00Z")}\nLast used: Never", passkeyDetails(unused))
        assertTrue(passkeyDetails(unused.put("last_used_at", "2026-10-02T09:00:00Z")).endsWith("Last used: ${displayTime("2026-10-02T09:00:00Z")}"))
        assertEquals("Passkey added. Signing in now needs it.", passkeyAdded(first = true, revoked = 0))
        assertEquals("Passkey added. Signing in now needs it. Signed out 2 other devices that used only the password.", passkeyAdded(first = true, revoked = 2))
        assertEquals("Passkey added. Signed out 1 other device that used only the password.", passkeyAdded(first = false, revoked = 1))
        assertEquals("Passkey added", passkeyAdded(first = false, revoked = 0))
        // A passkey still named after its provider does not repeat it as the subtitle.
        assertEquals("", passkeyProvider(unused))
        assertEquals("Bitwarden", passkeyProvider(JSONObject("""{"name":"Laptop vault","provider":"Bitwarden"}""")))
        assertEquals("", passkeyProvider(JSONObject("""{"name":"Passkey","provider":""}""")))
        assertEquals("Passkeys", screenTitle("passkeys"))
        assertEquals("Signed-in devices", screenTitle("devices"))
    }

    @Test fun passkeyProviderErrorsSayWhatToDo() {
        // No provider. Android 14+ has the provider setting, where Bitwarden or Google Password Manager is turned on.
        val modern = noPasskeyProvider(34)
        listOf("Bitwarden", "Google Password Manager", "Android's settings").forEach { assertTrue(it, modern.contains(it)) }
        // Older Androids have no such setting: passkeys go through Play services and Google Password Manager only.
        val older = noPasskeyProvider(33)
        listOf("Google Play services", "Google Password Manager", "Bitwarden passkeys need Android 14").forEach { assertTrue(it, older.contains(it)) }
        assertFalse(older.contains("settings"))
        listOf(GetCredentialProviderConfigurationException(), GetCredentialUnsupportedException(), CreateCredentialNoCreateOptionException()).forEach {
            assertEquals(modern, passkeyProblem(it, sdk = 35))
            assertEquals(older, passkeyProblem(it, sdk = 28))
        }
        assertEquals("Passkey request cancelled.", passkeyProblem(GetCredentialCancellationException()))
        assertEquals("Passkey request cancelled.", passkeyProblem(CreateCredentialCancellationException()))
        assertTrue(passkeyProblem(NoCredentialException()).startsWith("No passkey for this Ledger server"))
        // An app the server's assetlinks.json does not list cannot use the server's passkeys.
        assertTrue(passkeyProblem(GetPublicKeyCredentialDomException(SecurityError())).contains("assetlinks.json"))
        assertTrue(passkeyProblem(CreatePublicKeyCredentialDomException(SecurityError())).contains("LEDGER_ANDROID_APPS"))
        assertTrue(passkeyProblem(CreatePublicKeyCredentialDomException(InvalidStateError())).contains("already holds a Ledger passkey"))
    }

    @Test fun longNoticesStayLongEnoughToRead() {
        // Errors such as the missing-provider one are too long to read in a 4-second snackbar.
        assertTrue(longNotice(noPasskeyProvider(34), undoable = false))
        assertTrue(longNotice(noPasskeyProvider(28), undoable = false))
        assertTrue(longNotice("Archived", undoable = true))
        assertFalse(longNotice("Saved", undoable = false))
        assertFalse(longNotice("Signed out 1 other device", undoable = false))
    }

    @Test fun aBlankServerAddressAsksForOne() {
        listOf("", "   ").forEach {
            assertEquals("Enter the server address first.", assertThrows(IllegalArgumentException::class.java) { serverOrigin(it) }.message)
        }
    }
}
