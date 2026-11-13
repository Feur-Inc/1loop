// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import org.json.JSONObject

// Install Chromium and Install Claude Code on a server without them (the
// slim Docker image, issue #406): the server downloads them into its data
// folder (internal/server/install.go); this follows the install in
// /api/state. Shown only for what the server says it lacks.

// app.extensionsCall is the target's plain GET/POST, whatever the route.
private class ServerInstallItem(val what: String, val field: String, val title: String, val button: String, val detail: String)

private val serverInstallItems = listOf(
    ServerInstallItem("chromium", "browser", "Chromium", "Install Chromium",
        "For Browser Use: the agent's browser tabs run on the server. It downloads Chromium (about 170 MB, checked against Google's checksum) into its data folder, with the system libraries it needs from Debian's signed packages."),
    ServerInstallItem("claude-code", "claudeCode", "Claude Code", "Install Claude Code",
        "For Claude Code connections. The server downloads Anthropic's claude command into its data folder, checked against Anthropic's release signature and checksum."),
)

@Composable
fun ColumnScope.ServerInstallSection(app: RemoteApp) {
    val scope = rememberCoroutineScope()
    var state by remember(app.server) { mutableStateOf<JSONObject?>(null) }
    var failure by remember(app.server) { mutableStateOf<String?>(null) }
    var poke by remember { mutableIntStateOf(0) }
    LaunchedEffect(app.server, poke) {
        while (true) {
            state = try { app.extensionsCall("/api/state") } catch (cancelled: CancellationException) { throw cancelled } catch (_: Exception) { null }
            val installs = state?.optJSONObject("installs")
            if (serverInstallItems.none { installs?.optJSONObject(it.what)?.optBoolean("running") == true }) break
            delay(1000)
        }
    }
    val current = state ?: return
    val installs = current.optJSONObject("installs")
    for (item in serverInstallItems) {
        val job = installs?.optJSONObject(item.what)
        val running = job?.optBoolean("running") == true
        if (!running && (!current.has(item.field) || current.optBoolean(item.field))) continue
        Text(item.title + " on the server", style = Type.title)
        Text(item.detail, style = Type.small)
        val error = job?.optString("error").orEmpty()
        Pill(
            if (running) "Installing…" else if (error.isNotEmpty()) "Try again" else item.button,
            primary = true, enabled = app.connected && !running,
        ) {
            scope.launch {
                failure = try { app.extensionsCall("/api/install/" + item.what, JSONObject()); null }
                catch (cancelled: CancellationException) { throw cancelled }
                catch (problem: Exception) { problem.message ?: "The server could not start the install." }
                poke++
            }
        }
        when {
            running -> Text(job?.optString("text").orEmpty(), style = Type.small)
            error.isNotEmpty() -> Text("The install failed: $error", style = Type.small.copy(color = Palette.danger))
        }
    }
    failure?.let { Text(it, style = Type.small.copy(color = Palette.danger)) }
}
