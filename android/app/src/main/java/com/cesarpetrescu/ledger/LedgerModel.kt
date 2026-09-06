package com.cesarpetrescu.ledger

import android.app.Application
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.IOException

class LedgerModel(application: Application) : AndroidViewModel(application) {
    private val sessions = SessionStore(application)
    var api by mutableStateOf<Api?>(null); private set
    var reauthRequired by mutableStateOf(false); private set
    var starting by mutableStateOf(true); private set
    var busy by mutableStateOf(false); private set
    var notice by mutableStateOf<String?>(null)
    var revision by mutableStateOf(0); private set
    var stack by mutableStateOf(listOf("home")); private set
    val route get() = stack.last()

    init {
        viewModelScope.launch {
            try { api = withContext(Dispatchers.IO) { sessions.read() } }
            catch (e: Exception) { notice = errorMessage(e) }
            finally { starting = false }
        }
    }

    fun go(route: String) { if (!busy) stack = stack + route }
    fun tab(route: String) { if (!busy) stack = listOf(route) }
    fun back() { if (!busy && stack.size > 1) stack = stack.dropLast(1) }
    fun refresh() { revision++ }
    fun clearNotice() { notice = null }

    fun login(origin: String, password: String) {
        if (busy) return
        busy = true
        viewModelScope.launch {
            try {
                val client = withContext(Dispatchers.IO) {
                    Api(serverOrigin(origin)).login(password).also { fresh ->
                        try { sessions.save(fresh) } catch (e: Exception) {
                            runCatching { fresh.request("POST", "/logout") }
                            throw e
                        }
                    }
                }
                if (!reauthRequired || api?.origin != client.origin) stack = listOf("home")
                api = client
                reauthRequired = false
                notice = null
            } catch (e: Exception) {
                if (e is CancellationException) throw e
                notice = errorMessage(e)
            } finally { busy = false }
        }
    }

    fun act(success: String = "Saved", after: () -> Unit = {}, block: (Api) -> Unit) {
        val client = api ?: return
        if (reauthRequired) return
        if (busy) return
        busy = true
        viewModelScope.launch {
            try {
                withContext(Dispatchers.IO) { block(client) }
                revision++
                notice = success
                busy = false
                after()
            } catch (e: Exception) {
                if (e is CancellationException) throw e
                failed(e, client)
            } finally { busy = false }
        }
    }

    suspend fun failed(error: Exception, client: Api) {
        if (error is ApiError && error.status == 401 && api === client) {
            withContext(Dispatchers.IO) { sessions.clear() }
            reauthRequired = true
        }
        notice = errorMessage(error)
    }

    fun logout() = act("Signed out", after = { api = null; stack = listOf("home") }) { client ->
        try { client.request("POST", "/logout") }
        catch (e: ApiError) { if (e.status != 401) throw e }
        sessions.clear()
    }

    fun forget() {
        if (busy) return
        busy = true
        viewModelScope.launch {
            try {
                withContext(Dispatchers.IO) { sessions.clear() }
                api = null
                reauthRequired = false
                stack = listOf("home")
                notice = "Removed from this phone. The server session will expire automatically."
            } catch (e: Exception) { notice = errorMessage(e) }
            finally { busy = false }
        }
    }
}

fun errorMessage(error: Exception): String = when (error) {
    is ApiError, is IllegalArgumentException, is IllegalStateException -> error.message ?: "Could not complete the request."
    is IOException -> "Connection failed. Check your network and server address, then try again."
    else -> "Could not read the server response. Check that the server is up to date."
}
