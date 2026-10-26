package io.oneloop.remote

import androidx.compose.ui.text.rememberTextMeasurer
import kotlinx.coroutines.CancellationException
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.viewinterop.AndroidView
import androidx.compose.ui.layout.Layout
import androidx.compose.ui.graphics.TransformOrigin
import androidx.compose.ui.input.pointer.pointerInteropFilter
import androidx.compose.ui.ExperimentalComposeUiApi
import androidx.compose.ui.unit.Constraints
import androidx.compose.ui.layout.onSizeChanged
import android.graphics.BitmapFactory
import androidx.activity.compose.BackHandler
import androidx.compose.animation.Crossfade
import androidx.compose.animation.core.VectorConverter
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsFocusedAsState
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.platform.LocalFocusManager
import kotlinx.coroutines.launch
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
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.unit.IntSize
import androidx.compose.runtime.derivedStateOf
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.drawscope.scale
import androidx.compose.ui.graphics.drawscope.translate
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.TextFieldValue
import androidx.compose.ui.text.TextRange
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
    val home = chat.startsWith("home:")
    val initialTab = if (home) "files" else "tasks"
    var tabs by remember(chat) { mutableStateOf<List<PanelTab>>(emptyList()) }
    var selected by rememberSaveable(chat) { mutableStateOf(initialTab) }
    var reachable by remember { mutableStateOf(true) }
    val scope = rememberCoroutineScope()
    LaunchedEffect(chat) {
        while (true) {
            val list = app.panelTabs(chat)
            reachable = list != null
            if (list != null) {
                tabs = list.filter { it.kind != "todo" && it.kind != "tool" } // the task list is always first
                if (selected != "tasks" && selected != "files" && tabs.none { it.key == selected }) selected = initialTab
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
                    if (!reachable) "Reconnecting…" else if (home) "New chat" else app.chats.firstOrNull { it.id == chat }?.title.orEmpty(),
                    style = Type.caption.copy(color = if (reachable) Palette.faint else Palette.amber), maxLines = 1, overflow = TextOverflow.Ellipsis,
                )
            }
            LocalPanelAdd(app, chat)
        }
        Row(
            Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()).padding(horizontal = 12.dp).padding(bottom = 10.dp),
            horizontalArrangement = Arrangement.spacedBy(6.dp),
        ) {
            val done = tasks.count { it.status == "completed" }
            if (!home) TabChip(if (tasks.isEmpty()) "Tasks" else "Tasks $done/${tasks.size}", Icons.tasks, selected == "tasks") { selected = "tasks" }
            TabChip("Files", Icons.folder, selected == "files") { selected = "files" }
            // The tab shown can be closed, as on the desktop.
            for (tab in tabs) TabChip(
                tab.title, tabIcon(tab.kind), selected == tab.key,
                onClose = if (selected == tab.key && tab.kind in listOf("browser", "terminal")) ({
                    scope.launch { closeLocalTab(app, chat, tab.key) }
                    tabs = tabs.filter { it.key != tab.key }
                    selected = initialTab
                }) else null,
            ) { selected = tab.key }
        }
        Box(Modifier.fillMaxWidth().height(1.dp).background(Palette.borderSoft))
        Box(Modifier.weight(1f).fillMaxWidth()) {
            val shown = if (selected == "files") "files" else tabs.firstOrNull { it.key == selected }?.key ?: initialTab
            Crossfade(shown, animationSpec = tween(180), label = "tab") { key ->
                val tab = tabs.firstOrNull { it.key == key }
                if (key == "files") {
                    FilesView(app, app.chats.firstOrNull { it.id == chat }?.workspace ?: app.selectedProject ?: app.workspace)
                    return@Crossfade
                }
                when (tab?.kind) {
                    "browser" -> BrowserView(app, chat, tab)
                    "chatgptweb" -> ChatGPTWebView(app, chat)
                    "terminal" -> TerminalView(app, chat, tab)
                    "changes" -> ChangesView(app, chat, tab)
                    "tool" -> ToolView(app, app.items.getOrNull(tab.index))
                    else -> Column {
                        Box(Modifier.weight(1f)) { TasksView(tasks, app.meta?.background.orEmpty(), app::stopTask) }
                        ContextFooter(app.meta)
                    }
                }
            }
        }
    }
}

