package io.oneloop.remote

import android.graphics.BitmapFactory
import androidx.activity.compose.BackHandler
import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInVertically
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.gestures.detectVerticalDragGestures
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.layout.windowInsetsPadding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.withContext
import org.json.JSONObject

// ---------------------------------------------------------------------------
// The chat's side panel, as on the computer: its task list first, then the
// tabs open there (browsers, terminals, Git changes, tool calls). Browsers
// and terminals are live and take the phone's touches and typing.

@Composable
fun PanelScreen(app: RemoteApp, chat: String, onClose: () -> Unit) {
    BackHandler(onBack = onClose)
    var tabs by remember { mutableStateOf<List<PanelTab>>(emptyList()) }
    var selected by rememberSaveable(chat) { mutableStateOf("tasks") }
    var reachable by remember { mutableStateOf(true) }
    LaunchedEffect(chat) {
        while (true) {
            val list = app.panelTabs(chat)
            reachable = list != null
            if (list != null) {
                tabs = list.filter { it.kind != "todo" } // the task list is always first
                if (selected != "tasks" && tabs.none { it.key == selected }) selected = "tasks"
            }
            delay(2000)
        }
    }
    val tasks = app.meta?.todo.orEmpty()
    Column(Modifier.fillMaxSize().background(Palette.background).windowInsetsPadding(WindowInsets.safeDrawing)) {
        Row(Modifier.fillMaxWidth().padding(horizontal = 8.dp, vertical = 6.dp), verticalAlignment = Alignment.CenterVertically) {
            RoundButton(Icons.back, Color.Transparent, Palette.muted, description = "Back to the chat", onClick = onClose)
            Column(Modifier.weight(1f).padding(horizontal = 8.dp)) {
                Text("Panel", style = Type.body.copy(fontWeight = FontWeight.Medium))
                Text(
                    if (reachable) app.chats.firstOrNull { it.id == chat }?.title.orEmpty() else "Reconnecting…",
                    style = Type.caption.copy(color = if (reachable) Palette.faint else Palette.amber), maxLines = 1, overflow = TextOverflow.Ellipsis,
                )
            }
        }
        Row(
            Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()).padding(horizontal = 12.dp).padding(bottom = 10.dp),
            horizontalArrangement = Arrangement.spacedBy(6.dp),
        ) {
            val done = tasks.count { it.status == "completed" }
            TabChip(if (tasks.isEmpty()) "Tasks" else "Tasks $done/${tasks.size}", Icons.tasks, selected == "tasks") { selected = "tasks" }
            for (tab in tabs) TabChip(tab.title, tabIcon(tab.kind), selected == tab.key) { selected = tab.key }
        }
        Box(Modifier.fillMaxWidth().height(1.dp).background(Palette.borderSoft))
        Box(Modifier.weight(1f).fillMaxWidth()) {
            val tab = tabs.firstOrNull { it.key == selected }
            key(tab?.key ?: "tasks") {
                when (tab?.kind) {
                    "browser" -> BrowserView(app, chat, tab)
                    "terminal" -> TerminalView(app, chat, tab)
                    "changes" -> ChangesView(app, chat, tab)
                    "tool" -> ToolView(app.items.getOrNull(tab.index))
                    else -> TasksView(tasks)
                }
            }
        }
    }
}

private fun tabIcon(kind: String) = when (kind) {
    "browser" -> Icons.globe
    "terminal" -> Icons.terminal
    "changes" -> Icons.pencil
    else -> Icons.document
}

@Composable
private fun TabChip(text: String, icon: ImageVector, selected: Boolean, onClick: () -> Unit) {
    val shape = RoundedCornerShape(50)
    Row(
        Modifier.clip(shape).background(if (selected) Palette.raised else Color.Transparent)
            .border(1.dp, if (selected) Palette.borderHot else Palette.border, shape)
            .clickable(onClick = onClick).height(36.dp).padding(horizontal = 14.dp),
        verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(7.dp),
    ) {
        Icon(icon, null, tint = if (selected) Palette.text else Palette.muted, modifier = Modifier.size(16.dp))
        Text(
            text, style = Type.small.copy(color = if (selected) Palette.text else Palette.muted, fontWeight = FontWeight.Medium),
            maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.widthIn(max = 180.dp),
        )
    }
}

