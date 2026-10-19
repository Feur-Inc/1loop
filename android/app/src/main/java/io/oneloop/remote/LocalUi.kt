package io.oneloop.remote

import androidx.activity.compose.BackHandler
import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.animateDpAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.layout.windowInsetsPadding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.DropdownMenu
import androidx.compose.runtime.rememberCoroutineScope
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp

// Local phone: where 1loop works (ModeScreen), the setup of what this phone
// still lacks (LocalSetupScreen) and the one switch that lets the agent use
// the phone itself through ADB (LocalAdbSection). These only draw and call
// back; LocalRuntime does the work.

// ---------------------------------------------------------------------------
// Where 1loop works: always the three, whatever is installed.

@Composable
fun ModeScreen(onSelect: (ExecutionTarget) -> Unit) {
    Column(
        Modifier.fillMaxSize().windowInsetsPadding(WindowInsets.safeDrawing).verticalScroll(rememberScrollState())
            .padding(horizontal = 24.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Spacer(Modifier.height(56.dp))
        RingMark(64.dp)
        Spacer(Modifier.height(20.dp))
        Text("Where should 1loop work?", style = Type.headline, textAlign = TextAlign.Center)
        Spacer(Modifier.height(6.dp))
        Text("Choose where to run your agent.", style = Type.small.copy(fontSize = 15.sp), textAlign = TextAlign.Center)
        Spacer(Modifier.height(28.dp))
        Column(Modifier.widthIn(max = 520.dp).fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            ModeCard(Icons.server, "Remote server", "A 1loop server, over HTTPS.") { onSelect(ExecutionTarget.RemoteServer) }
            ModeCard(Icons.computer, "Remote desktop", "1loop on your computer, on this Wi-Fi.") { onSelect(ExecutionTarget.RemoteDesktop) }
            ModeCard(Icons.phone, "Local phone", "On this phone, with Debian. The setup is optional.") { onSelect(ExecutionTarget.LocalPhone) }
        }
        Spacer(Modifier.height(32.dp))
    }
}

@Composable
private fun ModeCard(icon: ImageVector, title: String, detail: String, onClick: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(14.dp)).background(Palette.surface)
            .border(1.dp, Palette.borderSoft, RoundedCornerShape(14.dp)).clickable(onClick = onClick)
            .padding(horizontal = 16.dp, vertical = 14.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(icon, null, tint = Palette.text, modifier = Modifier.size(22.dp))
        Column(Modifier.weight(1f).padding(horizontal = 14.dp)) {
            Text(title, style = Type.body.copy(fontWeight = FontWeight.Medium))
            Text(detail, style = Type.caption.copy(color = Palette.muted))
        }
        Icon(Icons.chevronRight, null, tint = Palette.faint, modifier = Modifier.size(18.dp))
    }
}

// ---------------------------------------------------------------------------
// Setup: only what is missing, running or failed; done steps are not shown.

@Composable
fun LocalSetupScreen(
    status: LocalStatus, onPrepare: (String) -> Unit, onStart: () -> Unit, onBack: () -> Unit,
    starting: Boolean = false, problem: String? = null,
) {
    BackHandler(onBack = onBack)
    val pending = status.steps.filter { it.state != StepState.Done }
    Column(
        Modifier.fillMaxSize().windowInsetsPadding(WindowInsets.safeDrawing).verticalScroll(rememberScrollState())
            .padding(horizontal = 24.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Row(Modifier.fillMaxWidth().padding(top = 8.dp)) {
            RoundButton(Icons.back, Color.Transparent, Palette.muted, description = "Back", onClick = onBack)
        }
        Spacer(Modifier.height(24.dp))
        RingMark(56.dp)
        Spacer(Modifier.height(20.dp))
        Text("Local phone", style = Type.headline, textAlign = TextAlign.Center)
        Spacer(Modifier.height(6.dp))
        Text(
            when {
                pending.isEmpty() -> "This phone has everything it needs."
                status.ready -> "Ready to start. The remaining steps are optional."
                else -> "Only the missing steps are shown."
            },
            style = Type.small.copy(fontSize = 15.sp), textAlign = TextAlign.Center,
        )
        Spacer(Modifier.height(28.dp))
        Column(Modifier.widthIn(max = 520.dp).fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            for (step in pending) StepCard(step) { onPrepare(step.id) }
        }
        Spacer(Modifier.height(24.dp))
        if (starting) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                WorkRing("wait", 26.dp)
                Spacer(Modifier.width(12.dp))
                Text("Starting the local agent…", style = Type.small)
            }
        } else Pill("Start", primary = true, enabled = status.ready, onClick = onStart)
        problem?.let { Text(it, style = Type.small.copy(color = Palette.danger), textAlign = TextAlign.Center, modifier = Modifier.padding(top = 14.dp)) }
        if (!status.ready) {
            Text(
                "Prepare the required steps to start.", style = Type.caption, textAlign = TextAlign.Center,
                modifier = Modifier.padding(top = 10.dp),
            )
        }
        Spacer(Modifier.height(32.dp))
    }
}

