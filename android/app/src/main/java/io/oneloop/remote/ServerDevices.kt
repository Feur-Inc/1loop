// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.AlertDialog
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
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch
import org.json.JSONObject
import java.net.URLEncoder

// Unpairing (#428): forgetting a paired server unpairs this phone there
// first (its token stops working), and the headless server open now lists
// the devices paired with it in Settings → General, each with Remove. Both
// ask first. The server's side is internal/server/security.go.

/** A device paired with a headless server, as GET /api/devices lists it; current is this phone. */
data class ServerDevice(val id: String, val name: String, val added: String, val current: Boolean)

fun serverDevices(o: JSONObject): List<ServerDevice> =
    o.optJSONArray("devices").let { a ->
        (0 until (a?.length() ?: 0)).map { a!!.getJSONObject(it).let { d -> ServerDevice(d.optString("id"), d.optString("name"), d.optString("added").take(10), d.optBoolean("current")) } }
    }

fun revokePath(id: String) = "/api/devices/${URLEncoder.encode(id, "UTF-8")}/revoke"

/** Unpairs this phone from a headless server: it finds itself among the paired devices and revokes its own token. */
fun unpairFrom(api: Api) {
    val me = serverDevices(api.get("/api/devices")).firstOrNull { it.current } ?: return
    api.post(revokePath(me.id), JSONObject())
}

@Composable
fun ConfirmDialog(title: String, text: String, verb: String, onDismiss: () -> Unit, onConfirm: () -> Unit) {
    GlassBackdrop()
    AlertDialog(
        onDismissRequest = onDismiss,
        containerColor = GlassSurface, modifier = GlassDialogEdge,
        title = { Text(title, style = Type.title) },
        text = { Text(text, style = Type.small) },
        confirmButton = { Pill(verb, primary = true) { onDismiss(); onConfirm() } },
        dismissButton = { Pill("Cancel", onClick = onDismiss) },
    )
}

/** Asks before forgetting a paired computer or server. */
@Composable
fun ForgetServerDialog(app: RemoteApp, target: Server, onDismiss: () -> Unit) {
    val text = if (target.desktop) "${target.name} is forgotten on this phone. To remove this phone on the computer too, use Forget in its Settings → Phone."
    else "This phone is unpaired from ${target.name}: its access stops at once, and using the server again needs a new pairing, approved on a paired device or in the server's console."
    ConfirmDialog("Forget ${target.name}?", text, "Forget", onDismiss) { app.unpair(target) }
}

/** Settings → General: the devices paired with the headless server open now, each with Remove. */
@Composable
fun ServerDevicesSection(app: RemoteApp) {
    val target = app.server?.takeIf { !it.localPhone && !it.desktop } ?: return
    val scope = rememberCoroutineScope()
    var devices by remember(target.key) { mutableStateOf<List<ServerDevice>?>(null) }
    var problem by remember(target.key) { mutableStateOf<String?>(null) }
    var asked by remember(target.key) { mutableStateOf<ServerDevice?>(null) }
    suspend fun reload() {
        try { devices = app.pairedDevices(); problem = null }
        catch (cancelled: CancellationException) { throw cancelled }
        catch (failure: Exception) { problem = failure.message }
    }
    LaunchedEffect(target.key) { reload() }
    Text("Devices paired with ${target.name}", style = Type.title, modifier = Modifier.padding(top = 8.dp))
    Text("Every device that can use this server. Removing one unpairs it at once: it must be paired again to come back.", style = Type.small)
    if (devices == null && problem == null) Text("Loading…", style = Type.small)
    devices?.forEach { device ->
        Row(verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                Text(device.name + if (device.current) " · this phone" else "", style = Type.body.copy(fontWeight = FontWeight.Medium))
                if (device.added.isNotEmpty()) Text("Paired ${device.added}", style = Type.caption)
            }
            Pill("Remove") { asked = device }
        }
    }
    problem?.let { Text(it, style = Type.small.copy(color = Palette.danger)) }
    asked?.let { device ->
        if (device.current) ForgetServerDialog(app, target) { asked = null }
        else ConfirmDialog(
            "Remove ${device.name}?",
            "${device.name} is unpaired from ${target.name}: its access stops at once, and it must be paired again to use the server.",
            "Remove", { asked = null },
        ) {
            scope.launch {
                try { app.revokeServerDevice(device.id); reload() }
                catch (cancelled: CancellationException) { throw cancelled }
                catch (failure: Exception) { problem = failure.message }
            }
        }
    }
}
