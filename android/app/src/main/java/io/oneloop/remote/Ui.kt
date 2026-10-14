package io.oneloop.remote

import androidx.activity.compose.BackHandler
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.Crossfade
import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInHorizontally
import androidx.compose.animation.slideOutHorizontally
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsFocusedAsState
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.WindowInsetsSides
import androidx.compose.foundation.layout.only
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.layout.windowInsetsPadding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.DrawerValue
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.ModalDrawerSheet
import androidx.compose.material3.ModalNavigationDrawer
import androidx.compose.material3.Text
import androidx.compose.material3.rememberDrawerState
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import org.json.JSONObject

// ---------------------------------------------------------------------------
// Shared controls, shaped like the desktop's (ui.go tapRow styles).

@Composable
fun Pill(text: String, primary: Boolean = false, enabled: Boolean = true, icon: ImageVector? = null, onClick: () -> Unit) {
    val shape = RoundedCornerShape(50)
    val base = if (primary) Modifier.background(if (enabled) Palette.amber else Palette.raised, shape)
    else Modifier.border(1.dp, Palette.border, shape)
    Row(
        base.clip(shape).clickable(enabled = enabled, onClick = onClick).height(44.dp).padding(horizontal = 20.dp),
        verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        val ink = if (primary) Palette.ink else Palette.muted
        if (icon != null) Icon(icon, null, tint = ink, modifier = Modifier.size(18.dp))
        Text(text, style = Type.body.copy(color = if (enabled) ink else Palette.faint, fontWeight = FontWeight.Medium))
    }
}

@Composable
fun RoundButton(icon: ImageVector, fill: Color, ink: Color, size: Dp = 40.dp, iconSize: Dp = 20.dp, description: String, modifier: Modifier = Modifier, onClick: () -> Unit) {
    Box(
        modifier.size(size).clip(CircleShape).background(fill).clickable(onClick = onClick),
        contentAlignment = Alignment.Center,
    ) { Icon(icon, description, tint = ink, modifier = Modifier.size(iconSize)) }
}

@Composable
fun CopyChip(text: () -> String) {
    val clipboard = LocalClipboardManager.current
    var copied by remember { mutableStateOf(false) }
    LaunchedEffect(copied) { if (copied) { delay(1400); copied = false } }
    Row(
        Modifier.clip(RoundedCornerShape(6.dp)).clickable { clipboard.setText(AnnotatedString(text())); copied = true }
            .padding(horizontal = 8.dp, vertical = 6.dp),
        verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        Icon(Icons.copy, null, tint = Palette.muted, modifier = Modifier.size(15.dp))
        Text(if (copied) "Copied" else "Copy", style = Type.small)
    }
}

// ---------------------------------------------------------------------------
// Root

@Composable
fun Root(app: RemoteApp) {
    Box(Modifier.fillMaxSize().background(Palette.background)) {
        Crossfade(app.screen::class, animationSpec = tween(220), label = "screen") { kind ->
            when (kind) {
                Screen.Servers::class -> ServersScreen(app)
                Screen.Pair::class -> (app.screen as? Screen.Pair)?.let { PairScreen(app, it) }
                Screen.Chat::class -> ChatScreen(app)
                else -> Box(Modifier.fillMaxSize())
            }
        }
        if (app.screen == Screen.Launch) LaunchScreen { app.started() }
    }
}

// ---------------------------------------------------------------------------
// Computers on the network

