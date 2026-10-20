package io.oneloop.remote

import android.content.Context
import android.net.ConnectivityManager
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.text.KeyboardOptions
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
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import org.json.JSONObject
import java.net.Inet4Address

// This phone shared on the network: the desktop or another phone connects to
// its engine as to a headless server (Remote server, by address and
// fingerprint) and pairs with a code this phone's user types.

data class PairedDevice(val name: String)
data class PendingPairing(val id: String, val device: String, val expiresAt: Long)
data class SharingState(val enabled: Boolean, val port: Int, val fingerprint: String, val devices: List<PairedDevice>, val pending: List<PendingPairing>) {
    companion object {
        fun from(o: JSONObject) = SharingState(
            o.optBoolean("enabled"), o.optInt("port"), o.optString("fingerprint"),
            o.optJSONArray("devices").let { a -> (0 until (a?.length() ?: 0)).map { PairedDevice(a!!.getJSONObject(it).optString("name")) } },
            o.optJSONArray("pending").let { a -> (0 until (a?.length() ?: 0)).map { a!!.getJSONObject(it).let { p -> PendingPairing(p.optString("id"), p.optString("device"), p.getLong("expiresAt")) } } },
        )
    }
}

/** This phone's IPv4 addresses on its networks, for the other device to type. */
fun localAddresses(context: Context): List<String> {
    val manager = context.getSystemService(ConnectivityManager::class.java) ?: return emptyList()
    return manager.allNetworks.flatMap { network ->
        manager.getLinkProperties(network)?.linkAddresses.orEmpty().mapNotNull { (it.address as? Inet4Address)?.takeIf { a -> !a.isLoopbackAddress }?.hostAddress }
    }.distinct()
}

/** Settings → Share this phone (local mode only). */
@Composable
fun SharingSection(app: RemoteApp) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val state = app.sharing
    var problem by remember { mutableStateOf<String?>(null) }
    Text("Share this phone", style = Type.headline)
    Text("Lets the 1loop desktop or another phone on this network use this phone's agent: there, add a Remote server with the address and fingerprint below, then type here the code it shows. Off each time the engine starts.", style = Type.small)
    Row(verticalAlignment = Alignment.CenterVertically) {
        Text(if (state?.enabled == true) "Shared on the network" else "Not shared", style = Type.body.copy(fontWeight = FontWeight.Medium), modifier = Modifier.weight(1f))
        Toggle(state?.enabled == true, enabled = app.connected) { on ->
            scope.launch {
                try { app.setSharing(on); problem = null }
                catch (cancelled: CancellationException) { throw cancelled }
                catch (failure: Exception) { problem = failure.message }
            }
        }
    }
    if (state?.enabled == true) {
        val addresses = remember(state.port) { localAddresses(context).map { "https://$it:${state.port}" } }
        addresses.forEach { address -> Row(verticalAlignment = Alignment.CenterVertically) { Text(address, style = Type.mono, modifier = Modifier.weight(1f)); CopyChip { address } } }
        if (addresses.isEmpty()) Text("No network: connect this phone to Wi-Fi.", style = Type.small.copy(color = Palette.danger))
        Text("Certificate SHA-256", style = Type.caption)
        Row(verticalAlignment = Alignment.CenterVertically) { Text(state.fingerprint, style = Type.mono, modifier = Modifier.weight(1f)); CopyChip { state.fingerprint } }
    }
    if (state != null && state.devices.isNotEmpty()) {
        Text("Paired devices", style = Type.body.copy(fontWeight = FontWeight.Medium))
        state.devices.forEachIndexed { index, device ->
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(device.name, style = Type.body, modifier = Modifier.weight(1f))
                Pill("Remove") { scope.launch { runCatching { app.revokeDevice(index) }.onFailure { problem = it.message } } }
            }
        }
    }
    problem?.let { Text(it, style = Type.small.copy(color = Palette.danger)) }
}

/** A device asking to use this phone: its user types the code it shows. */
@Composable
fun PairingRequestDialog(app: RemoteApp) {
    val request = app.sharing?.pending?.firstOrNull() ?: return
    val scope = rememberCoroutineScope()
    var code by remember(request.id) { mutableStateOf("") }
    var problem by remember(request.id) { mutableStateOf<String?>(null) }
    LaunchedEffect(request.id) {
        delay((request.expiresAt - System.currentTimeMillis()).coerceAtLeast(0))
        try {
            app.refreshSharing()
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (failure: Exception) {
            problem = failure.message
        }
    }
    AlertDialog(
        onDismissRequest = {},
        containerColor = Palette.surface,
        title = { Text("“${request.device}” wants to use this phone", style = Type.title) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                Text("It will send messages to the agent on this phone and approve its actions. Type the six-digit code that device shows.", style = Type.small)
                Field(code, { code = it.filter(Char::isDigit).take(6); problem = null }, "Code shown on that device", KeyboardType.Number)
                problem?.let { Text(it, style = Type.small.copy(color = Palette.danger)) }
            }
        },
        confirmButton = {
            Pill("Allow", primary = true, enabled = code.length == 6) {
                scope.launch {
                    try { app.answerPairing(request.id, code, true) }
                    catch (cancelled: CancellationException) { throw cancelled }
                    catch (failure: Exception) { problem = failure.message }
                }
            }
        },
        dismissButton = { Pill("Refuse") { scope.launch { runCatching { app.answerPairing(request.id, "", false) } } } },
    )
}