private fun tabIcon(kind: String) = when (kind) {
    "browser", "chatgptweb" -> Icons.globe
    "terminal" -> Icons.terminal
    "changes" -> Icons.pencil
    else -> Icons.document
}

@Composable
private fun TabChip(text: String, icon: ImageVector, selected: Boolean, onClose: (() -> Unit)? = null, onClick: () -> Unit) {
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
        if (onClose != null) {
            Icon(Icons.close, "Close tab", tint = Palette.muted, modifier = Modifier.size(22.dp).clip(RoundedCornerShape(50)).clickable(onClick = onClose).padding(4.dp))
        }
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
private fun TasksView(tasks: List<Task>, background: List<BackgroundTask> = emptyList(), onStop: (String) -> Unit = {}) {
    if (tasks.isEmpty() && background.isEmpty()) {
        Empty("No tasks yet", "When the agent plans its work, its task list shows here and follows its progress.")
        return
    }
    var now by remember { mutableLongStateOf(System.currentTimeMillis()) }
    LaunchedEffect(Unit) { while (true) { delay(1000); now = System.currentTimeMillis() } }
    val done = tasks.count { it.status == "completed" }
    LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(horizontal = 18.dp, vertical = 16.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
        if (tasks.isNotEmpty()) item {
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
                // A fixed width with tabular digits: the counting time never
                // reflows the task's text.
                if (time.isNotEmpty()) Text(
                    time, textAlign = TextAlign.End, maxLines = 1,
                    style = Type.caption.copy(color = if (task.status == "in_progress") Palette.amber else Palette.faint, fontFeatureSettings = "tnum"),
                    modifier = Modifier.padding(start = 10.dp, top = 3.dp).width(58.dp),
                )
            }
        }
        // The agent's background commands, newest first, each with its own Stop.
        if (background.isNotEmpty()) item {
            Text("Background", style = Type.body.copy(fontWeight = FontWeight.Medium), modifier = Modifier.padding(top = if (tasks.isEmpty()) 0.dp else 18.dp, bottom = 6.dp))
        }
        items(background.reversed(), key = { "bg-" + it.id }) { task ->
            Row(Modifier.fillMaxWidth().padding(vertical = 8.dp), verticalAlignment = Alignment.CenterVertically) {
                Box(Modifier.size(22.dp), contentAlignment = Alignment.Center) {
                    if (task.running) WorkRing("think", 22.dp) else TaskMark(!task.stopped && task.exitCode == 0)
                }
                Spacer(Modifier.width(12.dp))
                Column(Modifier.weight(1f)) {
                    Text("$ " + task.command, style = Type.mono, maxLines = 2, overflow = TextOverflow.Ellipsis)
                    Text(
                        when {
                            task.running -> "Running · " + span(now - task.started)
                            task.stopped -> "Stopped"
                            task.exitCode == 0 -> "Done in " + span(task.ended - task.started)
                            else -> "Failed with code ${task.exitCode}"
                        },
                        style = Type.caption.copy(color = if (task.running) Palette.amber else Palette.faint),
                    )
                }
                if (task.running) Pill("Stop") { onStop(task.id) }
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

/** What the computer says about the page, with each change. */
private data class PageState(val url: String, val title: String, val loading: Boolean, val width: Int, val height: Int, val cursor: Offset?)

private fun pageState(json: JSONObject) = PageState(
    json.optString("url"), json.optString("title"), json.optBoolean("loading"), json.optInt("width"), json.optInt("height"),
    json.optJSONObject("cursor")?.let { Offset(it.optDouble("x").toFloat(), it.optDouble("y").toFloat()) },
)

/** A touch shown where it landed, at once: the page answers a moment later. */
private class Touch(val at: Offset, val id: Long) {
    val grow = Animatable(0f)
}

private val hardware = BitmapFactory.Options().apply { inPreferredConfig = android.graphics.Bitmap.Config.HARDWARE }

@Composable
private fun BrowserView(app: RemoteApp, chat: String, tab: PanelTab) {
    var picture by remember { mutableStateOf<ImageBitmap?>(null) }
    var page by remember { mutableStateOf<PageState?>(null) }
    var live by remember { mutableStateOf(false) }
    var address by remember { mutableStateOf(tab.url) }
    val addressFocus = remember { MutableInteractionSource() }
    val addressFocused by addressFocus.collectIsFocusedAsState()
    val focus = LocalFocusManager.current
    var typed by remember { mutableStateOf("") }
    var locked by remember { mutableStateOf(true) }
    var used by remember { mutableLongStateOf(0L) } // the last input, for the relock
    var nudge by remember { mutableIntStateOf(0) } // a touch while locked: the lock shakes
    val touches = remember { mutableStateListOf<Touch>() }
    val scope = rememberCoroutineScope()
    var viewport by remember { mutableStateOf<Viewport?>(null) } // null: an older 1loop, without viewport or console
    var console by remember { mutableStateOf(false) }
    val native = app.server?.localPhone == true && viewport?.native == true
    val context = LocalContext.current
    var nativeView by remember(tab.key) { mutableStateOf<android.webkit.WebView?>(null) }
    LaunchedEffect(native, tab.key) {
        nativeView = null
        if (native) {
            while (nativeView == null) {
                nativeView = AndroidLocalRuntime.get(context).browser.view(tab.key.removePrefix("browser-"))
                if (nativeView == null) delay(100)
            }
        }
    }
    var area by remember { mutableStateOf(IntSize.Zero) } // the page's view here, in pixels
    val density = LocalDensity.current
    LaunchedEffect(tab.key) { viewport = Viewport.from(app.panelTab(chat, tab.key)) }
    val resize = { width: Int, height: Int, mobile: Boolean, adaptive: Boolean, ratio: Double ->
        scope.launch {
            try {
                resizeBrowser(app, chat, tab.key, width, height, mobile, adaptive, ratio)
                viewport = Viewport.from(app.panelTab(chat, tab.key))
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                app.problem = e.message
            }
        }
    }
    // Adaptive: the page follows this view's size, in CSS pixels at the phone's density.
    val adaptive = { with(density) { resize(area.width.toDp().value.toInt(), area.height.toDp().value.toInt(), false, true, density.density.toDouble()) } }
    LaunchedEffect(area, viewport?.adaptive) {
        val shown = viewport ?: return@LaunchedEffect
        if (!shown.adaptive || area == IntSize.Zero) return@LaunchedEffect
        delay(400) // once the view has settled
        val (width, height) = with(density) { area.width.toDp().value.toInt() to area.height.toDp().value.toInt() }
        if (width.coerceIn(240, 3840) != shown.width || height.coerceIn(200, 2160) != shown.height) adaptive()
    }
    // The page's frames and state, as the computer draws them.
    LaunchedEffect(tab.key) {
        while (true) {
            runCatching {
                app.panelStream(chat, tab.key) { kind, payload ->
                    when (kind) {
                        // Decoded straight into the GPU's memory: nothing to upload when drawn.
                        'F' -> BitmapFactory.decodeByteArray(payload, 0, payload.size, hardware)?.asImageBitmap()?.let { frame ->
                            withContext(Dispatchers.Main) { picture = frame; live = true }
                        }
                        'S' -> runCatching { JSONObject(String(payload)) }.getOrNull()?.let { state ->
                            withContext(Dispatchers.Main) {
                                page = pageState(state)
                                Viewport.from(state)?.let { viewport = it }
                                live = true
                            }
                        }
                    }
                }
            }
            live = false
            delay(800)
        }
    }
    var leaving by remember { mutableStateOf<String?>(null) } // the address left by a navigation not yet begun
    LaunchedEffect(page?.url, addressFocused) {
        val url = page?.url ?: return@LaunchedEffect
        if (url != leaving) leaving = null
        if (!addressFocused && leaving == null) address = url
    }
    LaunchedEffect(locked, used) {
        if (!locked) {
            delay(RELOCK_MS)
            locked = true
        }
    }
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
                address, { address = it }, "Address", KeyboardType.Uri, ImeAction.Go, Modifier.weight(1f).alpha(dim),
                enabled = !locked, interaction = addressFocus, selectAll = true,
            ) {
                act {
                    leaving = page?.url
                    app.panelInput(chat, tab.key, "navigate", address.trim())
                }
                focus.clearFocus()
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
        viewport?.let { shown ->
            BrowserToolbar(
                shown, console, onConsole = { console = !console }, enabled = true,
                onMode = { mode ->
                    act {
                        when (mode) {
                            "Adaptive" -> adaptive()
                            "Tablet" -> resize(768, 1024, false, false, 0.0)
                            "Mobile" -> resize(390, 844, true, false, 0.0)
                            else -> resize(1280, 800, false, false, 0.0)
                        }
                    }
                },
                onCustom = { width, height -> act { resize(width, height, false, false, 0.0) } },
            )
        }
        Box(Modifier.weight(1f).fillMaxWidth().background(Palette.code).onSizeChanged { area = it }, contentAlignment = Alignment.TopCenter) {
            // Each new frame is read in the draw phase only: the view is not
            // composed again for it, only its picture painted.
            val ratio by remember { derivedStateOf { picture?.let { it.width / it.height.toFloat() } } }
            val shown = if (native && nativeView != null) viewport?.let { it.width / it.height.toFloat() } else ratio
            if (shown == null) WorkRing("wait", 34.dp, Modifier.align(Alignment.Center))
            else Box(Modifier.aspectRatio(shown)) {
                var pending by remember { mutableFloatStateOf(0f) }
                if (native && nativeView != null) {
                    NativeBrowserSurface(nativeView!!, viewport!!, locked) {
                        if (locked) nudge++ else used = System.currentTimeMillis()
                    }
                } else {
                Box(
                    Modifier.fillMaxSize()
                        .drawBehind { picture?.let { drawImage(it, dstSize = IntSize(size.width.toInt(), size.height.toInt())) } }
                        .pointerInput(tab.key) {
                            detectTapGestures { at ->
                                act {
                                    val touch = Touch(at, System.nanoTime())
                                    touches.add(touch)
                                    scope.launch {
                                        touch.grow.animateTo(1f, tween(420, easing = CssEase))
                                        touches.remove(touch)
                                    }
                                    app.panelInput(chat, tab.key, "tap", x = at.x / size.width, y = at.y / size.height)
                                }
                            }
                        }
                        .pointerInput(tab.key) {
                            var at = Offset.Zero
                            detectVerticalDragGestures(onDragStart = { at = it; pending = 0f }) { change, drag ->
                                change.consume()
                                pending -= drag
                                if (kotlin.math.abs(pending) > 6f) {
                                    val dy = pending / size.height
                                    act { app.panelScroll(chat, tab.key, at.x / size.width, at.y / size.height, dy) }
                                    pending = 0f
                                }
                            }
                        },
                )
                }
                // The touches, as rings that open and fade.
                Canvas(Modifier.fillMaxSize()) {
                    for (touch in touches) {
                        val t = touch.grow.value
                        drawCircle(Palette.amber.copy(alpha = 0.28f * (1 - t)), radius = 6.dp.toPx() + 22.dp.toPx() * t, center = touch.at)
                        drawCircle(Palette.amber.copy(alpha = 1 - t), radius = 6.dp.toPx() + 22.dp.toPx() * t, center = touch.at, style = Stroke(2.dp.toPx()))
                    }
                }
                // The agent's cursor, gliding to where it acts.
                val state = page
                val cursor = state?.cursor
                if (cursor != null && state.width > 0 && state.height > 0) {
                    BoxWithConstraints(Modifier.fillMaxSize()) {
                        val target = Offset(cursor.x / state.width * constraints.maxWidth, cursor.y / state.height * constraints.maxHeight)
                        val moving = remember { Animatable(target, Offset.VectorConverter) }
                        LaunchedEffect(target) { moving.animateTo(target, tween(260, easing = CssEase)) }
                        AgentCursor(Modifier.graphicsLayer { translationX = moving.value.x; translationY = moving.value.y })
                    }
                }
                // Loading: a thin line along the top, as in a browser.
                if (state?.loading == true) LoadingLine(Modifier.align(Alignment.TopCenter))
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
            if (!live && shown != null) Text(
                "Reconnecting…", style = Type.caption.copy(color = Palette.amber),
                modifier = Modifier.align(Alignment.TopEnd).padding(8.dp).background(Palette.popover, RoundedCornerShape(6.dp)).padding(horizontal = 8.dp, vertical = 4.dp),
            )
        }
        if (console && viewport != null) BrowserConsole(app, chat, tab.key, { action -> act(action) }, Modifier.height(220.dp))
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

// ChatGPT (Web): the chat's ChatGPT page, as 1loop drives it in this
// phone's browser for the account, kept open while the chat is shown (as on
// the desktop): ChatGPTWebLease renews it every few seconds, the engine
// closes it when the app stops, and pauses it after a while without a turn.
@Composable
fun ChatGPTWebLease(app: RemoteApp) {
    val chat = app.current
    val web = app.server?.localPhone == true && chat != null && app.meta?.chat == chat && app.meta?.provider == "chatgptweb"
    LaunchedEffect(chat, web) {
        app.chatgptPage = null
        if (!web || chat == null) return@LaunchedEffect
        var open = true // the chat was just opened: a paused page opens again
        while (true) {
            val target = app.server
            if (target != null) try {
                val reply = withContext(Dispatchers.IO) {
                    Api(target).post("/api/local/chats/${android.net.Uri.encode(chat)}/chatgpt-web", JSONObject().put("open", open))
                }
                app.chatgptPage = reply.put("chat", chat)
                open = false
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                app.chatgptPage = JSONObject().put("chat", chat).put("problem", e.message ?: "ChatGPT page unavailable.")
            }
            delay(10_000)
        }
    }
}

// The panel shows that page, locked: a touch can interrupt the automated
// conversation, so taking control is confirmed.
@Composable
private fun ChatGPTWebView(app: RemoteApp, chat: String) {
    val context = LocalContext.current
    val reply = app.chatgptPage?.takeIf { it.optString("chat") == chat }
    val page = reply?.optJSONObject("page")
    val profile = reply?.optString("profile").orEmpty()
    val problem = reply?.optString("problem")?.takeIf { it.isNotEmpty() }
    val paused = reply?.optBoolean("paused") == true
    var view by remember(chat) { mutableStateOf<android.webkit.WebView?>(null) }
    var locked by remember(chat) { mutableStateOf(true) }
    var asking by remember { mutableStateOf(false) }
    val id = page?.optString("id").orEmpty()
    LaunchedEffect(id, profile) {
        view = null
        if (id.isEmpty() || profile.isEmpty()) return@LaunchedEffect
        val browser = runCatching { AndroidLocalRuntime.get(context).chatgptBrowser(profile) }.getOrNull() ?: return@LaunchedEffect
        while (true) { // the page opens a moment after the lease; the renderer may also end it
            val shown = browser.view(id)
            if (shown !== view) view = shown
            delay(if (shown == null) 150 else 2000)
        }
    }
    Column(Modifier.fillMaxSize()) {
        Row(Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 8.dp), verticalAlignment = Alignment.CenterVertically) {
            Text(
                if (locked) "ChatGPT · the conversation 1loop drives" else "You control this page: lock it when done",
                style = Type.caption.copy(color = if (locked) Palette.muted else Palette.amber), modifier = Modifier.weight(1f), maxLines = 2,
            )
            Pill(if (locked) "Take control" else "Lock") { if (locked) asking = true else locked = true }
        }
        Box(Modifier.weight(1f).fillMaxWidth().background(Palette.code), contentAlignment = Alignment.TopCenter) {
            val shown = view
            val viewport = page?.let { Viewport(it.optInt("width"), it.optInt("height"), false, false, it.optDouble("pixelRatio", 1.0), true) }
            when {
                paused -> Text("Paused after a while without a message, to save the phone's resources. It opens again with your next message, or when you open this chat again.", style = Type.small, modifier = Modifier.padding(16.dp))
                problem != null && shown == null -> Text(problem, style = Type.small.copy(color = Palette.danger), modifier = Modifier.padding(16.dp))
                shown == null || viewport == null || viewport.width <= 0 || viewport.height <= 0 -> WorkRing("wait", 34.dp, Modifier.align(Alignment.Center))
                else -> Box(Modifier.aspectRatio(viewport.width / viewport.height.toFloat())) {
                    NativeBrowserSurface(shown, viewport, locked) {}
                }
            }
        }
    }
    if (asking) {
        androidx.compose.material3.AlertDialog(
            onDismissRequest = { asking = false }, containerColor = Palette.popover,
            title = { Text("Take control of ChatGPT?", style = Type.title) },
            text = { Text("Taps and typing can interrupt the automated conversation. Complete sign-in or security checks yourself, then lock the page again.", style = Type.small) },
            confirmButton = { androidx.compose.material3.TextButton(onClick = { asking = false; locked = false }) { Text("Take control", color = Palette.amber) } },
            dismissButton = { androidx.compose.material3.TextButton(onClick = { asking = false }) { Text("Cancel", color = Palette.muted) } },
        )
    }
}

@OptIn(ExperimentalComposeUiApi::class)
@Composable
private fun NativeBrowserSurface(view: android.webkit.WebView, viewport: Viewport, locked: Boolean, onTouch: () -> Unit) {
    val density = LocalDensity.current.density
    val width = kotlin.math.ceil(viewport.width * density.toDouble()).toInt()
    val height = kotlin.math.ceil(viewport.height * density.toDouble()).toInt()
    Layout(content = {
        AndroidView(factory = {
            (view.parent as? android.view.ViewGroup)?.removeView(view)
            view
        }, modifier = Modifier.pointerInteropFilter {
            onTouch()
            locked
        }, onRelease = { released ->
            (released.parent as? android.view.ViewGroup)?.removeView(released)
        })
    }, modifier = Modifier.fillMaxSize()) { children, constraints ->
        val child = children.single().measure(Constraints.fixed(width, height))
        val scale = minOf(constraints.maxWidth.toFloat() / width, constraints.maxHeight.toFloat() / height)
        layout(constraints.maxWidth, constraints.maxHeight) {
            child.placeWithLayer(0, 0) {
                scaleX = scale
                scaleY = scale
                transformOrigin = TransformOrigin(0f, 0f)
            }
        }
    }
}

@Composable
private fun LoadingLine(modifier: Modifier) {
    val sweep = rememberInfiniteTransition(label = "loading")
    val at by sweep.animateFloat(0f, 1f, infiniteRepeatable(tween(1100, easing = CssEaseInOut)), label = "sweep")
    Canvas(modifier.fillMaxWidth().height(2.5.dp)) {
        val span = size.width * 0.35f
        val x = -span + (size.width + span) * at
        drawRect(Palette.amber, topLeft = Offset(x, 0f), size = androidx.compose.ui.geometry.Size(span, size.height))
    }
}

@Composable
private fun Input(
    value: String, onChange: (String) -> Unit, placeholder: String, keyboard: KeyboardType, action: ImeAction,
    modifier: Modifier = Modifier, mono: Boolean = false, enabled: Boolean = true,
    interaction: MutableInteractionSource = remember { MutableInteractionSource() }, selectAll: Boolean = false, onDone: () -> Unit,
) {
    // Kept with its selection: an address is selected whole when the field
    // is touched, as in a browser, so typing replaces it.
    var field by remember { mutableStateOf(TextFieldValue(value, TextRange(value.length))) }
    if (field.text != value) field = TextFieldValue(value, TextRange(value.length))
    val focused by interaction.collectIsFocusedAsState()
    LaunchedEffect(focused) {
        if (focused && selectAll) {
            delay(30) // after the tap has placed the caret
            field = field.copy(selection = TextRange(0, field.text.length))
        }
    }
    BasicTextField(
        field, { field = it; if (it.text != value) onChange(it.text) }, modifier, enabled = enabled, singleLine = true, interactionSource = interaction, textStyle = if (mono) Type.mono else Type.body.copy(fontSize = 14.sp),
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
    // The terminal's columns and rows follow this view, as the desktop's (an older 1loop ignores it).
    val style = Type.mono.copy(fontSize = 12.sp, lineHeight = 17.sp)
    val measurer = rememberTextMeasurer()
    val cell = remember { measurer.measure("MMMMMMMMMM", style).size }
    val padding = with(LocalDensity.current) { 24.dp.toPx() }
    var grid by remember { mutableStateOf(0 to 0) }
    LaunchedEffect(grid) {
        val (columns, rows) = grid
        if (columns == 0) return@LaunchedEffect
        delay(300)
        runCatching { app.panelCall(chat, tab.key, JSONObject().put("action", "resize").put("width", columns).put("height", rows)) }
    }
    Column(Modifier.fillMaxSize()) {
        Box(
            Modifier.weight(1f).fillMaxWidth().background(Palette.code).onSizeChanged { size ->
                grid = ((size.width - padding) / (cell.width / 10f)).toInt().coerceIn(20, 400) to ((size.height - padding) / cell.height).toInt().coerceIn(5, 150)
            },
        ) {
            if (output == null) WorkRing("wait", 34.dp, Modifier.align(Alignment.Center))
            else Text(
                output.orEmpty().ifEmpty { " " }, style = style,
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
private fun ToolView(app: RemoteApp, item: Item?) {
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
        if (item.tool == "shell") CodeWell(args.optString("command"), "Command", wrap = true)
        if (item.diff.isNotEmpty()) CodeWell(item.diff, "Changes", diff = true)
        if (item.text.isNotEmpty()) CodeWell(toolPreviewText(item.text.trimEnd()), "Output", wrap = true)
        else if (item.diff.isEmpty()) Text(if (item.state == "done") "No output" else "Waiting for the result…", style = Type.small.copy(color = Palette.faint))
        if (item.failed.isNotEmpty() && item.failed != "Stopped") CodeWell(item.failed, "Error", wrap = true)
        val at = app.items.indexOf(item)
        if (item.images > 0 && at >= 0) ItemImages(app, app.firstItem + at, item.images)
    }
}

// The agent's cursor, in 1loop's colours: a rounded amber pointer with a
// dark edge, and its name beside it, as a collaborator's cursor. The
// pointer's tip is the composable's top left corner.
private val pointer = androidx.compose.ui.graphics.vector.PathParser()
    .parsePathString("M0 0 L15.6 7.3 Q16.9 7.9 15.5 8.5 L9.4 10.6 L6.7 16.6 Q6.1 17.9 5.6 16.5 Z").toPath()

@Composable
fun AgentCursor(modifier: Modifier = Modifier) {
    Box(modifier) {
        Canvas(Modifier.size(24.dp)) {
            val unit = 1.25.dp.toPx()
            scale(unit, unit, pivot = Offset.Zero) {
                translate(1.6f, 2.4f) { drawPath(pointer, Color.Black.copy(alpha = 0.35f)) } // its shadow
                translate(0.8f, 0.8f) {
                    drawPath(pointer, Palette.amber)
                    drawPath(pointer, Color(0xFF121212), style = Stroke(1.5f, join = StrokeJoin.Round))
                }
            }
        }
        Text(
            "1loop", style = Type.caption.copy(fontSize = 9.5.sp, lineHeight = 11.sp, color = Palette.ink, fontWeight = FontWeight.SemiBold),
            modifier = Modifier.padding(start = 17.dp, top = 21.dp).background(Palette.amber, RoundedCornerShape(50)).padding(horizontal = 6.dp, vertical = 1.5.dp),
        )
    }
}

// The desktop's footer under the task list: the context, and when it compacts.
@Composable
private fun ContextFooter(meta: Meta?) {
    val text = meta?.contextStatus?.ifEmpty { null } ?: when {
        meta == null || meta.window <= 0 -> "Context usage is available once a model is selected."
        else -> "Context · ${minOf(100, meta.used * 100 / meta.window)}% · ${tokens(meta.used)} / ${tokens(meta.window)} tokens"
    }
    Column {
        Box(Modifier.fillMaxWidth().height(1.dp).background(Palette.borderSoft))
        Text(text, style = Type.caption.copy(color = Palette.muted), modifier = Modifier.padding(horizontal = 18.dp, vertical = 12.dp))
    }
}