@OptIn(androidx.compose.foundation.ExperimentalFoundationApi::class)
@Composable
fun ServersScreen(app: RemoteApp) {
    var adding by remember { mutableStateOf(false) }
    Column(
        Modifier.fillMaxSize().windowInsetsPadding(WindowInsets.safeDrawing).verticalScroll(rememberScrollState())
            .padding(horizontal = 24.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Spacer(Modifier.height(56.dp))
        RingMark(64.dp, intro = true)
        Spacer(Modifier.height(20.dp))
        Text("Connect to your computer", style = Type.headline, textAlign = TextAlign.Center)
        Spacer(Modifier.height(6.dp))
        Text("Pick the 1loop running on this Wi-Fi network.", style = Type.small.copy(fontSize = 15.sp), textAlign = TextAlign.Center)
        Spacer(Modifier.height(28.dp))
        Column(Modifier.widthIn(max = 520.dp).fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            for (server in app.servers) {
                val online = server.key in app.online
                Row(
                    Modifier.fillMaxWidth().clip(RoundedCornerShape(14.dp)).background(Palette.surface)
                        .border(1.dp, Palette.borderSoft, RoundedCornerShape(14.dp))
                        .combinedClickable(onClick = { app.choose(server) }, onLongClick = { app.forget(server) })
                        .padding(horizontal = 16.dp, vertical = 14.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Icon(Icons.computer, null, tint = if (online) Palette.text else Palette.faint, modifier = Modifier.size(22.dp))
                    Column(Modifier.weight(1f).padding(horizontal = 14.dp)) {
                        Text(server.name, style = Type.body.copy(fontWeight = FontWeight.Medium), maxLines = 1, overflow = TextOverflow.Ellipsis)
                        Text(
                            server.host + if (online) "" else " · not found", style = Type.caption.copy(color = Palette.muted),
                        )
                    }
                    if (server.paired) {
                        Box(Modifier.size(8.dp).clip(CircleShape).background(Palette.amber))
                        Spacer(Modifier.width(10.dp))
                    }
                    Icon(Icons.chevronRight, null, tint = Palette.faint, modifier = Modifier.size(18.dp))
                }
            }
            if (app.scanning) {
                Row(Modifier.fillMaxWidth().padding(vertical = 14.dp), horizontalArrangement = Arrangement.Center, verticalAlignment = Alignment.CenterVertically) {
                    WorkRing("wait", 26.dp)
                    Spacer(Modifier.width(12.dp))
                    Text("Searching the network…", style = Type.small)
                }
            } else if (app.servers.isEmpty()) {
                Text(
                    "No 1loop found. Open 1loop on your computer, on the same Wi-Fi, and check Settings → Phone remote.",
                    style = Type.small, textAlign = TextAlign.Center, modifier = Modifier.fillMaxWidth().padding(vertical = 14.dp),
                )
            }
            if (app.servers.any { it.paired }) {
                Text("Long press a computer to forget it.", style = Type.caption, textAlign = TextAlign.Center, modifier = Modifier.fillMaxWidth())
            }
        }
        app.problem?.let { Text(it, style = Type.small.copy(color = Palette.danger), textAlign = TextAlign.Center, modifier = Modifier.padding(top = 14.dp)) }
        Spacer(Modifier.height(24.dp))
        Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            Pill("Search again", icon = Icons.refresh, enabled = !app.scanning) { app.scan() }
            Pill("Add by address", icon = Icons.plus) { adding = true }
        }
        Spacer(Modifier.height(32.dp))
    }
    if (adding) AddressSheet(onDone = { adding = false }) { app.addManually(it) }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun AddressSheet(onDone: () -> Unit, onAdd: (String) -> Unit) {
    var address by remember { mutableStateOf("") }
    val focus = remember { FocusRequester() }
    ModalBottomSheet(onDismissRequest = onDone, containerColor = Palette.surface, sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)) {
        Column(Modifier.padding(horizontal = 24.dp).padding(bottom = 24.dp).imePadding()) {
            Text("Add by address", style = Type.title)
            Spacer(Modifier.height(6.dp))
            Text("The computer's local address, as in 192.168.1.20 (port 47101 unless changed).", style = Type.small)
            Spacer(Modifier.height(16.dp))
            Field(address, { address = it }, "192.168.1.20", KeyboardType.Uri, Modifier.focusRequester(focus))
            Spacer(Modifier.height(16.dp))
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
                Pill("Connect", primary = true, enabled = address.isNotBlank()) { onAdd(address); onDone() }
            }
        }
    }
    LaunchedEffect(Unit) { focus.requestFocus() }
}

@Composable
private fun Field(value: String, onChange: (String) -> Unit, placeholder: String, keyboard: KeyboardType, modifier: Modifier = Modifier) {
    val interaction = remember { MutableInteractionSource() }
    val focused by interaction.collectIsFocusedAsState()
    val border by animateColorAsState(if (focused) Palette.borderHot.copy(alpha = 1f) else Palette.border, tween(160), label = "border")
    BasicTextField(
        value, onChange, modifier.fillMaxWidth(), singleLine = true, textStyle = Type.body,
        cursorBrush = SolidColor(Palette.amber), keyboardOptions = KeyboardOptions(keyboardType = keyboard), interactionSource = interaction,
        decorationBox = { inner ->
            Box(
                Modifier.background(Palette.code, RoundedCornerShape(10.dp)).border(1.dp, border, RoundedCornerShape(10.dp))
                    .padding(horizontal = 14.dp, vertical = 12.dp),
            ) {
                if (value.isEmpty()) Text(placeholder, style = Type.body.copy(color = Palette.faint))
                inner()
            }
        },
    )
}

