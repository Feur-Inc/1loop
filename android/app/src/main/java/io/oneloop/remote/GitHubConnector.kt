package io.oneloop.remote

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.net.Uri
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import org.json.JSONObject

@Composable
fun GitHubConnectorSection(app: RemoteApp) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var state by remember(app.server) { mutableStateOf<JSONObject?>(null) }
    var problem by remember(app.server) { mutableStateOf<String?>(null) }
    var busy by remember(app.server) { mutableStateOf(false) }
    var clientId by remember(app.server) { mutableStateOf("") }

    suspend fun refresh() {
        try {
            state = app.extensionsCall("/api/github")
            problem = null
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (failure: Exception) {
            problem = failure.message ?: "Could not read the GitHub connector."
        }
    }
    fun change(action: String, body: JSONObject = JSONObject()) {
        busy = true
        scope.launch {
            try {
                state = app.extensionsCall("/api/github/$action", body)
                problem = null
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (failure: Exception) {
                problem = failure.message ?: "Could not change the GitHub connector."
            } finally {
                busy = false
            }
        }
    }
    fun open(address: String) {
        try {
            context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(address)))
        } catch (_: android.content.ActivityNotFoundException) {
            problem = "No browser is available to open GitHub."
        }
    }
    LaunchedEffect(app.server) { refresh() }
    val code = state?.optString("code").orEmpty()
    LaunchedEffect(app.server, code) {
        while (code.isNotEmpty()) {
            delay(1000)
            refresh()
        }
    }
    Text("Connectors · GitHub", style = Type.headline)
    Text("Connect the account whose repositories the agent may use. GitHub tools are offered only while this connector is connected and enabled, and follow the current approval mode.", style = Type.small)
    problem?.let { Text(it, style = Type.small.copy(color = Palette.danger)) }
    val view = state
    if (view == null) {
        Pill("Refresh GitHub", enabled = app.connected && !busy) { scope.launch { refresh() } }
        return
    }
    view.optString("error").takeIf { it.isNotEmpty() }?.let {
        Text(it, style = Type.small.copy(color = Palette.danger))
        Pill("Cancel sign-in", enabled = !busy) { change("cancel") }
    }
    if (!view.optBoolean("configured")) {
        Text("Publisher setup: register 1loop's GitHub App with Device flow enabled and enter its public client ID. Never enter an app secret or private key.", style = Type.small)
        BasicTextField(
            clientId,
            { clientId = it },
            singleLine = true,
            textStyle = Type.body,
            modifier = Modifier.fillMaxWidth()
                .border(1.dp, Palette.borderSoft, RoundedCornerShape(12.dp))
                .padding(12.dp),
            decorationBox = { inner ->
                if (clientId.isEmpty()) Text("Public GitHub App client ID", style = Type.small)
                inner()
            },
        )
        Pill("Save client ID", primary = true, enabled = !busy && clientId.isNotBlank()) {
            change("configure", JSONObject().put("clientId", clientId.trim()))
        }
        Pill("Register a GitHub App") { open("https://github.com/settings/apps/new") }
    } else if (view.optBoolean("connected")) {
        Text("Connected as @${view.optString("login")}", style = Type.body)
        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.SpaceBetween) {
            Text("Enable GitHub tools", style = Type.body)
            Toggle(view.optBoolean("enabled"), enabled = app.connected && !busy) {
                change("enable", JSONObject().put("enabled", it))
            }
        }
        Pill("Review access on GitHub") {
            open("https://github.com/settings/apps/authorizations")
        }
        Pill("Manage repository access") { open(view.optString("installUrl")) }
        Pill("Disconnect", enabled = !busy) { change("disconnect") }
        Text("Disconnect removes authorization stored on this execution target. Revoke the app separately in GitHub settings if needed. Tokens are not sent to the model.", style = Type.small)
    } else if (code.isNotEmpty()) {
        Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Text("Open GitHub, enter this code and review the requested access:", style = Type.small)
            Text(code, style = Type.title)
            Pill("Copy code") {
                context.getSystemService(ClipboardManager::class.java)
                    .setPrimaryClip(ClipData.newPlainText("GitHub sign-in code", code))
            }
            Pill("Open GitHub", primary = true) { open(view.optString("url")) }
            Pill("Cancel sign-in", enabled = !busy) { change("cancel") }
        }
    } else if (view.optString("error").isEmpty()) {
        Text("Not connected. GitHub tools are unavailable to the model.", style = Type.small)
        Pill("Choose repositories on GitHub") { open(view.optString("installUrl")) }
        Pill(if (busy) "Connecting…" else "Connect GitHub", primary = true, enabled = app.connected && !busy) {
            change("connect")
        }
    }
}