@Composable
internal fun StepCard(step: SetupStep, onPrepare: () -> Unit) {
    val failed = step.state == StepState.Failed
    Column(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(14.dp)).background(Palette.surface)
            .border(1.dp, if (failed) Palette.danger.copy(alpha = 0.5f) else Palette.borderSoft, RoundedCornerShape(14.dp))
            .padding(horizontal = 16.dp, vertical = 14.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(step.title, style = Type.body.copy(fontWeight = FontWeight.Medium), modifier = Modifier.weight(1f))
            if (!step.required) Text("Optional", style = Type.caption)
        }
        if (step.detail.isNotEmpty()) {
            Text(step.detail, style = Type.small.copy(color = if (failed) Palette.danger else Palette.muted), modifier = Modifier.padding(top = 4.dp))
        }
        if (step.state == StepState.Running) {
            Row(Modifier.padding(top = 12.dp), verticalAlignment = Alignment.CenterVertically) {
                WorkRing("wait", 22.dp)
                Spacer(Modifier.width(10.dp))
                Text("Preparing…", style = Type.small)
            }
        } else if (step.canPrepare) { // not on a phone that cannot run it
            Box(Modifier.padding(top = 12.dp)) { Pill(if (failed) "Try again" else "Prepare", onClick = onPrepare) }
        }
    }
}

// ---------------------------------------------------------------------------
// Allow local ADB: one switch. Phone Use comes with it once ADB is connected;
// pairing and connecting are only shown while allowed but not connected.

@Composable
fun LocalAdbSection(
    state: LocalAdbState,
    onAllowedChange: (Boolean) -> Unit,
    onPair: (port: Int, code: String) -> Unit,
    onConnect: (port: Int) -> Unit,
    onOpenSettings: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier.fillMaxWidth().clip(RoundedCornerShape(14.dp)).background(Palette.surface)
            .border(1.dp, Palette.borderSoft, RoundedCornerShape(14.dp)).padding(horizontal = 16.dp, vertical = 14.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Icon(Icons.phone, null, tint = if (state.allowed) Palette.text else Palette.muted, modifier = Modifier.size(20.dp))
            Column(Modifier.weight(1f).padding(horizontal = 12.dp)) {
                Text("Allow local ADB", style = Type.body.copy(fontWeight = FontWeight.Medium))
                Text(
                    when {
                        state.busy -> "Working…"
                        !state.allowed -> "Off. 1loop cannot use this phone."
                        state.connected -> "Connected. Phone Use is available."
                        else -> "Allowed, not connected yet."
                    },
                    style = Type.caption.copy(color = if (state.allowed && state.connected) Palette.added else Palette.muted),
                )
            }
            // Turning it on waits for the current work; turning it off never does.
            Toggle(state.allowed, enabled = state.allowed || !state.busy, onChange = onAllowedChange)
        }
        Text(
            "When on, the agent can see this phone's screen, tap, type and run adb commands. " +
                "Typing replaces your clipboard. Turning it off blocks new actions at once; it cannot undo what was already done.",
            style = Type.caption, modifier = Modifier.padding(top = 10.dp),
        )
        state.problem?.let { Text(it, style = Type.small.copy(color = Palette.danger), modifier = Modifier.padding(top = 10.dp)) }
        if (state.allowed && !state.connected) AdbGuide(state.paired, state.busy, onPair, onConnect, onOpenSettings)
    }
}

@Composable
private fun AdbGuide(paired: Boolean, busy: Boolean, onPair: (Int, String) -> Unit, onConnect: (Int) -> Unit, onOpenSettings: () -> Unit) {
    // Held in memory only, and cleared once sent: the pairing code is never kept.
    var pairPort by remember { mutableStateOf("") }
    var code by remember { mutableStateOf("") }
    var connectPort by remember { mutableStateOf("") }
    // Paired once: only connecting is needed, unless Android forgot this phone.
    var pairAgain by remember { mutableStateOf(false) }
    val pairing = !paired || pairAgain
    Column(Modifier.padding(top = 16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
        GuideStep("1", "Turn on Wireless debugging", "In Developer options. Android 11 or later.")
        Pill("Open Wireless debugging", enabled = !busy, onClick = onOpenSettings)
        if (pairing) {
            Spacer(Modifier.height(4.dp))
            GuideStep("2", "Pair this phone", "Tap Pair device with pairing code, then enter its port and code.")
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Field(pairPort, { pairPort = it.filter(Char::isDigit).take(5) }, "Port", KeyboardType.Number, Modifier.weight(1f))
                Field(code, { code = it.filter(Char::isDigit).take(6) }, "Pairing code", KeyboardType.Number, Modifier.weight(1f))
            }
            Pill("Pair", enabled = !busy && port(pairPort) != null && code.isNotEmpty()) {
                onPair(port(pairPort)!!, code)
                code = ""
            }
        }
        Spacer(Modifier.height(4.dp))
        GuideStep(if (pairing) "3" else "2", "Connect", "Enter the port shown under IP address & Port.")
        Field(connectPort, { connectPort = it.filter(Char::isDigit).take(5) }, "Port", KeyboardType.Number)
        Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            Pill("Connect", enabled = !busy && port(connectPort) != null) { onConnect(port(connectPort)!!) }
            if (!pairing) Pill("Pair again", enabled = !busy) { pairAgain = true }
        }
    }
}