// ---------------------------------------------------------------------------
// Pairing: the six digits shown on the computer.

@Composable
fun PairScreen(app: RemoteApp, pair: Screen.Pair) {
    var code by remember { mutableStateOf("") }
    val focus = remember { FocusRequester() }
    BackHandler { app.screen = Screen.Servers }
    Column(
        Modifier.fillMaxSize().windowInsetsPadding(WindowInsets.safeDrawing).imePadding().padding(horizontal = 24.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Row(Modifier.fillMaxWidth().padding(top = 8.dp)) {
            RoundButton(Icons.close, Color.Transparent, Palette.muted, description = "Back") { app.screen = Screen.Servers }
        }
        Spacer(Modifier.height(32.dp))
        RingMark(56.dp)
        Spacer(Modifier.height(20.dp))
        Text("Pair with ${pair.server.name}", style = Type.headline, textAlign = TextAlign.Center)
        Spacer(Modifier.height(8.dp))
        Text("Type the code 1loop shows on your computer.", style = Type.small.copy(fontSize = 15.sp), textAlign = TextAlign.Center)
        Spacer(Modifier.height(32.dp))
        BasicTextField(
            code, { new -> code = new.filter(Char::isDigit).take(6); if (code.length == 6) app.confirm(pair, code) },
            Modifier.focusRequester(focus), keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number, autoCorrectEnabled = false),
            cursorBrush = SolidColor(Color.Transparent),
            decorationBox = {
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    for (i in 0 until 6) {
                        val filled = i < code.length
                        val active = i == code.length
                        Box(
                            Modifier.size(46.dp, 56.dp).background(Palette.surface, RoundedCornerShape(12.dp))
                                .border(1.dp, if (active) Palette.amber else Palette.border, RoundedCornerShape(12.dp)),
                            contentAlignment = Alignment.Center,
                        ) {
                            Text(if (filled) code[i].toString() else "", style = Type.headline.copy(fontWeight = FontWeight.Medium))
                        }
                        if (i == 2) Spacer(Modifier.width(8.dp))
                    }
                }
            },
        )
        Spacer(Modifier.height(20.dp))
        if (app.busy) WorkRing("wait", 28.dp)
        app.problem?.let { Text(it, style = Type.small.copy(color = Palette.danger), textAlign = TextAlign.Center) }
        Spacer(Modifier.height(20.dp))
        Text(
            "A paired phone can send messages and approve commands on the computer.",
            style = Type.caption, textAlign = TextAlign.Center, modifier = Modifier.widthIn(max = 320.dp),
        )
    }
    LaunchedEffect(Unit) { focus.requestFocus() }
    LaunchedEffect(app.problem) { if (app.problem != null) code = "" }
}

// ---------------------------------------------------------------------------
// Chats

@Composable
fun ChatScreen(app: RemoteApp) {
    BoxWithConstraints(Modifier.fillMaxSize()) {
        if (maxWidth >= 720.dp) {
            Row(Modifier.fillMaxSize()) {
                ChatsPane(app, Modifier.width(300.dp).fillMaxHeight()) {}
                Conversation(app, null, Modifier.weight(1f))
            }
        } else {
            val drawer = rememberDrawerState(DrawerValue.Closed)
            val scope = rememberCoroutineScope()
            BackHandler(drawer.isOpen) { scope.launch { drawer.close() } }
            ModalNavigationDrawer(
                drawerState = drawer,
                drawerContent = {
                    ModalDrawerSheet(drawerContainerColor = Palette.sidebar, drawerShape = RoundedCornerShape(topEnd = 18.dp, bottomEnd = 18.dp)) {
                        ChatsPane(app, Modifier.fillMaxHeight()) { scope.launch { drawer.close() } }
                    }
                },
            ) {
                Conversation(app, onMenu = { scope.launch { drawer.open() } }, Modifier.fillMaxSize())
            }
        }
    }
    val approval = app.approvals.values.firstOrNull()
    if (approval != null) ApprovalSheet(app, approval)
}

