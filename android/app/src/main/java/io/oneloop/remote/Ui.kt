package io.oneloop.remote

import android.content.ClipData
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
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
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import androidx.compose.foundation.gestures.awaitHorizontalTouchSlopOrCancellation
import androidx.compose.foundation.gestures.horizontalDrag
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
import androidx.compose.material3.SheetValue
import androidx.compose.material3.Text
import androidx.compose.material3.rememberDrawerState
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
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
import androidx.compose.ui.platform.ClipEntry
import androidx.compose.ui.platform.LocalClipboard
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.isCtrlPressed
import androidx.compose.ui.input.key.isShiftPressed
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.input.pointer.positionChange
import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.TextFieldValue
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.compose.LocalLifecycleOwner
import com.journeyapps.barcodescanner.ScanContract
import com.journeyapps.barcodescanner.ScanOptions
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import org.json.JSONObject
import java.util.Locale

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
fun RoundButton(icon: ImageVector, fill: Color, ink: Color, description: String, modifier: Modifier = Modifier, size: Dp = 40.dp, iconSize: Dp = 20.dp, onClick: () -> Unit) {
    Box(
        modifier.size(size).clip(CircleShape).background(fill).clickable(onClick = onClick),
        contentAlignment = Alignment.Center,
    ) { Icon(icon, description, tint = ink, modifier = Modifier.size(iconSize)) }
}