private fun port(text: String) = text.toIntOrNull()?.takeIf { it in 1..65535 }

@Composable
private fun GuideStep(number: String, title: String, detail: String) {
    Row {
        Box(Modifier.size(22.dp).clip(CircleShape).background(Palette.raised), contentAlignment = Alignment.Center) {
            Text(number, style = Type.caption.copy(color = Palette.text, fontWeight = FontWeight.SemiBold))
        }
        Column(Modifier.padding(start = 10.dp)) {
            Text(title, style = Type.small.copy(color = Palette.text, fontWeight = FontWeight.Medium))
            Text(detail, style = Type.caption)
        }
    }
}

@Composable
internal fun Toggle(checked: Boolean, enabled: Boolean, onChange: (Boolean) -> Unit) {
    val track by animateColorAsState(if (checked) Palette.amber else Palette.raised, tween(160), label = "track")
    val knob by animateDpAsState(if (checked) 22.dp else 3.dp, tween(160), label = "knob")
    Box(
        Modifier.size(48.dp, 28.dp).clip(RoundedCornerShape(50)).background(track)
            .border(1.dp, if (checked) Color.Transparent else Palette.border, RoundedCornerShape(50))
            .clickable(enabled = enabled) { onChange(!checked) },
    ) {
        Box(
            Modifier.offset(x = knob, y = 3.dp).size(22.dp).clip(CircleShape)
                .background(if (checked) Palette.ink else if (enabled) Palette.muted else Palette.faint),
        )
    }
}

// ---------------------------------------------------------------------------
// The panel's +: a browser or a terminal tab.

@Composable
fun LocalPanelAdd(app: RemoteApp, chat: String) {
    val scope = rememberCoroutineScope()
    var menu by remember { mutableStateOf(false) }
    fun open() {
        scope.launch {
            try {
                localPanel(app, chat, JSONObject().put("action", "open").put("kind", "browser").put("url", ""))
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                app.problem = e.message ?: "The browser did not open."
            }
        }
    }
    Box {
        RoundButton(Icons.plus, Color.Transparent, Palette.muted, description = "Open a tab") { menu = true }
        DropdownMenu(menu, { menu = false }, containerColor = Palette.popover) {
            Row(
                Modifier.widthIn(min = 200.dp).clickable {
                    menu = false
                    open()
                }.padding(horizontal = 14.dp, vertical = 12.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Icon(Icons.globe, null, tint = Palette.text, modifier = Modifier.size(18.dp))
                Spacer(Modifier.width(12.dp))
                Text("Browser", style = Type.body.copy(fontSize = 14.5.sp))
            }
            Row(
                Modifier.widthIn(min = 200.dp).clickable {
                    menu = false
                    scope.launch {
                        try {
                            localPanel(app, chat, JSONObject().put("action", "open").put("kind", "terminal"))
                        } catch (e: CancellationException) {
                            throw e
                        } catch (e: Exception) {
                            app.problem = e.message
                        }
                    }
                }.padding(horizontal = 14.dp, vertical = 12.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Icon(Icons.terminal, null, tint = Palette.text, modifier = Modifier.size(18.dp))
                Spacer(Modifier.width(12.dp))
                Text("Terminal", style = Type.body.copy(fontSize = 14.5.sp))
            }
        }
    }
}

/** Opens or closes a tab of a chat's panel (POST /api/chats/{id}/panel); an older 1loop refuses it. */
private suspend fun localPanel(app: RemoteApp, chat: String, body: JSONObject, timeout: Int = 20_000) {
    val target = app.server ?: error("The local agent is not running.")
    withContext(Dispatchers.IO) { Api(target).post("/api/chats/$chat/panel", body, timeout) }
}

suspend fun closeLocalTab(app: RemoteApp, chat: String, key: String) {
    runCatching { localPanel(app, chat, JSONObject().put("action", "close").put("key", key)) }
}