@Composable
private fun ChatsPane(app: RemoteApp, modifier: Modifier, onPicked: () -> Unit) {
    Column(modifier.background(Palette.sidebar).windowInsetsPadding(WindowInsets.safeDrawing).padding(horizontal = 12.dp)) {
        Image(painterResource(R.drawable.logo), "1loop", modifier = Modifier.padding(start = 6.dp, top = 14.dp, bottom = 14.dp).height(28.dp))
        NavRow("New chat", Icons.plus, selected = app.current == null) { app.open(null); onPicked() }
        Column(Modifier.weight(1f).verticalScroll(rememberScrollState())) {
            val groups = app.chats.groupBy { it.workspace }
            for ((workspace, chats) in groups) {
                Text(workspace.substringAfterLast('/'), style = Type.small.copy(color = Palette.faint), modifier = Modifier.padding(start = 10.dp, top = 18.dp, bottom = 6.dp))
                for (chat in chats) {
                    NavRow(chat.title, null, selected = chat.id == app.current, running = chat.running) { app.open(chat.id); onPicked() }
                }
            }
            if (app.chats.isEmpty()) Text("No chats yet", style = Type.small.copy(color = Palette.faint), modifier = Modifier.padding(10.dp))
        }
        Box(Modifier.fillMaxWidth().height(1.dp).background(Palette.borderSoft))
        Row(
            Modifier.fillMaxWidth().padding(vertical = 8.dp).clip(RoundedCornerShape(8.dp)).clickable { app.leave() }
                .padding(horizontal = 10.dp, vertical = 10.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Icon(Icons.computer, null, tint = Palette.muted, modifier = Modifier.size(18.dp))
            Column(Modifier.weight(1f).padding(start = 10.dp)) {
                Text(app.server?.name.orEmpty(), style = Type.body.copy(fontSize = 14.sp), maxLines = 1, overflow = TextOverflow.Ellipsis)
                Text(if (app.connected) "Connected · change computer" else "Reconnecting…", style = Type.caption.copy(color = if (app.connected) Palette.muted else Palette.amber))
            }
        }
    }
}

@Composable
private fun NavRow(text: String, icon: ImageVector?, selected: Boolean, running: Boolean = false, onClick: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(8.dp)).background(if (selected) Palette.selected else Color.Transparent)
            .clickable(onClick = onClick).padding(horizontal = 10.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (icon != null) {
            Icon(icon, null, tint = Palette.text, modifier = Modifier.size(18.dp))
            Spacer(Modifier.width(10.dp))
        }
        Text(text, style = Type.body.copy(fontSize = 14.sp, color = if (selected) Palette.text else Color(0xFFB4B4B4)), maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
        if (running) Box(Modifier.size(7.dp).clip(CircleShape).background(Palette.amber))
    }
}

@Composable
private fun Conversation(app: RemoteApp, onMenu: (() -> Unit)?, modifier: Modifier) {
    val meta = app.meta
    val running = meta?.running == true
    var tool by remember { mutableStateOf<Item?>(null) }
    var panel by remember { mutableStateOf(false) }
    Box(modifier) {
    Column(Modifier.fillMaxSize().background(Palette.background).windowInsetsPadding(WindowInsets.safeDrawing.only(WindowInsetsSides.Top + WindowInsetsSides.Horizontal))) {
        // Title bar
        Row(Modifier.fillMaxWidth().padding(horizontal = 8.dp, vertical = 6.dp), verticalAlignment = Alignment.CenterVertically) {
            if (onMenu != null) RoundButton(Icons.sidebar, Color.Transparent, Palette.muted, description = "Chats", onClick = onMenu)
            Column(Modifier.weight(1f).padding(horizontal = 8.dp)) {
                val title = app.chats.firstOrNull { it.id == app.current }?.title ?: "New chat"
                Text(title, style = Type.body.copy(fontWeight = FontWeight.Medium), maxLines = 1, overflow = TextOverflow.Ellipsis)
                Text(
                    if (app.connected) app.chatModel?.name ?: app.server?.name.orEmpty() else "Reconnecting to ${app.server?.name}…",
                    style = Type.caption.copy(color = if (app.connected) Palette.faint else Palette.amber), maxLines = 1, overflow = TextOverflow.Ellipsis,
                )
            }
            if (app.current != null) Box {
                RoundButton(Icons.panel, Color.Transparent, Palette.muted, description = "Panel: tasks, browser, terminal") { panel = true }
                // A task in progress: the panel has news.
                if (meta?.todo?.any { it.status == "in_progress" } == true) {
                    Box(Modifier.align(Alignment.TopEnd).padding(top = 8.dp, end = 8.dp).size(7.dp).clip(CircleShape).background(Palette.amber))
                }
            }
            RoundButton(Icons.plus, Color.Transparent, Palette.muted, description = "New chat") { app.open(null) }
        }
        Box(Modifier.weight(1f).fillMaxWidth()) {
            if (app.current == null) Welcome(app) else Transcript(app, running) { tool = it }
            // The conversation fades under the composer instead of a hard edge.
            Box(Modifier.align(Alignment.BottomCenter).fillMaxWidth().height(18.dp).background(Brush.verticalGradient(listOf(Color.Transparent, Palette.background))))
        }
        Composer(app, running)
    }
    // The panel slides in from the right, over the chat, as on the computer.
    val chat = app.current
    var shownChat by remember { mutableStateOf(chat) }
    if (chat != null) shownChat = chat
    AnimatedVisibility(
        panel && chat != null,
        // Opaque and only moved: no transparency to composite at each frame.
        enter = slideInHorizontally(tween(300, easing = CssEase)) { it },
        exit = slideOutHorizontally(tween(240, easing = CssEase)) { it },
    ) {
        shownChat?.let { PanelScreen(app, it) { panel = false } }
    }
    }
    tool?.let { ToolSheet(it) { tool = null } }
}

@Composable
private fun Welcome(app: RemoteApp) {
    Column(Modifier.fillMaxSize().padding(24.dp), verticalArrangement = Arrangement.Center, horizontalAlignment = Alignment.CenterHorizontally) {
        RingMark(60.dp, intro = true)
        Spacer(Modifier.height(18.dp))
        Text("What shall we build today?", style = Type.headline, textAlign = TextAlign.Center)
        Spacer(Modifier.height(4.dp))
        Text("Your project. Your model. One loop.", style = Type.small.copy(fontSize = 15.sp))
    }
}

@Composable
private fun Transcript(app: RemoteApp, running: Boolean, onTool: (Item) -> Unit) {
    val list = rememberLazyListState()
    var follow by remember { mutableStateOf(true) }
    val items = app.items
    val lastUser by remember { derivedStateOf { items.indexOfLast { it.kind == "user" } } }
    // Following the newest line until the reader scrolls up, like the desktop.
    LaunchedEffect(list) {
        snapshotFlow { list.isScrollInProgress }.collect { scrolling -> if (!scrolling) follow = !list.canScrollForward }
    }
    val tail by remember { derivedStateOf { items.size to (items.lastOrNull()?.text?.length ?: 0) } }
    LaunchedEffect(tail, running, app.meta?.label) {
        if (follow && items.isNotEmpty()) list.scrollToItem(items.size + 1, 0)
    }
    LazyColumn(
        state = list, modifier = Modifier.fillMaxSize(),
        contentPadding = PaddingValues(horizontal = 16.dp, vertical = 12.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        itemsIndexed(items, contentType = { _, item -> item.kind }) { index, item ->
            Box(Modifier.widthIn(max = 760.dp).fillMaxWidth()) {
                when (item.kind) {
                    "user" -> UserBubble(item)
                    "assistant" -> Column {
                        Markdown(item.text)
                        // One Copy per finished answer: under its last text, not while it runs.
                        val endsTurn = (index + 1 until items.size).takeWhile { items[it].kind != "user" }.none { items[it].kind == "assistant" }
                        if (endsTurn && !(running && index > lastUser)) {
                            CopyChip { answerText(items, index) }
                        }
                    }
                    "tool" -> ToolRow(item) { onTool(item) }
                    "error" -> Text(item.text, style = Type.body.copy(color = Palette.danger))
                    "note" -> Text(item.text, style = Type.caption, textAlign = TextAlign.Center, modifier = Modifier.fillMaxWidth())
                }
            }
        }
        item {
            AnimatedVisibility(running, enter = fadeIn(), exit = fadeOut()) {
                Row(Modifier.widthIn(max = 760.dp).fillMaxWidth().padding(top = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                    WorkRing(app.meta?.mark?.ifEmpty { "wait" } ?: "wait", 38.dp)
                    Spacer(Modifier.width(12.dp))
                    Text(app.meta?.label.orEmpty(), style = Type.body.copy(color = Palette.muted), maxLines = 2, overflow = TextOverflow.Ellipsis)
                }
            }
        }
    }
}

private fun answerText(items: List<Item>, index: Int): String {
    val parts = mutableListOf<String>()
    var i = index
    while (i >= 0 && items[i].kind != "user") {
        if (items[i].kind == "assistant" && items[i].text.isNotBlank()) parts.add(0, items[i].text.trim())
        i--
    }
    return parts.joinToString("\n\n")
}

@Composable
private fun UserBubble(item: Item) {
    Column(Modifier.fillMaxWidth(), horizontalAlignment = Alignment.End) {
        Box(Modifier.widthIn(max = 560.dp).fillMaxWidth(0.85f), contentAlignment = Alignment.CenterEnd) {
            Text(
                item.text.ifEmpty { "[image]" } + if (item.images > 0 && item.text.isNotEmpty()) "  [+${item.images} image]" else "",
                style = Type.body,
                modifier = Modifier.background(Palette.raised, RoundedCornerShape(18.dp)).padding(horizontal = 14.dp, vertical = 10.dp),
            )
        }
        if (item.text.isNotEmpty()) CopyChip { item.text }
    }
}

@Composable
private fun ToolRow(item: Item, onClick: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(8.dp)).clickable(onClick = onClick).padding(horizontal = 8.dp, vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(Icons.forTool(item.tool), null, tint = Palette.muted, modifier = Modifier.size(16.dp))
        Spacer(Modifier.width(8.dp))
        Text(item.verb, style = Type.body.copy(fontSize = 14.sp, color = Palette.muted))
        Spacer(Modifier.width(6.dp))
        Text(
            item.target, style = if (item.mono) Type.mono else Type.body.copy(fontSize = 14.sp), maxLines = 1,
            overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f, fill = false),
        )
        if (item.meta.isNotEmpty()) {
            Spacer(Modifier.width(10.dp))
            Text(item.meta, style = Type.caption.copy(fontSize = 12.5.sp, color = toneColor(item.tone)), maxLines = 1)
        }
    }
}

private fun toneColor(tone: String) = when (tone) {
    "amber" -> Palette.amber
    "danger" -> Palette.danger
    "added" -> Palette.added
    else -> Palette.faint
}

// ---------------------------------------------------------------------------
// Composer: Send when idle; while the task runs, Send queues the message for
// the next turn and the arrow steers it into the current task.

@Composable
private fun Composer(app: RemoteApp, running: Boolean) {
    var text by remember { mutableStateOf("") }
    var project by remember { mutableStateOf<String?>(null) }
    var models by remember { mutableStateOf(false) }
    val interaction = remember { MutableInteractionSource() }
    val focused by interaction.collectIsFocusedAsState()
    val border by animateColorAsState(if (focused) Palette.borderHot else Palette.border, tween(160), label = "card")
    Column(
        Modifier.fillMaxWidth().navigationBarsPadding().imePadding().padding(horizontal = 12.dp).padding(bottom = 10.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        val meta = app.meta
        if (meta != null && (meta.queue.isNotEmpty() || meta.steer.isNotEmpty())) QueueTray(meta)
        app.problem?.let { Text(it, style = Type.small.copy(color = Palette.danger), modifier = Modifier.padding(bottom = 6.dp)) }
        // The desktop's context line: the project and the computer.
        Row(Modifier.widthIn(max = 760.dp).fillMaxWidth().padding(bottom = 4.dp), verticalAlignment = Alignment.CenterVertically) {
            ProjectChip(app, project) { project = it }
            Spacer(Modifier.width(4.dp))
            Icon(Icons.computer, null, tint = Palette.muted, modifier = Modifier.size(15.dp))
            Spacer(Modifier.width(6.dp))
            Text(app.server?.name.orEmpty(), style = Type.small, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
        Column(
            Modifier.widthIn(max = 760.dp).fillMaxWidth().background(Palette.surface, RoundedCornerShape(18.dp))
                .border(1.dp, border, RoundedCornerShape(18.dp)).padding(start = 14.dp, end = 8.dp, top = 6.dp, bottom = 8.dp),
        ) {
            BasicTextField(
                text, { text = it }, Modifier.fillMaxWidth().padding(top = 8.dp, bottom = 6.dp, end = 6.dp),
                textStyle = Type.body, cursorBrush = SolidColor(Palette.amber), maxLines = 8, interactionSource = interaction,
                decorationBox = { inner ->
                    Box {
                        if (text.isEmpty()) Text(if (running) "Queue a message…" else "Describe what you want to build…", style = Type.body.copy(color = Palette.faint))
                        inner()
                    }
                },
            )
            Row(verticalAlignment = Alignment.CenterVertically) {
                // The chips take what the buttons leave, and the buttons sit
                // against the right edge: a long model name shrinks, not them.
                Row(Modifier.weight(1f), verticalAlignment = Alignment.CenterVertically) {
                    ApprovalChip(app)
                    Spacer(Modifier.width(4.dp))
                    ModelChip(app, onClick = { models = true }, modifier = Modifier.weight(1f, fill = false))
                }
                val meta = app.meta
                ContextRing(meta?.used?.takeIf { app.current != null } ?: 0, meta?.window?.takeIf { app.current != null && it > 0 } ?: app.defaultWindow)
                val send = { mode: String ->
                    val message = text.trim()
                    if (message.isNotEmpty()) {
                        app.send(message, mode, project)
                        text = ""
                    }
                }
                if (running) {
                    if (text.isNotBlank()) {
                        RoundButton(Icons.arrowTurn, Palette.raised, Palette.amber, size = 36.dp, iconSize = 18.dp, description = "Steer now") { send("steer") }
                        Spacer(Modifier.width(8.dp))
                        RoundButton(Icons.send, Palette.amber, Palette.ink, size = 36.dp, description = "Queue") { send("queue") }
                        Spacer(Modifier.width(8.dp))
                    }
                    RoundButton(Icons.close, Palette.text, Palette.ink, size = 36.dp, iconSize = 18.dp, description = "Stop") { app.stop() }
                } else {
                    RoundButton(Icons.send, if (text.isBlank()) Palette.raised else Palette.amber, if (text.isBlank()) Palette.faint else Palette.ink, size = 36.dp, description = "Send") { send("send") }
                }
            }
        }
    }
    if (models) ModelSheet(app) { models = false }
}

// The project a new chat starts in; an open chat shows its own.
@Composable
private fun ProjectChip(app: RemoteApp, chosen: String?, onChoose: (String) -> Unit) {
    var open by remember { mutableStateOf(false) }
    val chat = app.chats.firstOrNull { it.id == app.current }
    val path = chat?.workspace ?: chosen ?: app.workspace
    Box {
        Row(
            Modifier.clip(RoundedCornerShape(8.dp)).clickable(enabled = chat == null && app.projects.size > 1) { open = true }
                .padding(horizontal = 6.dp, vertical = 6.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Icon(Icons.folder, null, tint = Palette.muted, modifier = Modifier.size(16.dp))
            Spacer(Modifier.width(6.dp))
            Text(path.substringAfterLast('/'), style = Type.small, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.widthIn(max = 160.dp))
        }
        DropdownMenu(open, { open = false }, containerColor = Palette.popover) {
            for (p in app.projects) DropdownMenuItem(
                text = { Text(p.substringAfterLast('/'), style = Type.body) },
                onClick = { onChoose(p); open = false },
                leadingIcon = { Icon(if (p == path) Icons.check else Icons.folder, null, tint = Palette.muted, modifier = Modifier.size(16.dp)) },
            )
        }
    }
}

@Composable
private fun QueueTray(meta: Meta) {
    Column(
        Modifier.widthIn(max = 728.dp).fillMaxWidth().background(Palette.popover, RoundedCornerShape(topStart = 12.dp, topEnd = 12.dp))
            .border(1.dp, Palette.borderSoft, RoundedCornerShape(topStart = 12.dp, topEnd = 12.dp)).padding(horizontal = 12.dp, vertical = 8.dp),
        verticalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        Text(
            if (meta.queue.isEmpty()) "Steering the current task" else "${meta.queue.size} queued" + if (meta.steer.isNotEmpty()) " · ${meta.steer.size} steering" else "",
            style = Type.caption.copy(color = Palette.muted),
        )
        for (s in meta.steer) QueueLine(s, null)
        meta.queue.forEachIndexed { i, q -> QueueLine(q, i + 1) }
    }
}

@Composable
private fun QueueLine(text: String, number: Int?) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        Box(Modifier.size(20.dp).clip(CircleShape).background(if (number == null) Color.Transparent else Palette.border), contentAlignment = Alignment.Center) {
            if (number == null) Icon(Icons.arrowTurn, null, tint = Palette.amber, modifier = Modifier.size(15.dp))
            else Text("$number", style = Type.caption.copy(fontSize = 11.sp, color = Palette.muted))
        }
        Spacer(Modifier.width(10.dp))
        Text(text.replace('\n', ' '), style = Type.body.copy(fontSize = 14.sp, color = Palette.muted), maxLines = 1, overflow = TextOverflow.Ellipsis)
    }
}

// ---------------------------------------------------------------------------
// Sheets

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun ToolSheet(item: Item, onDone: () -> Unit) {
    val args = remember(item.args) { runCatching { JSONObject(item.args) }.getOrDefault(JSONObject()) }
    ModalBottomSheet(onDismissRequest = onDone, containerColor = Palette.surface, sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)) {
        Column(Modifier.verticalScroll(rememberScrollState()).padding(horizontal = 20.dp).padding(bottom = 28.dp), verticalArrangement = Arrangement.spacedBy(14.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Icon(Icons.forTool(item.tool), null, tint = Palette.text, modifier = Modifier.size(20.dp))
                Spacer(Modifier.width(10.dp))
                Text(if (item.tool == "shell") "${item.verb} a command" else "${item.verb} ${item.target}", style = Type.title)
            }
            Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                val (status, color) = when {
                    item.state == "pending" -> "Awaiting your approval" to Palette.amber
                    item.state == "running" -> "Running…" to Palette.amber
                    item.failed == "Stopped" -> "Stopped before it finished" to Palette.muted
                    item.failed.isNotEmpty() -> item.meta.ifEmpty { "Failed" } to Palette.danger
                    item.tool == "shell" -> "Exit 0" to Palette.added
                    else -> "Done" to Palette.added
                }
                Text(status, style = Type.small.copy(color = color))
                if (item.tool == "edit_file" && item.meta.isNotEmpty()) Text(item.meta, style = Type.small.copy(color = Palette.added))
                if (item.millis > 0) Text(elapsed(item.millis), style = Type.small)
            }
            val running = item.state != "done"
            when (item.tool) {
                "shell" -> {
                    CodeWell(args.optString("command"), "Command")
                    Section("Output", item.text, if (running) "Waiting for the result…" else "No output")
                }
                "edit_file" -> if (item.diff.isNotEmpty()) CodeWell(item.diff, "Changes", diff = true) else Text("No diff recorded", style = Type.small)
                "read_file" -> Section("Content", item.text, if (running) "Waiting for the result…" else "Empty file")
                "list_directory" -> Section("Entries", item.text, if (running) "Waiting for the result…" else "Empty directory")
                else -> Section("Output", item.text, "No output")
            }
            if (item.failed.isNotEmpty() && item.failed != "Stopped") CodeWell(item.failed, "Error")
        }
    }
}

@Composable
private fun Section(name: String, text: String, empty: String) {
    if (text.isEmpty()) Text(empty, style = Type.small.copy(color = Palette.faint)) else CodeWell(text.trimEnd(), name)
}

private fun elapsed(ms: Long) = when {
    ms < 1000 -> "$ms ms"
    ms < 60000 -> String.format("%.1f s", ms / 1000.0).replace(".0 s", " s")
    else -> "${ms / 60000}m ${(ms / 1000 % 60).toString().padStart(2, '0')}s"
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun ApprovalSheet(app: RemoteApp, approval: Approval) {
    val state = rememberModalBottomSheetState(skipPartiallyExpanded = true, confirmValueChange = { true })
    val title = app.chats.firstOrNull { it.id == approval.chat }?.title.orEmpty()
    ModalBottomSheet(onDismissRequest = {}, sheetState = state, containerColor = Palette.surface) {
        Column(Modifier.verticalScroll(rememberScrollState()).padding(horizontal = 20.dp).padding(bottom = 24.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                WorkRing("approval", 34.dp)
                Spacer(Modifier.width(12.dp))
                Column {
                    Text(approval.title, style = Type.title)
                    Text(title, style = Type.caption.copy(color = Palette.muted), maxLines = 1, overflow = TextOverflow.Ellipsis)
                }
            }
            Text("1loop needs your approval to perform this action on the computer.", style = Type.small)
            CodeWell(approval.preview, "Preview", diff = approval.preview.startsWith("---"))
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(10.dp, Alignment.End)) {
                Pill("Deny") { app.answer(approval, false) }
                Pill("Allow", primary = true) { app.answer(approval, true) }
            }
        }
    }
}