@Composable
private fun Empty(title: String, detail: String) {
    Column(Modifier.fillMaxSize().padding(32.dp), verticalArrangement = Arrangement.Center, horizontalAlignment = Alignment.CenterHorizontally) {
        Text(title, style = Type.body.copy(fontWeight = FontWeight.Medium), textAlign = TextAlign.Center)
        Spacer(Modifier.height(6.dp))
        Text(detail, style = Type.small, textAlign = TextAlign.Center)
    }
}

// ---------------------------------------------------------------------------
// Task list

@Composable
private fun TasksView(tasks: List<Task>) {
    if (tasks.isEmpty()) {
        Empty("No tasks yet", "When the agent plans its work, its task list shows here and follows its progress.")
        return
    }
    var now by remember { mutableLongStateOf(System.currentTimeMillis()) }
    LaunchedEffect(Unit) { while (true) { delay(1000); now = System.currentTimeMillis() } }
    val done = tasks.count { it.status == "completed" }
    LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(horizontal = 18.dp, vertical = 16.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
        item {
            Column(Modifier.padding(bottom = 12.dp)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text("$done of ${tasks.size} done", style = Type.body.copy(fontWeight = FontWeight.Medium), modifier = Modifier.weight(1f))
                    tasks.firstOrNull { it.status == "in_progress" }?.let { Text("In progress", style = Type.small.copy(color = Palette.amber)) }
                }
                Spacer(Modifier.height(10.dp))
                Box(Modifier.fillMaxWidth().height(3.dp).clip(RoundedCornerShape(2.dp)).background(Palette.raised)) {
                    Box(Modifier.fillMaxWidth(done / tasks.size.toFloat()).fillMaxHeight().background(Palette.amber))
                }
            }
        }
        items(tasks, key = { it.id }) { task ->
            Row(Modifier.fillMaxWidth().padding(vertical = 9.dp), verticalAlignment = Alignment.Top) {
                Box(Modifier.size(22.dp), contentAlignment = Alignment.Center) {
                    if (task.status == "in_progress") WorkRing("think", 22.dp) else TaskMark(task.status == "completed")
                }
                Spacer(Modifier.width(12.dp))
                Text(
                    task.text, modifier = Modifier.weight(1f).padding(top = 1.dp),
                    style = Type.body.copy(color = when (task.status) {
                        "completed" -> Palette.muted
                        "in_progress" -> Palette.text
                        else -> Color(0xFFB4B4B4)
                    }),
                )
                val time = when {
                    task.status == "completed" && task.started > 0 && task.completed - task.started >= 1000 -> span(task.completed - task.started)
                    task.status == "in_progress" && task.started > 0 -> span(now - task.started)
                    else -> ""
                }
                if (time.isNotEmpty()) Text(time, style = Type.caption.copy(color = if (task.status == "in_progress") Palette.amber else Palette.faint), modifier = Modifier.padding(start = 10.dp, top = 3.dp))
            }
        }
    }
}

// A finished task: the closed amber ring with its check; one to do: an empty ring.
@Composable
private fun TaskMark(done: Boolean) {
    Canvas(Modifier.size(20.dp)) {
        val r = size.minDimension / 2 - 1.5.dp.toPx()
        if (done) {
            drawCircle(Palette.amber, r, style = Stroke(2.dp.toPx()))
            val w = size.width
            val path = androidx.compose.ui.graphics.Path().apply {
                moveTo(w * 0.31f, w * 0.52f); lineTo(w * 0.45f, w * 0.65f); lineTo(w * 0.70f, w * 0.38f)
            }
            drawPath(path, Palette.amber, style = Stroke(2.dp.toPx(), cap = StrokeCap.Round))
        } else drawCircle(Palette.faint, r, style = Stroke(1.5.dp.toPx()))
    }
}

private fun span(ms: Long): String {
    val s = (ms / 1000).coerceAtLeast(0)
    return when {
        s < 60 -> "${s}s"
        s < 3600 -> "${s / 60}m ${(s % 60).toString().padStart(2, '0')}s"
        else -> "${s / 3600}h ${(s / 60 % 60).toString().padStart(2, '0')}m"
    }
}

// ---------------------------------------------------------------------------
// Browser: the page as the computer renders it, a few times a second. A tap
// clicks where it lands, a vertical drag scrolls the page.

