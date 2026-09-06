package com.cesarpetrescu.ledger

import org.json.JSONArray
import org.json.JSONObject
import java.io.InputStream
import java.io.OutputStream
import java.net.HttpCookie
import java.net.URI
import java.net.URLEncoder
import javax.net.ssl.HttpsURLConnection

fun serverOrigin(input: String): String {
    val uri = try { URI(input.trim()) } catch (_: Exception) { throw IllegalArgumentException("Enter a valid HTTPS server address.") }
    require(uri.scheme.equals("https", ignoreCase = true) && !uri.host.isNullOrBlank() &&
        uri.rawUserInfo == null && uri.rawQuery == null && uri.rawFragment == null &&
        (uri.rawPath.isNullOrEmpty() || uri.rawPath == "/") && (uri.port == -1 || uri.port in 1..65535)) {
        "Use an HTTPS server address without a path, password, or query."
    }
    return "https://${uri.host.lowercase()}${if (uri.port != -1 && uri.port != 443) ":${uri.port}" else ""}"
}

fun validProjectSlug(value: String) = value.matches(Regex("[a-z0-9][a-z0-9-]{1,63}"))
fun validFieldText(value: String, max: Int, multiline: Boolean) =
    value.codePointCount(0, value.length) <= max && (multiline || !value.contains('\n') && !value.contains('\r'))
fun canRetarget(work: String, claimed: Boolean = false) = !claimed && work in listOf("draft", "ready")
// Ten worst-case JSON-escaped 100,000-rune messages fit within the 8 MiB response cap.
fun handoffPath(id: String, before: String = "") = "/handoffs/${segment(id)}?messages=10&before=${segment(before)}"

fun segment(value: String): String = URLEncoder.encode(value, Charsets.UTF_8.name()).replace("+", "%20")
fun json(vararg fields: Pair<String, Any?>) = JSONObject().apply { fields.forEach { (k, v) -> put(k, v ?: JSONObject.NULL) } }
fun JSONObject.text(key: String) = if (isNull(key)) "" else optString(key)
fun JSONObject.rows(key: String): List<JSONObject> = optJSONArray(key)?.let { array -> (0 until array.length()).map { array.getJSONObject(it) } } ?: emptyList()
fun JSONObject.strings(key: String): List<String> = optJSONArray(key)?.let { array -> (0 until array.length()).map { array.getString(it) } } ?: emptyList()
fun sessionCookie(headers: List<String>): String = headers.flatMap { HttpCookie.parse(it) }
    .firstOrNull { it.name == "ledger_admin_session" && it.secure && it.path == "/admin" && it.value.matches(Regex("[A-Za-z0-9_-]{43}")) }
    ?.let { "${it.name}=${it.value}" } ?: throw IllegalStateException("The server did not return a valid session cookie.")

class ApiError(val status: Int, message: String) : Exception(message)
class Api(val origin: String, val cookie: String = "", val csrf: String = "") {
    // Each client belongs to one immutable session. Never follow redirects with credentials.
    private fun connect(method: String, path: String): HttpsURLConnection {
        require(path.startsWith("/") && !path.startsWith("//") && !path.contains('#'))
        return (URI("$origin/admin/api$path").toURL().openConnection() as HttpsURLConnection).apply {
            requestMethod = method
            instanceFollowRedirects = false
            connectTimeout = 15_000
            readTimeout = 30_000
            useCaches = false
            setRequestProperty("Accept", "application/json")
            if (cookie.isNotEmpty()) setRequestProperty("Cookie", cookie)
            if (method != "GET") {
                setRequestProperty("Origin", origin)
                if (csrf.isNotEmpty()) setRequestProperty("X-CSRF-Token", csrf)
            }
        }
    }

    fun request(method: String, path: String, body: JSONObject? = null): JSONObject = exchange(method, path, body).first

