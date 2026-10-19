package io.oneloop.remote

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.Icon
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
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import org.json.JSONObject

// A browser tab's viewport and console, as on the desktop: Adaptive (the
// phone's view size), Desktop, Tablet, Mobile or Custom; the page's logs and
// JavaScript. Shown only when the computer reports the viewport (pixelRatio):
// an older 1loop has neither.

/** What the computer says of a tab's viewport (GET …/panel/{key}). */
data class Viewport(val width: Int, val height: Int, val mobile: Boolean, val adaptive: Boolean, val pixelRatio: Double, val native: Boolean = false) {
    companion object {
        fun from(o: JSONObject?) = o?.takeIf { it.has("pixelRatio") }?.let {
            Viewport(it.optInt("width"), it.optInt("height"), it.optBoolean("mobile"), it.optBoolean("adaptive"), it.optDouble("pixelRatio"), it.optBoolean("native"))
        }
    }
}

private val presets = listOf(
    Triple("Desktop", 1280 to 800, false), Triple("Tablet", 768 to 1024, false), Triple("Mobile", 390 to 844, true),
)

private fun Viewport.mode() = if (adaptive) "Adaptive" else presets.firstOrNull { (_, size, mobile) -> size == width to height && mobile == this.mobile }?.first ?: "Custom"

/** Sends a viewport, within the desktop's bounds; the tab's state follows. */
suspend fun resizeBrowser(app: RemoteApp, chat: String, key: String, width: Int, height: Int, mobile: Boolean, adaptive: Boolean, pixelRatio: Double = 0.0) {
    val body = JSONObject().put("action", "resize").put("width", width.coerceIn(240, 3840)).put("height", height.coerceIn(200, 2160))
        .put("mobile", mobile).put("adaptive", adaptive)
    if (pixelRatio > 0) body.put("pixelRatio", pixelRatio)
    app.panelCall(chat, key, body)
}

@Composable
fun BrowserToolbar(
    viewport: Viewport, console: Boolean, onConsole: () -> Unit,
    onMode: (String) -> Unit, onCustom: (Int, Int) -> Unit, enabled: Boolean,
) {
    var menu by remember { mutableStateOf(false) }
    var custom by remember { mutableStateOf(false) }
    var dimensions by remember(viewport) { mutableStateOf("${viewport.width} × ${viewport.height}") }
    Column(Modifier.padding(horizontal = 12.dp).padding(bottom = 6.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Box {
                ToolChip("${viewport.mode()} · ${viewport.width} × ${viewport.height}", selected = false, icon = Icons.chevron, enabled = enabled) { menu = true }
                DropdownMenu(menu, { menu = false }, containerColor = Palette.popover) {
                    val modes = listOf("Adaptive" to "Follow this view's size") +
                        presets.map { (name, size, _) -> name to "${size.first} × ${size.second}" } + ("Custom" to "Choose CSS viewport dimensions")
                    for ((name, detail) in modes) {
                        Row(
                            Modifier.widthIn(min = 240.dp).clickable {
                                menu = false
                                if (name == "Custom") custom = true else { custom = false; onMode(name) }
                            }.padding(horizontal = 14.dp, vertical = 10.dp),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            Column(Modifier.weight(1f)) {
                                Text(name, style = Type.body.copy(fontSize = 14.5.sp))
                                Text(detail, style = Type.caption.copy(color = Palette.muted))
                            }
                            if (viewport.mode() == name) Icon(Icons.check, null, tint = Palette.amber, modifier = Modifier.size(18.dp))
                        }
                    }
                }
            }
            ToolChip("Console", selected = console, icon = Icons.terminal, onClick = onConsole)
        }
        if (custom) Row(Modifier.padding(top = 8.dp), verticalAlignment = Alignment.CenterVertically) {
            Field(dimensions, { dimensions = it }, "width × height", KeyboardType.Ascii, Modifier.weight(1f))
            Spacer(Modifier.width(8.dp))
            val parts = dimensions.split('x', '×', ',', ' ').filter { it.isNotBlank() }.mapNotNull { it.trim().toIntOrNull() }
            Pill("Apply", primary = true, enabled = enabled && parts.size == 2) {
                onCustom(parts[0], parts[1])
                custom = false
            }
        }
    }
}