// Control of the page is locked by default: a page watched on the phone is
// easily touched by mistake. The lock opens it; it closes again after 15
// minutes without input, and whenever the reader leaves the tab or panel
// (this view is then disposed, and starts locked the next time).
private const val RELOCK_MS = 15 * 60 * 1000L

@Composable
private fun BrowserView(app: RemoteApp, chat: String, tab: PanelTab) {
    var picture by remember { mutableStateOf<ImageBitmap?>(null) }
    var address by remember { mutableStateOf(tab.url) }
    var editing by remember { mutableStateOf(false) }
    var typed by remember { mutableStateOf("") }
    var locked by remember { mutableStateOf(true) }
    var used by remember { mutableLongStateOf(0L) } // the last input, for the relock
    var nudge by remember { mutableIntStateOf(0) } // a touch while locked: the lock shakes
    LaunchedEffect(tab.key) {
        while (true) {
            val bytes = app.panelScreen(chat, tab.key)
            if (bytes != null) {
                val decoded = withContext(Dispatchers.Default) { BitmapFactory.decodeByteArray(bytes, 0, bytes.size)?.asImageBitmap() }
                if (decoded != null) picture = decoded
            }
            delay(if (bytes == null) 1500 else 250)
        }
    }
    LaunchedEffect(locked, used) {
        if (!locked) {
            delay(RELOCK_MS)
            locked = true
        }
    }
    LaunchedEffect(tab.url) { if (!editing) address = tab.url }
    // Every action on the page goes through here.
    val act = { action: () -> Unit ->
        if (locked) nudge++ else {
            used = System.currentTimeMillis()
            action()
        }
    }
    val shake = remember { Animatable(0f) }
    LaunchedEffect(nudge) {
        if (nudge > 0) for (to in listOf(-6f, 6f, -4f, 4f, 0f)) shake.animateTo(to, tween(55))
    }
    val dim by animateFloatAsState(if (locked) 0.4f else 1f, tween(200), label = "dim")
    Column(Modifier.fillMaxSize()) {
        Row(Modifier.fillMaxWidth().padding(horizontal = 8.dp, vertical = 8.dp), verticalAlignment = Alignment.CenterVertically) {
            Row(Modifier.alpha(dim), verticalAlignment = Alignment.CenterVertically) {
                RoundButton(Icons.back, Color.Transparent, Palette.muted, size = 36.dp, iconSize = 18.dp, description = "Back") { act { app.panelInput(chat, tab.key, "back") } }
                RoundButton(Icons.chevronRight, Color.Transparent, Palette.muted, size = 36.dp, iconSize = 18.dp, description = "Forward") { act { app.panelInput(chat, tab.key, "forward") } }
                RoundButton(Icons.refresh, Color.Transparent, Palette.muted, size = 36.dp, iconSize = 17.dp, description = "Reload") { act { app.panelInput(chat, tab.key, "reload") } }
            }
            Spacer(Modifier.width(4.dp))
            Input(
                address, { address = it; editing = true }, "Address", KeyboardType.Uri, ImeAction.Go, Modifier.weight(1f).alpha(dim), enabled = !locked,
            ) {
                act { app.panelInput(chat, tab.key, "navigate", address.trim()) }
                editing = false
            }
            Spacer(Modifier.width(6.dp))
            val fill by animateColorAsState(if (locked) Palette.raised else Palette.amber, tween(200), label = "lock")
            RoundButton(
                if (locked) Icons.lock else Icons.unlock, fill, if (locked) Palette.text else Palette.ink, size = 38.dp, iconSize = 18.dp,
                description = if (locked) "Unlock control of the page" else "Lock control of the page",
                modifier = Modifier.graphicsLayer { translationX = shake.value.dp.toPx() },
            ) {
                locked = !locked
                used = System.currentTimeMillis()
            }
        }
        Box(Modifier.weight(1f).fillMaxWidth().background(Palette.code), contentAlignment = Alignment.TopCenter) {
            val shown = picture
            if (shown == null) WorkRing("wait", 34.dp, Modifier.align(Alignment.Center))
            else {
                var pending by remember { mutableFloatStateOf(0f) }
                Image(
                    shown, "Page", contentScale = ContentScale.FillBounds,
                    modifier = Modifier.aspectRatio(shown.width / shown.height.toFloat())
                        .pointerInput(tab.key) {
                            detectTapGestures { at -> act { app.panelInput(chat, tab.key, "tap", x = at.x / size.width, y = at.y / size.height) } }
                        }
                        .pointerInput(tab.key) {
                            var at = Offset.Zero
                            detectVerticalDragGestures(onDragStart = { at = it; pending = 0f }) { change, drag ->
                                change.consume()
                                pending -= drag
                                if (kotlin.math.abs(pending) > 24f) {
                                    val dy = pending / size.height
                                    act { app.panelInput(chat, tab.key, "scroll", x = at.x / size.width, y = at.y / size.height, dy = dy) }
                                    pending = 0f
                                }
                            }
                        },
                )
            }
            androidx.compose.animation.AnimatedVisibility(
                visible = locked && shown != null, modifier = Modifier.align(Alignment.BottomCenter).padding(bottom = 14.dp),
                enter = fadeIn(tween(180)) + slideInVertically(tween(220)) { it / 2 }, exit = fadeOut(tween(160)),
            ) {
                Row(
                    Modifier.clip(RoundedCornerShape(50)).background(Palette.popover.copy(alpha = 0.94f))
                        .border(1.dp, Palette.border, RoundedCornerShape(50)).clickable { locked = false; used = System.currentTimeMillis() }
                        .padding(horizontal = 14.dp, vertical = 9.dp),
                    verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    Icon(Icons.lock, null, tint = Palette.amber, modifier = Modifier.size(15.dp))
                    Text("View only · unlock to control", style = Type.small.copy(color = Palette.text))
                }
            }
        }
        Row(Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 8.dp).alpha(dim), verticalAlignment = Alignment.CenterVertically) {
            Input(typed, { typed = it }, "Type into the page", KeyboardType.Text, ImeAction.Send, Modifier.weight(1f), enabled = !locked) {
                act { app.panelInput(chat, tab.key, "line", typed) }
                typed = ""
            }
            Spacer(Modifier.width(8.dp))
            RoundButton(Icons.close, Palette.raised, Palette.muted, size = 38.dp, iconSize = 16.dp, description = "Erase") { act { app.panelInput(chat, tab.key, "key", "Backspace") } }
        }
    }
}