    private fun exchange(method: String, path: String, body: JSONObject?): Pair<JSONObject, Map<String?, List<String>>> {
        val connection = connect(method, path)
        try {
            if (method != "GET") {
                val bytes = (body ?: JSONObject()).toString().toByteArray()
                val limit = when {
                    path == "/login" -> 8 * 1024
                    method == "POST" && (path == "/handoffs" || path.startsWith("/handoffs/") && path.endsWith("/messages")) -> 1024 * 1024
                    else -> 64 * 1024
                }
                require(bytes.size <= limit) { "This form is too large for the server. Shorten its text and try again." }
                connection.setRequestProperty("Content-Type", "application/json")
                connection.doOutput = true
                connection.setFixedLengthStreamingMode(bytes.size)
                connection.outputStream.use { it.write(bytes) }
            }
            checkResponse(connection)
            val bytes = connection.inputStream.use { it.readBounded(8 * 1024 * 1024) }
            return (if (bytes.isEmpty()) JSONObject() else JSONObject(String(bytes, Charsets.UTF_8))) to connection.headerFields
        } finally { connection.disconnect() }
    }

    fun login(password: String): Api {
        val (data, headers) = exchange("POST", "/login", json("password" to password))
        val token = data.getString("csrf_token")
        require(token.matches(Regex("[A-Za-z0-9_-]{43}"))) { "Invalid session response." }
        val cookies = headers.filterKeys { it.equals("Set-Cookie", ignoreCase = true) }.values.flatten()
        return Api(origin, sessionCookie(cookies), token)
    }

    fun download(path: String, destination: OutputStream) {
        val connection = connect("GET", path)
        try {
            checkResponse(connection)
            // Exports include every message; keep memory bounded while streaming to the selected document.
            connection.inputStream.use { it.copyTo(destination) }
        } finally { connection.disconnect() }
    }

    fun upload(messageId: String, filename: String, bytes: ByteArray): JSONObject {
        require(bytes.isNotEmpty() && bytes.size <= 25 * 1024 * 1024) { "Files must contain 1 byte to 25 MiB." }
        val boundary = "ledger-${java.util.UUID.randomUUID()}"
        val safeName = filename.substringAfterLast('/').substringAfterLast('\\').replace(Regex("[\\r\\n\\\"\\p{Cntrl}]"), "_").take(200).ifBlank { "attachment" }
        val prefix = "--$boundary\r\nContent-Disposition: form-data; name=\"file\"; filename=\"$safeName\"\r\nContent-Type: application/octet-stream\r\n\r\n".toByteArray()
        val suffix = "\r\n--$boundary--\r\n".toByteArray()
        val connection = connect("POST", "/handoff-messages/${segment(messageId)}/files")
        try {
            connection.doOutput = true
            connection.setRequestProperty("Content-Type", "multipart/form-data; boundary=$boundary")
            connection.setFixedLengthStreamingMode(prefix.size + bytes.size + suffix.size)
            connection.outputStream.use { it.write(prefix); it.write(bytes); it.write(suffix) }
            checkResponse(connection)
            return connection.inputStream.use { JSONObject(String(it.readBounded(64 * 1024), Charsets.UTF_8)) }
        } finally { connection.disconnect() }
    }

    private fun checkResponse(connection: HttpsURLConnection) {
        val status = connection.responseCode
        if (status in 200..299) return
        val message = if (status in 400..499) runCatching {
            connection.errorStream?.use { JSONObject(String(it.readBounded(16 * 1024), Charsets.UTF_8)).text("error") }
        }.getOrNull()?.take(500) else null
        throw ApiError(status, when {
            status == 401 -> "Session expired or password incorrect. Sign in again."
            status == 412 -> "This event changed elsewhere. Go back and reopen it before saving."
            status in 300..399 -> "The server redirected this request. Enter its final HTTPS address."
            !message.isNullOrBlank() -> message
            else -> "The server could not complete the request ($status)."
        })
    }
}

fun InputStream.readBounded(limit: Int): ByteArray {
    val output = java.io.ByteArrayOutputStream()
    val buffer = ByteArray(8192)
    while (true) {
        val count = read(buffer)
        if (count == -1) break
        require(output.size() + count <= limit) { "The response or file is too large." }
        output.write(buffer, 0, count)
    }
    return output.toByteArray()
}

fun messageActions(work: String, delivery: String): List<String> = buildList {
    if (delivery == "unseen" && work != "draft") add("acknowledge")
    addAll(when (work) {
        "draft" -> listOf("publish")
        "ready" -> listOf("claim")
        "in_progress" -> listOf("block", "complete", "release")
        "blocked" -> listOf("complete", "release")
        "done" -> listOf("reopen")
        else -> emptyList()
    })
}