@Composable
private fun ToolChip(text: String, selected: Boolean, icon: androidx.compose.ui.graphics.vector.ImageVector, enabled: Boolean = true, onClick: () -> Unit) {
    val shape = RoundedCornerShape(50)
    Row(
        Modifier.clip(shape).background(if (selected) Palette.raised else Palette.background)
            .border(1.dp, if (selected) Palette.borderHot else Palette.border, shape)
            .clickable(enabled = enabled, onClick = onClick).height(32.dp).padding(horizontal = 12.dp),
        verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        Text(text, style = Type.small.copy(color = if (selected) Palette.text else Palette.muted), maxLines = 1, overflow = TextOverflow.Ellipsis)
        Icon(icon, null, tint = Palette.muted, modifier = Modifier.size(14.dp))
    }
}

private data class LogLine(val index: Int, val level: String, val text: String, val source: String)

/** The page's logs, read again every second and a half while shown, and a line for JavaScript. */
@Composable
fun BrowserConsole(app: RemoteApp, chat: String, key: String, act: (() -> Unit) -> Unit, modifier: Modifier = Modifier) {
    val scope = rememberCoroutineScope()
    var logs by remember { mutableStateOf<List<LogLine>>(emptyList()) }
    var script by remember { mutableStateOf("") }
    var problem by remember { mutableStateOf<String?>(null) }
    var round by remember { mutableStateOf(0) }
    val list = rememberLazyListState()
    LaunchedEffect(key, round) {
        while (true) {
            try {
                val array = app.panelCall(chat, key, JSONObject().put("action", "logs")).optJSONArray("logs")
                logs = (0 until (array?.length() ?: 0)).map { i ->
                    array!!.getJSONObject(i).let { LogLine(it.optInt("index"), it.optString("level"), it.optString("text"), it.optString("source")) }
                }
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                problem = e.message
            }
            delay(1500)
        }
    }
    LaunchedEffect(logs.size) { if (logs.isNotEmpty()) list.scrollToItem(logs.size - 1) }
    Column(modifier.fillMaxWidth().background(Palette.code)) {
        Box(Modifier.fillMaxWidth().height(1.dp).background(Palette.borderSoft))
        LazyColumn(Modifier.weight(1f).fillMaxWidth(), state = list, contentPadding = PaddingValues(horizontal = 12.dp, vertical = 8.dp)) {
            if (logs.isEmpty()) item { Text("No logs yet.", style = Type.caption) }
            items(logs, key = { it.index }) { line ->
                Text(
                    (if (line.level == "command") "› " else "") + line.text,
                    style = Type.mono.copy(
                        fontSize = 12.sp, lineHeight = 17.sp,
                        color = when (line.level) {
                            "error" -> Palette.danger
                            "warning", "warn" -> Palette.amber
                            "command" -> Palette.text
                            "result" -> Palette.added
                            else -> Palette.muted
                        },
                        fontWeight = if (line.level == "command") FontWeight.Medium else FontWeight.Normal,
                    ),
                    modifier = Modifier.padding(vertical = 2.dp),
                )
            }
        }
        problem?.let { Text(it, style = Type.caption.copy(color = Palette.danger), modifier = Modifier.padding(horizontal = 12.dp)) }
        Row(Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 8.dp), verticalAlignment = Alignment.CenterVertically) {
            val evaluate = {
                val code = script.trim()
                if (code.isNotEmpty()) act {
                    problem = null
                    scope.launch {
                        try {
                            app.panelCall(chat, key, JSONObject().put("action", "evaluate").put("text", code))
                            script = ""
                        } catch (e: CancellationException) {
                            throw e
                        } catch (e: Exception) {
                            problem = e.message // a failed script is not logged by the computer
                        }
                        round++ // read the command and its result now
                    }
                }
            }
            BasicTextField(
                script, { script = it },
                Modifier.weight(1f).background(Palette.background, RoundedCornerShape(10.dp)).border(1.dp, Palette.border, RoundedCornerShape(10.dp))
                    .padding(horizontal = 12.dp, vertical = 10.dp),
                textStyle = Type.mono.copy(fontSize = 13.sp), cursorBrush = SolidColor(Palette.amber), singleLine = true,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Ascii, imeAction = ImeAction.Send, autoCorrectEnabled = false),
                keyboardActions = KeyboardActions(onSend = { evaluate() }),
                decorationBox = { inner ->
                    Box {
                        if (script.isEmpty()) Text("Run JavaScript", style = Type.mono.copy(fontSize = 13.sp, color = Palette.faint))
                        inner()
                    }
                },
            )
            Spacer(Modifier.width(8.dp))
            RoundButton(Icons.send, Palette.amber, Palette.ink, size = 38.dp, description = "Run") { evaluate() }
        }
    }
}