@Composable
private fun Input(
    value: String, onChange: (String) -> Unit, placeholder: String, keyboard: KeyboardType, action: ImeAction,
    modifier: Modifier = Modifier, mono: Boolean = false, enabled: Boolean = true, onDone: () -> Unit,
) {
    BasicTextField(
        value, onChange, modifier, enabled = enabled, singleLine = true, textStyle = if (mono) Type.mono else Type.body.copy(fontSize = 14.sp),
        cursorBrush = SolidColor(Palette.amber),
        keyboardOptions = KeyboardOptions(
            keyboardType = keyboard, imeAction = action, autoCorrectEnabled = false,
            capitalization = if (keyboard == KeyboardType.Text) KeyboardCapitalization.Sentences else KeyboardCapitalization.None,
        ),
        keyboardActions = KeyboardActions(onAny = { onDone() }),
        decorationBox = { inner ->
            Box(
                Modifier.background(Palette.surface, RoundedCornerShape(10.dp)).border(1.dp, Palette.border, RoundedCornerShape(10.dp))
                    .padding(horizontal = 12.dp, vertical = 10.dp),
            ) {
                if (value.isEmpty()) Text(placeholder, style = Type.body.copy(fontSize = 14.sp, color = Palette.faint), maxLines = 1)
                inner()
            }
        },
    )
}

// ---------------------------------------------------------------------------
// Terminal: its output, followed live, and a line to type commands.