@Composable
fun CopyChip(text: () -> String) {
    val clipboard = LocalClipboard.current
    val scope = rememberCoroutineScope()
    var copied by remember { mutableStateOf(false) }
    LaunchedEffect(copied) { if (copied) { delay(1400); copied = false } }
    Row(
        Modifier.clip(RoundedCornerShape(6.dp)).clickable {
            val clip = ClipData.newPlainText("1loop", text())
            scope.launch { clipboard.setClipEntry(ClipEntry(clip)) }
            copied = true
        }
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
    val focus = LocalFocusManager.current
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    DisposableEffect(lifecycle, focus) {
        val observer = LifecycleEventObserver { _, event ->
            // A retained text field must not reopen the IME when the app returns.
            if (event == Lifecycle.Event.ON_STOP) focus.clearFocus(force = true)
        }
        lifecycle.addObserver(observer)
        onDispose { lifecycle.removeObserver(observer) }
    }
    LaunchedEffect(app.updates.latest?.code, app.screen == Screen.Launch) {
        val release = app.updates.latest
        if (app.screen != Screen.Launch && app.updates.available && release != null && app.updateOffered != release.code) {
            app.updateOffered = release.code
            app.updateAsked = true
        }
    }
    Box(Modifier.fillMaxSize().background(Palette.background)) {
        Crossfade(app.screen::class, animationSpec = tween(220), label = "screen") { kind ->
            when (kind) {
                Screen.Mode::class -> ModeScreen(app::chooseTarget)
                Screen.Servers::class -> ServersScreen(app)
                Screen.LocalSetup::class -> {
                    val status by app.local.status.collectAsState()
                    LocalSetupScreen(status, app::prepareLocal, app::startLocal, app::switchTarget, app.starting, app.problem)
                }
                Screen.Pair::class -> (app.screen as? Screen.Pair)?.let { PairScreen(app, it) }
                Screen.Chat::class -> ChatScreen(app)
                else -> Box(Modifier.fillMaxSize())
            }
        }
        if (app.screen == Screen.Launch) LaunchScreen { app.started() }
        // Without a connection Settings is out of reach: a new version shows here.
        if (app.screen != Screen.Launch && app.screen != Screen.Chat && app.updates.available && !app.updateLater) {
            UpdateCard(app, Modifier.align(Alignment.BottomCenter))
        }
        if (app.updateAsked) UpdateDialog(app) { app.updateAsked = false }
    }
}

@Composable
private fun UpdateCard(app: RemoteApp, modifier: Modifier) {
    Row(
        modifier.windowInsetsPadding(WindowInsets.safeDrawing).padding(16.dp).fillMaxWidth()
            .clip(RoundedCornerShape(14.dp)).background(Palette.surface).border(1.dp, Palette.borderSoft, RoundedCornerShape(14.dp))
            .padding(horizontal = 14.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        Text("1loop ${app.updates.latest?.version} is available", style = Type.body, modifier = Modifier.weight(1f))
        Pill("Later") { app.updateLater = true }
        Pill("Update", primary = true) { app.updateAsked = true }
    }
}

// ---------------------------------------------------------------------------
// Computers on the network

// From Android 17, an app that targets it reaches the devices on the local
// network (a computer on this Wi-Fi, its discovery) only once allowed to.
private fun localNetworkAllowed(context: Context) =
    Build.VERSION.SDK_INT < 37 || context.checkSelfPermission(android.Manifest.permission.ACCESS_LOCAL_NETWORK) == PackageManager.PERMISSION_GRANTED

// Remote desktop: the computers on this Wi-Fi, found or scanned from their
// QR code; Remote server: the HTTPS servers added by address.
@OptIn(androidx.compose.foundation.ExperimentalFoundationApi::class)
@Composable
fun ServersScreen(app: RemoteApp) {
    var adding by remember { mutableStateOf(false) }
    val desktop = app.target != ExecutionTarget.RemoteServer
    val shown = app.servers.filter { it.desktop == desktop }
    val scanner = rememberLauncherForActivityResult(ScanContract()) { result -> result.contents?.let(app::pairFromLink) }
    val context = LocalContext.current
    var lanAllowed by remember { mutableStateOf(localNetworkAllowed(context)) }
    var lanAsked by remember { mutableStateOf(0) }
    val askLan = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        lanAllowed = granted
        if (granted) app.scan()
    }
    val allowLan = {
        // Android stops asking after two refusals: the setting is then in the app's details.
        if (lanAsked < 2) {
            lanAsked++
            askLan.launch(android.Manifest.permission.ACCESS_LOCAL_NETWORK)
        } else {
            context.startActivity(Intent(android.provider.Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.fromParts("package", context.packageName, null)))
        }
    }
    LaunchedEffect(desktop) { if (desktop && !lanAllowed) allowLan() }
    // Back from the app's settings: allowed there.
    LifecycleResumeEffect(Unit) {
        if (!lanAllowed && localNetworkAllowed(context)) { lanAllowed = true; app.scan() }
        onPauseOrDispose {}
    }
    BackHandler { app.switchTarget() }
    Column(
        Modifier.fillMaxSize().windowInsetsPadding(WindowInsets.safeDrawing).verticalScroll(rememberScrollState())
            .padding(horizontal = 24.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Row(Modifier.fillMaxWidth().padding(top = 8.dp)) {
            RoundButton(Icons.back, Color.Transparent, Palette.muted, description = "Back") { app.switchTarget() }
        }
        Spacer(Modifier.height(8.dp))
        RingMark(64.dp, intro = true)
        Spacer(Modifier.height(20.dp))
        Text(if (desktop) "Remote desktop" else "Remote server", style = Type.headline, textAlign = TextAlign.Center)
        Spacer(Modifier.height(6.dp))
        Text(
            if (desktop) "Pick a computer on this Wi-Fi." else "Add a 1loop server by its HTTPS address.",
            style = Type.small.copy(fontSize = 15.sp), textAlign = TextAlign.Center,
        )
        Spacer(Modifier.height(28.dp))
        Column(Modifier.widthIn(max = 520.dp).fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            for (server in shown) {
                val online = server.key in app.online
                Row(
                    Modifier.fillMaxWidth().clip(RoundedCornerShape(14.dp)).background(Palette.surface)
                        .border(1.dp, Palette.borderSoft, RoundedCornerShape(14.dp))
                        .combinedClickable(onClick = { app.choose(server) }, onLongClick = { app.forget(server) })
                        .padding(horizontal = 16.dp, vertical = 14.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Icon(if (desktop) Icons.computer else Icons.server, null, tint = if (online) Palette.text else Palette.faint, modifier = Modifier.size(22.dp))
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
            if (desktop && !lanAllowed) {
                Text(
                    "1loop needs access to devices on your local network to find and reach your computer on this Wi-Fi.",
                    style = Type.small, textAlign = TextAlign.Center, modifier = Modifier.fillMaxWidth().padding(vertical = 14.dp),
                )
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.Center) {
                    Pill("Allow local network access", primary = true) { allowLan() }
                }
            } else if (desktop && app.scanning) {
                Row(Modifier.fillMaxWidth().padding(vertical = 14.dp), horizontalArrangement = Arrangement.Center, verticalAlignment = Alignment.CenterVertically) {
                    WorkRing("wait", 26.dp)
                    Spacer(Modifier.width(12.dp))
                    Text("Searching the network…", style = Type.small)
                }
            } else if (shown.isEmpty()) {
                Text(
                    if (desktop) "No 1loop found. Open 1loop on your computer, on the same Wi-Fi, and check Settings → Phone remote."
                    else "No server yet. Add one by its address.",
                    style = Type.small, textAlign = TextAlign.Center, modifier = Modifier.fillMaxWidth().padding(vertical = 14.dp),
                )
            }
            if (shown.any { it.paired }) {
                Text(if (desktop) "Long press a computer to forget it." else "Long press a server to forget it.", style = Type.caption, textAlign = TextAlign.Center, modifier = Modifier.fillMaxWidth())
            }
        }
        app.problem?.let { Text(it, style = Type.small.copy(color = Palette.danger), textAlign = TextAlign.Center, modifier = Modifier.padding(top = 14.dp)) }
        Spacer(Modifier.height(24.dp))
        Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            if (desktop) Pill("Search again", icon = Icons.refresh, enabled = !app.scanning) { app.scan() }
            Pill("Add by address", icon = Icons.plus, primary = !desktop) { adding = true }
        }
        if (desktop) {
            Spacer(Modifier.height(10.dp))
            Pill("Scan QR code", primary = true, enabled = !app.busy) {
                scanner.launch(ScanOptions().setDesiredBarcodeFormats(ScanOptions.QR_CODE).setPrompt("Scan the code shown in 1loop on the computer: Settings → Phone remote → Pair a phone").setBeepEnabled(false).setOrientationLocked(false))
            }
            if (app.busy) {
                Spacer(Modifier.height(12.dp))
                WorkRing("wait", 26.dp)
            }
        }
        Spacer(Modifier.height(32.dp))
    }
    if (adding) AddressSheet(onDone = { adding = false }) { app.addManually(it.first, it.second) }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun AddressSheet(onDone: () -> Unit, onAdd: (Pair<String, String>) -> Unit) {
    var address by remember { mutableStateOf("") }
    var fingerprint by remember { mutableStateOf("") }
    val focus = remember { FocusRequester() }
    ModalBottomSheet(onDismissRequest = onDone, containerColor = Palette.surface, sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)) {
        Column(Modifier.padding(horizontal = 24.dp).padding(bottom = 24.dp).imePadding()) {
            Text("Add by address", style = Type.title)
            Spacer(Modifier.height(6.dp))
            Text("HTTPS address and the SHA-256 fingerprint printed in the server console. Leave the fingerprint empty for a publicly trusted certificate, or for a computer: you then check its fingerprint while pairing.", style = Type.small)
            Spacer(Modifier.height(16.dp))
            Field(address, { address = it }, "https://192.168.1.20:47101", KeyboardType.Uri, Modifier.focusRequester(focus))
            Spacer(Modifier.height(12.dp))
            Field(fingerprint, { fingerprint = it }, "Certificate SHA-256 (private server)", KeyboardType.Ascii)
            Spacer(Modifier.height(16.dp))
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
                Pill("Connect", primary = true, enabled = address.isNotBlank()) { onAdd(address to fingerprint); onDone() }
            }
        }
    }
    LaunchedEffect(Unit) { focus.requestFocus() }
}

@Composable
fun Field(value: String, onChange: (String) -> Unit, placeholder: String, keyboard: KeyboardType, modifier: Modifier = Modifier, password: Boolean = false) {
    val interaction = remember { MutableInteractionSource() }
    val focused by interaction.collectIsFocusedAsState()
    val border by animateColorAsState(if (focused) Palette.borderHot.copy(alpha = 1f) else Palette.border, tween(160), label = "border")
    BasicTextField(
        value, onChange, modifier.fillMaxWidth(), singleLine = true, textStyle = Type.body,
        cursorBrush = SolidColor(Palette.amber), keyboardOptions = KeyboardOptions(keyboardType = keyboard), interactionSource = interaction,
        visualTransformation = if (password) PasswordVisualTransformation() else VisualTransformation.None,
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
        Text(if (pair.phone) "On that phone, type this code in 1loop's request and tap Allow, then tap Connect:\n${pair.consoleCode}" else if (pair.consoleCode.isNotEmpty()) "Enter this command in the server console, then tap Connect:\npair ${pair.pairing} ${pair.consoleCode}" else "Type the code 1loop shows on your computer.", style = Type.small.copy(fontSize = 15.sp), textAlign = TextAlign.Center)
        if (pair.server.desktop && pair.server.fingerprint.isNotEmpty()) {
            Spacer(Modifier.height(10.dp))
            Text("The computer must show the same check:\n${fingerprintCheck(pair.server.fingerprint)}", style = Type.small.copy(fontFamily = Type.mono.fontFamily), textAlign = TextAlign.Center)
        }
        Spacer(Modifier.height(32.dp))
        if (pair.consoleCode.isNotEmpty()) {
            if (!pair.phone) CopyChip { "pair ${pair.pairing} ${pair.consoleCode}" }
            Spacer(Modifier.height(16.dp))
            Pill("Connect after approval", primary = true, enabled = !app.busy) { app.confirm(pair, pair.consoleCode) }
        } else BasicTextField(
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
    LaunchedEffect(Unit) { if (pair.consoleCode.isEmpty()) focus.requestFocus() }
    LaunchedEffect(app.problem) { if (app.problem != null) code = "" }
}

// ---------------------------------------------------------------------------
// Chats

@Composable
fun ChatScreen(app: RemoteApp) {
    if (app.server?.localPhone == true) PairingRequestDialog(app) // a device asking to use this phone
    app.handoffAsk?.let { HandoffDialog(it) } // the chat continues with another model
    SharedFilesDialog(app) // files shared to 1loop from another app
    if (app.settingsOpen) {
        MobileSettings(app) { app.settingsOpen = false }
        if (app.connectionsOpen) ConnectionsSheet(app) { app.connectionsOpen = false }
        return
    }
    var panel by remember(app.current, app.selectedProject, app.server?.key) { mutableStateOf(false) }
    BoxWithConstraints(Modifier.fillMaxSize()) {
        if (maxWidth >= 720.dp) {
            Row(Modifier.fillMaxSize()) {
                ChatsPane(app, Modifier.width(300.dp).fillMaxHeight()) {}
                Conversation(app, null, Modifier.weight(1f), panel) { panel = it }
            }
        } else {
            val drawer = rememberDrawerState(DrawerValue.Closed)
            val scope = rememberCoroutineScope()
            BackHandler(drawer.isOpen) { scope.launch { drawer.close() } }
            ModalNavigationDrawer(
                drawerState = drawer,
                gesturesEnabled = drawer.isOpen || !panel,
                drawerContent = {
                    ModalDrawerSheet(drawerContainerColor = Palette.sidebar, drawerShape = RoundedCornerShape(topEnd = 18.dp, bottomEnd = 18.dp)) {
                        ChatsPane(app, Modifier.fillMaxHeight()) { scope.launch { drawer.close() } }
                    }
                },
            ) {
                Conversation(app, onMenu = { scope.launch { drawer.open() } }, Modifier.fillMaxSize(), panel) { panel = it }
            }
        }
    }
    val approval = app.approvals.values.firstOrNull()
    if (approval != null) ApprovalSheet(app, approval)
    val question = app.questions.values.firstOrNull { it.id !in app.questionsAside }
    // A sheet per request: closing one shows the next with its own state, not the last one's Hidden.
    if (approval == null && question != null) key(question.id) { QuestionSheet(app, question) }
    if (app.connectionsOpen) ConnectionsSheet(app) { app.connectionsOpen = false }
    if (app.projectsOpen) ProjectsSheet(app) { app.projectsOpen = false }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun ChatsPane(app: RemoteApp, modifier: Modifier, onPicked: () -> Unit) {
    var deleting by remember(app.server) { mutableStateOf<ChatSummary?>(null) }
    var projectActions by remember(app.server) { mutableStateOf<String?>(null) }
    Column(modifier.background(Palette.sidebar).windowInsetsPadding(WindowInsets.safeDrawing).padding(horizontal = 12.dp)) {
        Image(painterResource(R.drawable.logo), "1loop", modifier = Modifier.padding(start = 6.dp, top = 14.dp, bottom = 14.dp).height(28.dp))
        NavRow("New chat", Icons.plus, selected = app.current == null) { app.open(null); onPicked() }
        Column(Modifier.weight(1f).verticalScroll(rememberScrollState())) {
            Row(Modifier.fillMaxWidth().padding(top = 12.dp), verticalAlignment = Alignment.CenterVertically) {
                Text("Projects", style = Type.small.copy(color = Palette.faint), modifier = Modifier.weight(1f).padding(start = 10.dp))
                if (app.projectManagement) RoundButton(Icons.plus, Color.Transparent, Palette.muted, description = "Add project") { app.projectsOpen = true }
            }
            for (workspace in (app.projects + app.chats.map { it.workspace }).distinct()) {
                val selected = workspace == app.selectedProject
                NavRow(
                    projectName(workspace), Icons.folder, selected = selected,
                    onLongClick = if (app.projectActions) ({ projectActions = workspace }) else null,
                    longClickLabel = "Project actions",
                ) { app.selectProject(workspace) }
                if (selected) {
                    val chats = app.chats.filter { it.workspace == workspace }
                    Column(Modifier.padding(start = 16.dp)) {
                        for (chat in chats) NavRow(
                            chat.title, null, selected = chat.id == app.current, running = chat.running,
                            onLongClick = { deleting = chat },
                        ) { app.open(chat.id); onPicked() }
                        if (chats.isEmpty()) Text("No chats in this project", style = Type.caption, modifier = Modifier.padding(10.dp))
                    }
                }
            }
        }
        val localPhone = app.server?.localPhone == true
        Box(Modifier.fillMaxWidth().height(1.dp).background(Palette.borderSoft))
        Row(
            Modifier.fillMaxWidth().padding(top = 8.dp).clip(RoundedCornerShape(8.dp)).clickable { if (localPhone) app.switchTarget() else app.leave() }
                .padding(horizontal = 10.dp, vertical = 10.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Icon(if (localPhone) Icons.phone else Icons.computer, null, tint = Palette.muted, modifier = Modifier.size(18.dp))
            Column(Modifier.weight(1f).padding(start = 10.dp)) {
                Text(app.server?.name.orEmpty(), style = Type.body.copy(fontSize = 14.sp), maxLines = 1, overflow = TextOverflow.Ellipsis)
                Text(
                    if (!app.connected) "Reconnecting…" else if (localPhone) "Connected" else "Connected · change computer",
                    style = Type.caption.copy(color = if (app.connected) Palette.muted else Palette.amber),
                )
            }
        }
        NavRow("Settings", Icons.document, selected = false) { app.settingsOpen = true; onPicked() }
        NavRow("Switch execution target", Icons.refresh, selected = false) { app.switchTarget() }
        Spacer(Modifier.height(8.dp))
    }
    projectActions?.let { path -> ProjectActionsSheet(app, path) { projectActions = null } }
    deleting?.let { chat ->
        ModalBottomSheet(
            onDismissRequest = { deleting = null },
            containerColor = Palette.surface,
            sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
        ) {
            Column(Modifier.padding(horizontal = 24.dp).padding(bottom = 24.dp)) {
                Text("Delete chat?", style = Type.title)
                Spacer(Modifier.height(12.dp))
                Text("“${chat.title}” will be permanently deleted. Its running tasks and tabs will stop. Project files are kept.", style = Type.body)
                Spacer(Modifier.height(20.dp))
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
                    Pill("Cancel") { deleting = null }
                    Spacer(Modifier.width(10.dp))
                    Pill("Delete", primary = true) {
                        app.deleteChat(chat.id)
                        deleting = null
                    }
                }
            }
        }
    }
}

@Composable
private fun NavRow(text: String, icon: ImageVector?, selected: Boolean, running: Boolean = false, onLongClick: (() -> Unit)? = null, longClickLabel: String = "Delete chat", onClick: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(8.dp)).background(if (selected) Palette.selected else Color.Transparent)
            .combinedClickable(onClick = onClick, onLongClick = onLongClick, onLongClickLabel = longClickLabel).padding(horizontal = 10.dp, vertical = 10.dp),
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
private fun Conversation(app: RemoteApp, onMenu: (() -> Unit)?, modifier: Modifier, panel: Boolean, onPanelChange: (Boolean) -> Unit) {
    val focus = LocalFocusManager.current
    val meta = app.meta
    val running = meta?.running == true
    var tool by remember { mutableStateOf<Item?>(null) }
    val changePanel by rememberUpdatedState(onPanelChange)
    Box(modifier.pointerInput(panel) {
        if (!panel) {
            val edge = 24.dp.toPx()
            val threshold = 48.dp.toPx()
            awaitEachGesture {
                val down = awaitFirstDown(requireUnconsumed = false)
                // Only claim a leftward edge swipe; leave scrolling and text selection alone.
                if (down.position.x < size.width - edge) return@awaitEachGesture
                var distance = 0f
                val drag = awaitHorizontalTouchSlopOrCancellation(down.id) { change, overSlop ->
                    if (overSlop < 0) {
                        distance = -overSlop
                        change.consume()
                    }
                }
                if (drag != null) {
                    val released = horizontalDrag(drag.id) { change ->
                        distance -= change.positionChange().x
                        change.consume()
                    }
                    if (released && distance >= threshold) {
                        focus.clearFocus()
                        changePanel(true)
                    }
                }
            }
        }
    }) {
    Column(Modifier.fillMaxSize().background(Palette.background).windowInsetsPadding(WindowInsets.safeDrawing.only(WindowInsetsSides.Top + WindowInsetsSides.Horizontal))) {
        // Title bar
        Row(Modifier.fillMaxWidth().padding(horizontal = 8.dp, vertical = 6.dp), verticalAlignment = Alignment.CenterVertically) {
            if (onMenu != null) RoundButton(Icons.chats, Color.Transparent, Palette.muted, description = "Chats", onClick = onMenu)
            Column(Modifier.weight(1f).padding(horizontal = 8.dp)) {
                val title = app.chats.firstOrNull { it.id == app.current }?.title ?: "New chat"
                Text(title, style = Type.body.copy(fontWeight = FontWeight.Medium), maxLines = 1, overflow = TextOverflow.Ellipsis)
                Text(
                    if (app.connected) app.chatModel?.name ?: app.server?.name.orEmpty() else "Reconnecting to ${app.server?.name}…",
                    style = Type.caption.copy(color = if (app.connected) Palette.faint else Palette.amber), maxLines = 1, overflow = TextOverflow.Ellipsis,
                )
            }
            Box {
                RoundButton(Icons.panel, Color.Transparent, Palette.muted, description = "Panel: tasks, browser, terminal") {
                    focus.clearFocus(force = true)
                    onPanelChange(true)
                }
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
    val panelContext = app.current ?: "home:" + (app.selectedProject ?: app.workspace)
    var shownContext by remember { mutableStateOf(panelContext) }
    if (panel) shownContext = panelContext
    AnimatedVisibility(
        panel,
        // Opaque and only moved: no transparency to composite at each frame.
        enter = slideInHorizontally(tween(300, easing = CssEase)) { it },
        exit = slideOutHorizontally(tween(240, easing = CssEase)) { it },
    ) {
        PanelScreen(app, shownContext) { onPanelChange(false) }
    }
    }
    tool?.let { ToolSheet(app, it) { tool = null } }
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
    var editing by remember { mutableStateOf<Pair<Int, String>?>(null) } // a user message being edited: its index in the whole chat
    // Following the newest line until the reader scrolls up, like the desktop.
    LaunchedEffect(list) {
        snapshotFlow { list.isScrollInProgress }.collect { scrolling -> if (!scrolling) follow = !list.canScrollForward }
    }
    // Scrolled near the top of a long chat: the page before it, the view kept
    // on the same message. Not while following the end: a chat shown again
    // (back from sleep, reconnected) starts at the top for a moment, and
    // loading the earlier page then would leave the view on an old message.
    LaunchedEffect(list) {
        snapshotFlow { list.firstVisibleItemIndex }.collect { first ->
            if (first <= 2 && app.firstItem > 0 && !follow) {
                val offset = list.firstVisibleItemScrollOffset
                val added = app.loadEarlier()
                if (added > 0) list.scrollToItem(first + added, offset)
            }
        }
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
                    "user" -> UserBubble(item, images = { if (item.images > 0) ItemImages(app, app.firstItem + index, item.images) }) {
                        MessageActions(app, item, running) { editing = app.firstItem + index to item.text }
                    }
                    "assistant" -> Column {
                        Markdown(item.text)
                        // One Copy per finished answer: under its last text, not while it runs.
                        val endsTurn = (index + 1 until items.size).takeWhile { items[it].kind != "user" }.none { items[it].kind == "assistant" }
                        if (endsTurn && !(running && index > lastUser)) {
                            Row(verticalAlignment = Alignment.CenterVertically) {
                                CopyChip { answerText(items, index) }
                                MessageActions(app, item, running) { app.retryMessage(index) }
                            }
                        }
                    }
                    "tool" -> Column {
                        ToolRow(item) { onTool(item) }
                        // What the tool showed the model (a screenshot), as Codex shows it.
                        if (item.images > 0) ItemImages(app, app.firstItem + index, item.images, Modifier.padding(start = 32.dp, top = 2.dp))
                    }
                    "error" -> Column {
                        Text(item.text, style = Type.body.copy(color = Palette.danger))
                        if (!(running && index > lastUser)) MessageActions(app, item, running) { app.retryMessage(index) }
                    }
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
    editing?.let { (index, text) -> EditSheet(text, onSend = { app.editMessage(index, it) }) { editing = null } }
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
private fun UserBubble(item: Item, images: @Composable () -> Unit = {}, actions: @Composable () -> Unit) {
    Column(Modifier.fillMaxWidth(), horizontalAlignment = Alignment.End) {
        images()
        if (item.text.isNotEmpty()) Box(Modifier.widthIn(max = 560.dp).fillMaxWidth(0.85f).padding(top = if (item.images > 0) 6.dp else 0.dp), contentAlignment = Alignment.CenterEnd) {
            Text(
                item.text,
                style = Type.body,
                modifier = Modifier.background(Palette.raised, RoundedCornerShape(18.dp)).padding(horizontal = 14.dp, vertical = 10.dp),
            )
        }
        Row(verticalAlignment = Alignment.CenterVertically) {
            if (item.text.isNotEmpty()) CopyChip { item.text }
            actions()
        }
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
    val project = app.selectedProject
    // Each conversation has its own draft, saved as it is typed, and its undo history.
    val draft = app.draftKey(project)
    var value by remember(draft, app.draftRevision) { mutableStateOf(app.drafts.get(draft).let { TextFieldValue(it, TextRange(it.length)) }) }
    var steps by remember(draft) { mutableIntStateOf(0) } // recomposes the undo and redo buttons
    val text = value.text
    fun edit(next: TextFieldValue) {
        if (next.text != value.text) {
            app.drafts.history(draft).record(value.text)
            app.drafts.save(draft, next.text)
            steps++
        }
        value = next
    }
    fun walk(back: Boolean) {
        val restored = (if (back) app.drafts.undo(draft, value.text) else app.drafts.redo(draft, value.text)) ?: return
        value = TextFieldValue(restored, TextRange(restored.length))
        steps++
    }
    var models by remember { mutableStateOf(false) }
    val picker = rememberLauncherForActivityResult(ActivityResultContracts.OpenMultipleDocuments()) { uris ->
        if (uris.isNotEmpty()) app.attach(uris, project)
    }
    // Another chat may be in another project: keep only the files its message can carry.
    // An open chat is judged by its own workspace, once known; a new one by the project chosen.
    val chatWorkspace = app.current?.let { id -> app.chats.firstOrNull { it.id == id }?.workspace }
    LaunchedEffect(app.current, chatWorkspace, project) {
        when {
            app.current == null -> app.retainAttachments(project ?: app.workspace)
            chatWorkspace != null -> app.retainAttachments(chatWorkspace)
            // Not listed yet: nothing is removed on a guess.
        }
    }
    val interaction = remember { MutableInteractionSource() }
    val focused by interaction.collectIsFocusedAsState()
    val border by animateColorAsState(if (focused) Palette.borderHot else Palette.border, tween(160), label = "card")
    Column(
        Modifier.fillMaxWidth().navigationBarsPadding().imePadding().padding(horizontal = 12.dp).padding(bottom = 10.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        val meta = app.meta
        if (meta != null && (meta.queue.isNotEmpty() || meta.steer.isNotEmpty())) QueueTray(meta)
        // Questions closed without an answer: the run still waits for them.
        app.current?.let { app.questions[it] }?.takeIf { it.id in app.questionsAside }?.let { request ->
            QuestionTray(request) { app.questionsAside.remove(request.id) }
        }
        app.problem?.let { Text(it, style = Type.small.copy(color = Palette.danger), modifier = Modifier.padding(bottom = 6.dp)) }
        // The desktop's context line: the project and the computer.
        Row(Modifier.widthIn(max = 760.dp).fillMaxWidth().padding(bottom = 4.dp), verticalAlignment = Alignment.CenterVertically) {
            ProjectChip(app, project) {
                if (it != project) app.clearAttachments() // sent to the other project's folder
                app.selectProject(it)
            }
            Spacer(Modifier.width(4.dp))
            Icon(Icons.computer, null, tint = Palette.muted, modifier = Modifier.size(15.dp))
            Spacer(Modifier.width(6.dp))
            Text(app.server?.name.orEmpty(), style = Type.small, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
        Column(
            Modifier.widthIn(max = 760.dp).fillMaxWidth().background(Palette.surface, RoundedCornerShape(18.dp))
                .border(1.dp, border, RoundedCornerShape(18.dp)).padding(start = 14.dp, end = 8.dp, top = 6.dp, bottom = 8.dp),
        ) {
            if (app.attachments.isNotEmpty()) AttachmentTray(app)
            BasicTextField(
                value, ::edit,
                Modifier.fillMaxWidth().padding(top = 8.dp, bottom = 6.dp, end = 6.dp).onPreviewKeyEvent { event ->
                    // A hardware keyboard: Ctrl+Z, Ctrl+Y and Ctrl+Shift+Z, as on the desktop.
                    if (event.type != KeyEventType.KeyDown || !event.isCtrlPressed) false
                    else when {
                        event.key == Key.Z && !event.isShiftPressed -> { walk(true); true }
                        event.key == Key.Y || (event.key == Key.Z && event.isShiftPressed) -> { walk(false); true }
                        else -> false
                    }
                },
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
                    if (app.attachmentsEnabled) {
                        RoundButton(Icons.plus, Color.Transparent, Palette.muted, size = 32.dp, iconSize = 18.dp, description = "Attach files") { picker.launch(arrayOf("*/*")) }
                    }
                    ApprovalChip(app)
                    Spacer(Modifier.width(4.dp))
                    ModelChip(app, onClick = { models = true }, modifier = Modifier.weight(1f, fill = false))
                }
                run {
                    val history = steps.let { app.drafts.history(draft) } // read again at each step
                    if (history.undo.isNotEmpty()) RoundButton(Icons.undo, Color.Transparent, Palette.muted, size = 32.dp, iconSize = 17.dp, description = "Undo") { walk(true) }
                    if (history.redo.isNotEmpty()) RoundButton(Icons.redo, Color.Transparent, Palette.muted, size = 32.dp, iconSize = 17.dp, description = "Redo") { walk(false) }
                }
                val meta = app.meta
                ContextRing(meta?.used?.takeIf { app.current != null } ?: 0, meta?.window?.takeIf { app.current != null && it > 0 } ?: app.defaultWindow)
                // Not while a file is still on its way, or one failed and is still listed.
                val blocked = app.attachments.any { it.sending || it.problem != null }
                val ready = !blocked && (text.isNotBlank() || app.attachments.isNotEmpty())
                val send = { mode: String ->
                    if (ready) {
                        app.send(text, mode, project)
                        // Sent: the draft empties, Undo still brings the text back.
                        app.drafts.history(draft).record(text, merge = false)
                        app.drafts.save(draft, "")
                        value = TextFieldValue("")
                        steps++
                    }
                }
                if (running) {
                    if (ready) {
                        RoundButton(Icons.arrowTurn, Palette.raised, Palette.amber, size = 36.dp, iconSize = 18.dp, description = "Steer now") { send("steer") }
                        Spacer(Modifier.width(8.dp))
                        RoundButton(Icons.send, Palette.amber, Palette.ink, size = 36.dp, description = "Queue") { send("queue") }
                        Spacer(Modifier.width(8.dp))
                    }
                    RoundButton(Icons.close, Palette.text, Palette.ink, size = 36.dp, iconSize = 18.dp, description = "Stop") { app.stop() }
                } else {
                    RoundButton(Icons.send, if (ready) Palette.amber else Palette.raised, if (ready) Palette.ink else Palette.faint, size = 36.dp, description = "Send") { send("send") }
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
    val path = chat?.workspace ?: chosen ?: app.selectedProject ?: app.workspace
    Box {
        Row(
            Modifier.clip(RoundedCornerShape(8.dp)).clickable(enabled = chat == null && (app.projects.isNotEmpty() || app.projectManagement)) { open = true }
                .padding(horizontal = 6.dp, vertical = 6.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Icon(Icons.folder, null, tint = Palette.muted, modifier = Modifier.size(16.dp))
            Spacer(Modifier.width(6.dp))
            Text(projectName(path), style = Type.small, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.widthIn(max = 160.dp))
        }
        DropdownMenu(open, { open = false }, containerColor = Palette.popover) {
            for (p in app.projects) DropdownMenuItem(
                text = { Text(projectName(p), style = Type.body) },
                onClick = { onChoose(p); open = false },
                leadingIcon = { Icon(if (p == path) Icons.check else Icons.folder, null, tint = Palette.muted, modifier = Modifier.size(16.dp)) },
            )
            if (app.projectManagement) DropdownMenuItem(text = { Text("Add project…", style = Type.body) }, onClick = { open = false; app.projectsOpen = true })
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
private fun QuestionTray(request: Questions, onOpen: () -> Unit) {
    Row(
        Modifier.widthIn(max = 728.dp).fillMaxWidth().background(Palette.popover, RoundedCornerShape(topStart = 12.dp, topEnd = 12.dp))
            .border(1.dp, Palette.borderHot, RoundedCornerShape(topStart = 12.dp, topEnd = 12.dp)).padding(start = 12.dp, end = 8.dp, top = 6.dp, bottom = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            request.questions.firstOrNull()?.text ?: "1loop has a question", style = Type.body.copy(fontSize = 14.sp, color = Palette.muted),
            maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f),
        )
        Spacer(Modifier.width(8.dp))
        Pill("Answer", primary = true, onClick = onOpen)
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
private fun ToolSheet(app: RemoteApp, item: Item, onDone: () -> Unit) {
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
            if (item.tool != "shell" && item.args.isNotBlank()) CodeWell(toolPreviewText(item.args), "Arguments", wrap = true)
            val at = app.items.indexOf(item)
            if (item.images > 0 && at >= 0) ItemImages(app, app.firstItem + at, item.images)
            val running = item.state != "done"
            when (item.tool) {
                "shell" -> {
                    CodeWell(args.optString("command"), "Command", wrap = true)
                    Section("Output", item.text, if (running) "Waiting for the result…" else "No output")
                }
                "edit_file" -> if (item.diff.isNotEmpty()) CodeWell(item.diff, "Changes", diff = true) else Text("No diff recorded", style = Type.small)
                "read_file" -> Section("Content", item.text, if (running) "Waiting for the result…" else "Empty file")
                "list_directory" -> Section("Entries", item.text, if (running) "Waiting for the result…" else "Empty directory")
                else -> Section("Output", item.text, "No output")
            }
            if (item.failed.isNotEmpty() && item.failed != "Stopped") CodeWell(item.failed, "Error", wrap = true)
        }
    }
}

@Composable
private fun Section(name: String, text: String, empty: String) {
    if (text.isEmpty()) Text(empty, style = Type.small.copy(color = Palette.faint)) else CodeWell(toolPreviewText(text.trimEnd()), name, wrap = true)
}

private fun elapsed(ms: Long) = when {
    ms < 1000 -> "$ms ms"
    ms < 60000 -> String.format(Locale.ROOT, "%.1f s", ms / 1000.0).replace(".0 s", " s")
    else -> "${ms / 60000}m ${(ms / 1000 % 60).toString().padStart(2, '0')}s"
}

// The model's questions: an option per question, or the user's own words
// on the "Other" line; sent once every question has an answer.
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun QuestionSheet(app: RemoteApp, request: Questions) {
    val state = rememberModalBottomSheetState(skipPartiallyExpanded = true, confirmValueChange = { true })
    val title = app.chats.firstOrNull { it.id == request.chat }?.title.orEmpty()
    val draft = app.questionDraft(request) // survives the sheet being set aside
    val answers = draft.answers
    val others = draft.others
    // Closing (swipe, back, outside tap) sets the questions aside, to answer
    // from the composer: the run keeps waiting for them.
    ModalBottomSheet(onDismissRequest = { app.questionsAside.add(request.id) }, sheetState = state, containerColor = Palette.surface) {
        Column(Modifier.verticalScroll(rememberScrollState()).padding(horizontal = 20.dp).padding(bottom = 24.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                WorkRing("approval", 34.dp)
                Spacer(Modifier.width(12.dp))
                Column {
                    Text(if (request.questions.size > 1) "1loop has questions" else "1loop has a question", style = Type.title)
                    Text(title, style = Type.caption.copy(color = Palette.muted), maxLines = 1, overflow = TextOverflow.Ellipsis)
                }
            }
            for (question in request.questions) {
                Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                    if (question.header.isNotEmpty()) Text(question.header.uppercase(), style = Type.caption.copy(color = Palette.faint, fontWeight = FontWeight.SemiBold))
                    Text(question.text, style = Type.body)
                    for (option in question.options) {
                        val selected = answers[question.id] == option.label
                        Row(
                            Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).background(if (selected) Palette.selected else Color.Transparent)
                                .clickable { answers[question.id] = option.label; others.remove(question.id) }.padding(horizontal = 12.dp, vertical = 10.dp),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            Column(Modifier.weight(1f)) {
                                Text(option.label, style = Type.body.copy(fontSize = 14.5.sp))
                                if (option.description.isNotEmpty()) Text(option.description, style = Type.caption.copy(color = Palette.muted))
                            }
                            if (selected) Icon(Icons.check, null, tint = Palette.amber, modifier = Modifier.size(18.dp))
                        }
                    }
                    Field(others[question.id].orEmpty(), { text ->
                        others[question.id] = text
                        if (text.isNotBlank()) answers[question.id] = Questions.OTHER + text.trim() else if (answers[question.id]?.startsWith(Questions.OTHER) == true) answers.remove(question.id)
                    }, "Other: your own answer", KeyboardType.Text)
                }
            }
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
                Pill("Send answers", primary = true, enabled = request.questions.all { answers.containsKey(it.id) }) { app.answer(request, answers.toMap()) }
            }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun ApprovalSheet(app: RemoteApp, approval: Approval) {
    // Only Deny or Allow close it: a hidden sheet left in place would block every touch.
    val state = rememberModalBottomSheetState(skipPartiallyExpanded = true, confirmValueChange = { it != SheetValue.Hidden })
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

// Edit (a user message) or Retry (a reply), and the arrows between versions,
// as on the desktop; only when the computer offers them and no reply runs.
@Composable
private fun MessageActions(app: RemoteApp, item: Item, running: Boolean, onAction: () -> Unit) {
    if (!app.messageVersions || running) return
    val user = item.kind == "user"
    ActionChip(if (user) Icons.pencil else Icons.arrowTurn, if (user) "Edit" else "Retry", onAction)
    item.versions?.takeIf { it.total > 1 }?.let { versions ->
        Row(verticalAlignment = Alignment.CenterVertically) {
            Icon(
                Icons.back, "Previous version", tint = if (versions.current > 1) Palette.muted else Palette.faint,
                modifier = Modifier.size(28.dp).clip(RoundedCornerShape(6.dp))
                    .clickable(enabled = versions.current > 1) { app.switchVersion(versions.anchor, -1) }.padding(6.dp),
            )
            Text("${versions.current}/${versions.total}", style = Type.small)
            Icon(
                Icons.chevronRight, "Next version", tint = if (versions.current < versions.total) Palette.muted else Palette.faint,
                modifier = Modifier.size(28.dp).clip(RoundedCornerShape(6.dp))
                    .clickable(enabled = versions.current < versions.total) { app.switchVersion(versions.anchor, 1) }.padding(6.dp),
            )
        }
    }
}

@Composable
private fun ActionChip(icon: ImageVector, label: String, onClick: () -> Unit) {
    Row(
        Modifier.clip(RoundedCornerShape(6.dp)).clickable(onClick = onClick).padding(horizontal = 8.dp, vertical = 6.dp),
        verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        Icon(icon, null, tint = Palette.muted, modifier = Modifier.size(15.dp))
        Text(label, style = Type.small)
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun EditSheet(original: String, onSend: (String) -> Unit, onDone: () -> Unit) {
    var text by remember { mutableStateOf(original) }
    val focus = remember { FocusRequester() }
    ModalBottomSheet(onDismissRequest = onDone, containerColor = Palette.surface, sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)) {
        Column(Modifier.padding(horizontal = 24.dp).padding(bottom = 24.dp).imePadding()) {
            Text("Edit message", style = Type.title)
            Spacer(Modifier.height(6.dp))
            Text("Sends it as a new version; the earlier one stays reachable with the arrows. Its attachments are kept.", style = Type.small)
            Spacer(Modifier.height(16.dp))
            BasicTextField(
                text, { text = it }, Modifier.fillMaxWidth().focusRequester(focus).background(Palette.code, RoundedCornerShape(10.dp))
                    .border(1.dp, Palette.border, RoundedCornerShape(10.dp)).padding(horizontal = 14.dp, vertical = 12.dp),
                textStyle = Type.body, cursorBrush = SolidColor(Palette.amber), maxLines = 10,
            )
            Spacer(Modifier.height(16.dp))
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
                Pill("Cancel", onClick = onDone)
                Spacer(Modifier.width(10.dp))
                Pill("Send", primary = true, enabled = text.isNotBlank() && text != original) { onSend(text.trim()); onDone() }
            }
        }
    }
    LaunchedEffect(Unit) { focus.requestFocus() }
}

internal fun toolPreviewText(text: String): String = runCatching {
    when {
        text.trimStart().startsWith("{") -> JSONObject(text).toString(2)
        text.trimStart().startsWith("[") -> org.json.JSONArray(text).toString(2)
        else -> text
    }
}.getOrDefault(text)