@Composable
private fun TerminalView(app: RemoteApp, chat: String, tab: PanelTab) {
    var output by remember { mutableStateOf<String?>(null) }
    var exited by remember { mutableStateOf(false) }
    var line by remember { mutableStateOf("") }
    val scroll = rememberScrollState()
    LaunchedEffect(tab.key) {
        while (true) {
            app.panelTab(chat, tab.key)?.let { reply ->
                output = reply.optString("output").lines().takeLast(600).joinToString("\n").trimEnd()
                exited = reply.optBoolean("exited")
            }
            delay(500)
        }
    }
    LaunchedEffect(output) { scroll.scrollTo(scroll.maxValue) }
    Column(Modifier.fillMaxSize()) {
        Box(Modifier.weight(1f).fillMaxWidth().background(Palette.code)) {
            if (output == null) WorkRing("wait", 34.dp, Modifier.align(Alignment.Center))
            else Text(
                output.orEmpty().ifEmpty { " " }, style = Type.mono.copy(fontSize = 12.sp, lineHeight = 17.sp),
                modifier = Modifier.fillMaxSize().verticalScroll(scroll).padding(12.dp),
            )
        }
        if (exited) Text("This terminal has exited.", style = Type.small.copy(color = Palette.danger), modifier = Modifier.padding(start = 14.dp, top = 8.dp))
        Row(
            Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()).padding(horizontal = 12.dp).padding(top = 8.dp),
            horizontalArrangement = Arrangement.spacedBy(6.dp),
        ) {
            for ((label, keyName) in listOf("Ctrl+C" to "Ctrl+C", "Tab" to "Tab", "↑" to "ArrowUp", "↓" to "ArrowDown", "Esc" to "Escape", "Ctrl+D" to "Ctrl+D", "Ctrl+L" to "Ctrl+L")) {
                Box(
                    Modifier.clip(RoundedCornerShape(8.dp)).border(1.dp, Palette.border, RoundedCornerShape(8.dp))
                        .clickable { app.panelInput(chat, tab.key, "key", keyName) }.padding(horizontal = 12.dp, vertical = 7.dp),
                ) { Text(label, style = Type.small.copy(fontFamily = Type.mono.fontFamily, color = Palette.text)) }
            }
        }
        Row(Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 8.dp), verticalAlignment = Alignment.CenterVertically) {
            Input(line, { line = it }, "Command", KeyboardType.Ascii, ImeAction.Send, Modifier.weight(1f), mono = true) {
                app.panelInput(chat, tab.key, "line", line)
                line = ""
            }
            Spacer(Modifier.width(8.dp))
            RoundButton(Icons.send, Palette.amber, Palette.ink, size = 38.dp, description = "Run") {
                app.panelInput(chat, tab.key, "line", line)
                line = ""
            }
        }
    }
}

// ---------------------------------------------------------------------------
// Git changes and tool calls

@Composable
private fun ChangesView(app: RemoteApp, chat: String, tab: PanelTab) {
    var diff by remember { mutableStateOf<String?>(null) }
    var round by remember { mutableStateOf(0) }
    LaunchedEffect(round) { diff = app.panelTab(chat, tab.key)?.optString("diff").orEmpty() }
    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text("Unstaged changes", style = Type.body.copy(fontWeight = FontWeight.Medium), modifier = Modifier.weight(1f))
            Pill("Refresh", icon = Icons.refresh) { diff = null; round++ }
        }
        when {
            diff == null -> WorkRing("wait", 30.dp)
            diff!!.isBlank() -> Text("No changes in this view.", style = Type.small)
            else -> CodeWell(diff!!.trimEnd(), "Diff", diff = true)
        }
    }
}

@Composable
private fun ToolView(item: Item?) {
    if (item == null || item.kind != "tool") {
        Empty("Not available", "This call is no longer in the conversation.")
        return
    }
    val args = remember(item.args) { runCatching { JSONObject(item.args) }.getOrDefault(JSONObject()) }
    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Icon(Icons.forTool(item.tool), null, tint = Palette.text, modifier = Modifier.size(18.dp))
            Spacer(Modifier.width(10.dp))
            Text("${item.verb} ${item.target}", style = Type.body.copy(fontWeight = FontWeight.Medium), maxLines = 2, overflow = TextOverflow.Ellipsis)
        }
        if (item.meta.isNotEmpty()) Text(item.meta, style = Type.small)
        if (item.tool == "shell") CodeWell(args.optString("command"), "Command")
        if (item.diff.isNotEmpty()) CodeWell(item.diff, "Changes", diff = true)
        if (item.text.isNotEmpty()) CodeWell(item.text.trimEnd(), "Output")
        else if (item.diff.isEmpty()) Text(if (item.state == "done") "No output" else "Waiting for the result…", style = Type.small.copy(color = Palette.faint))
        if (item.failed.isNotEmpty() && item.failed != "Stopped") CodeWell(item.failed, "Error")
    }
}
