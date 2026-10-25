package io.oneloop.remote

import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.PowerManager
import android.provider.Settings
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.core.net.toUri
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch

@Composable
fun MobileSettings(app: RemoteApp, onBack: () -> Unit) {
    BackHandler(onBack = onBack)
    val scope = rememberCoroutineScope()
    var preferences by remember(app.server) { mutableStateOf(app.userPreferences) }
    var saving by remember { mutableStateOf(false) }
    var notice by remember { mutableStateOf<String?>(null) }
    Column(Modifier.fillMaxSize().background(Palette.background).windowInsetsPadding(WindowInsets.safeDrawing)) {
        Row(Modifier.padding(12.dp), verticalAlignment = Alignment.CenterVertically) {
            RoundButton(Icons.back, Color.Transparent, Palette.muted, description = "Back", onClick = onBack)
            Text("Settings", style = Type.title, modifier = Modifier.padding(start = 12.dp))
        }
        Column(Modifier.weight(1f).verticalScroll(rememberScrollState()).padding(20.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
            Text("Execution target", style = Type.headline)
            Text(app.server?.name.orEmpty(), style = Type.body)
            Pill("Switch execution target") { app.switchTarget() }
            Text("Approvals", style = Type.headline)
            ApprovalChip(app)
            if (app.server?.localPhone == true) {
                Text("Connections", style = Type.headline)
                Pill("Accounts and models", onClick = { app.connectionsOpen = true })
                Text("Phone access · Shizuku", style = Type.headline)
                val adb by app.local.adb.collectAsState()
                LocalAdbSection(adb, app::setAdbAllowed, app.local::openShizuku, manualApproval = app.approvalMode == "approval")
                BackgroundExecutionSection(adb.installed)
                LaunchedEffect(Unit) { runCatching { app.refreshSharing() } }
                SharingSection(app)
            }
            RunStatusSection()
            Text("Personalization", style = Type.headline)
            Text("Your preferences are added to 1loop's base instructions. They never replace its system prompt or tool rules. They apply to subsequent turns on this execution target.", style = Type.small)
            if (app.preferencesEditable) {
                BasicTextField(
                    preferences, { if (it.length <= 65536) { preferences = it; notice = null } },
                    modifier = Modifier.fillMaxWidth().heightIn(min = 180.dp).background(Palette.surface, RoundedCornerShape(12.dp))
                        .border(1.dp, Palette.borderSoft, RoundedCornerShape(12.dp)).padding(14.dp),
                    textStyle = Type.body, minLines = 8,
                    decorationBox = { inner ->
                        Box { if (preferences.isEmpty()) Text("Language, tone, coding conventions, level of detail…", style = Type.small); inner() }
                    },
                )
                Pill(if (saving) "Saving…" else "Save preferences", primary = true, enabled = app.connected && !saving) {
                    saving = true
                    scope.launch {
                        try { app.savePreferences(preferences); notice = "Saved" }
                        catch (cancelled: CancellationException) { throw cancelled }
                        catch (failure: Exception) { notice = failure.message ?: "Could not save preferences." }
                        finally { saving = false }
                    }
                }
            } else Text("Update the connected desktop or server to edit preferences here.", style = Type.small)
            notice?.let { Text(it, style = Type.small) }
            UpdatesSection(app)
            BrowserDataSection(app)
            HooksSection(app)
        }
    }
}

// While a model runs and 1loop is not on screen: the floating badge (it needs
// "Display over other apps"), the Live Update in the status bar, or nothing.
@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun RunStatusSection() {
    val context = LocalContext.current
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    var style by remember { mutableStateOf(AgentStatus.style(context)) }
    var overlay by remember { mutableStateOf(AgentStatus.canDrawOverlays(context)) }
    DisposableEffect(lifecycle) {
        // Back from the "Display over other apps" page.
        val observer = LifecycleEventObserver { _, event -> if (event == Lifecycle.Event.ON_RESUME) overlay = AgentStatus.canDrawOverlays(context) }
        lifecycle.addObserver(observer)
        onDispose { lifecycle.removeObserver(observer) }
    }
    Text("While a model runs", style = Type.headline)
    Text("Shows what the agent does over your other apps, with a button to stop it. It stays small and never covers the screen.", style = Type.small)
    // Wraps: the three choices are wider than a phone.
    FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        for (option in StatusStyle.entries) {
            Pill(option.title, primary = style == option) { style = option; AgentStatus.setStyle(context, option) }
        }
    }
    // Always there: to allow it, or to take it back.
    Text(
        if (overlay) "Display over other apps: allowed." else "Display over other apps: not allowed.",
        style = Type.body,
    )
    if (style == StatusStyle.Badge && !overlay) {
        Text("The badge needs it. Until you allow it, the status shows in the status bar instead.", style = Type.small)
    }
    Pill(if (overlay) "Display over other apps settings" else "Allow display over other apps", primary = style == StatusStyle.Badge && !overlay) {
        runCatching {
            context.startActivity(Intent(Settings.ACTION_MANAGE_OVERLAY_PERMISSION, "package:${context.packageName}".toUri()))
        }
    }
    // The Live Update (status bar chip, Samsung's Now Bar): Android 16 promotes it, but the
    // user decides per app, and Samsung shows it in the Now Bar only once "Live notifications"
    // is on for 1loop, in its notification settings, where this page leads on One UI.
    if (Build.VERSION.SDK_INT >= 36 && style != StatusStyle.Off) {
        Text("Live Update in the status bar and the Now Bar: in 1loop's notification settings, allow live notifications.", style = Type.small)
        Pill("Live notifications settings") {
            runCatching {
                context.startActivity(
                    Intent(Settings.ACTION_APP_NOTIFICATION_PROMOTION_SETTINGS).putExtra(Settings.EXTRA_APP_PACKAGE, context.packageName),
                )
            }.recoverCatching {
                context.startActivity(Intent(Settings.ACTION_APP_NOTIFICATION_SETTINGS).putExtra(Settings.EXTRA_APP_PACKAGE, context.packageName))
            }
        }
    }
}

// The agent's browser keeps its logins between sessions: this erases them,
// on this phone's WebView or on the computer the agent runs on.
@Composable
private fun BrowserDataSection(app: RemoteApp) {
    val scope = rememberCoroutineScope()
    var asking by remember { mutableStateOf(false) }
    var busy by remember { mutableStateOf(false) }
    var status by remember(app.server) { mutableStateOf<String?>(null) }
    Text("Browser", style = Type.headline)
    Text(
        if (app.server?.localPhone == true) "The agent's browser on this phone keeps its cookies and logins, cache and site data. Clear erases all of it."
        else "The agent's browser on ${app.server?.name ?: "the computer"} keeps its cookies and logins, cache, site data and history. Clear erases all of it.",
        style = Type.small,
    )
    Pill(if (busy) "Clearing…" else "Clear browsing data", enabled = app.connected && !busy) { asking = true }
    status?.let { Text(it, style = Type.small.copy(color = if (it.startsWith("Cleared")) Palette.added else Palette.danger)) }
    if (asking) AlertDialog(
        onDismissRequest = { asking = false },
        containerColor = Palette.surface,
        title = { Text("Clear browsing data?", style = Type.title) },
        text = { Text("Cookies and logins, cache and site data of the agent's browser will be erased. Sites will ask to log in again.", style = Type.small) },
        confirmButton = {
            Pill("Clear", primary = true) {
                asking = false
                busy = true
                scope.launch {
                    try { app.clearBrowser(); status = "Cleared." }
                    catch (cancelled: CancellationException) { throw cancelled }
                    catch (failure: Exception) { status = failure.message ?: "Could not clear the browser." }
                    finally { busy = false }
                }
            }
        },
        dismissButton = { Pill("Cancel") { asking = false } },
    )
}

// The hooks of the machine the agent runs on (computer, server or this
// phone's engine): commands around its work, in Claude Code's format.
@Composable
private fun HooksSection(app: RemoteApp) {
    val scope = rememberCoroutineScope()
    var hooks by remember(app.server) { mutableStateOf<String?>(null) }
    var status by remember(app.server) { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf(false) }
    Text("Hooks", style = Type.headline)
    Text("Commands run before or after the agent's tools (PreToolUse, PostToolUse), when you send a message (UserPromptSubmit) or when it stops (Stop). Exit code 2 from a PreToolUse blocks the tool.", style = Type.small)
    val text = hooks
    if (text == null) {
        Pill(if (busy) "Loading…" else "Edit hooks", enabled = app.connected && !busy) {
            busy = true
            scope.launch {
                try { hooks = app.loadHooks(); status = null }
                catch (cancelled: CancellationException) { throw cancelled }
                catch (failure: Exception) { status = failure.message ?: "Could not load the hooks." }
                finally { busy = false }
            }
        }
    } else {
        BasicTextField(
            text, { hooks = it; status = null },
            modifier = Modifier.fillMaxWidth().heightIn(min = 220.dp).background(Palette.code, RoundedCornerShape(12.dp))
                .border(1.dp, Palette.borderSoft, RoundedCornerShape(12.dp)).padding(14.dp),
            textStyle = Type.mono, minLines = 10,
        )
        Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            Pill(if (busy) "Saving…" else "Save hooks", primary = true, enabled = app.connected && !busy) {
                busy = true
                scope.launch {
                    try { app.saveHooks(text); status = "Saved" }
                    catch (cancelled: CancellationException) { throw cancelled }
                    catch (failure: Exception) { status = failure.message ?: "Could not save the hooks." }
                    finally { busy = false }
                }
            }
            Pill("Close") { hooks = null; status = null }
        }
    }
    status?.let { Text(it, style = Type.small.copy(color = if (it == "Saved") Palette.added else Palette.danger)) }
}

@Composable
private fun BackgroundExecutionSection(shizukuInstalled: Boolean) {
    val context = LocalContext.current
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    val power = remember(context) { context.getSystemService(PowerManager::class.java) }
    var exempt by remember {
        mutableStateOf(power.isIgnoringBatteryOptimizations(context.packageName))
    }
    var powerSaving by remember { mutableStateOf(power.isPowerSaveMode) }
    var problem by remember { mutableStateOf<String?>(null) }

    DisposableEffect(lifecycle, power) {
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_RESUME) {
                exempt = power.isIgnoringBatteryOptimizations(context.packageName)
                powerSaving = power.isPowerSaveMode
            }
        }
        lifecycle.addObserver(observer)
        onDispose {
            lifecycle.removeObserver(observer)
        }
    }

    fun open(intent: Intent) {
        try {
            context.startActivity(intent)
            problem = null
        } catch (_: android.content.ActivityNotFoundException) {
            problem = "This phone does not expose this settings page. " +
                "Open Settings → Apps → 1loop → Battery manually."
        }
    }

    Text("Background execution", style = Type.headline)
    Text(
        if (exempt) {
            "1loop is exempt from Android battery optimization."
        } else {
            "Android battery optimization is enabled for 1loop."
        },
        style = Type.body,
    )
    if (!exempt) Text(
        "For long Local phone tasks, open 1loop's battery settings and choose Unrestricted, " +
            "or exclude 1loop from battery optimization. This can increase battery use. " +
            "Android and the phone manufacturer may still stop background work.",
        style = Type.small,
    )
    // Both buttons lead to granting the exemption: once it is granted, neither shows.
    if (!exempt) {
        Pill("1loop battery settings") {
            open(
                Intent(
                    Settings.ACTION_APPLICATION_DETAILS_SETTINGS,
                    Uri.parse("package:${context.packageName}"),
                ),
            )
        }
        Pill("Battery optimization list") {
            open(Intent(Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS))
        }
    }

    if (Build.MANUFACTURER.equals("samsung", ignoreCase = true)) {
        GuideStep(
            "1",
            "Allow background activity",
            "Settings → Apps → 1loop → Battery → Unrestricted. Labels can vary by One UI version.",
        )
        GuideStep(
            "2",
            "Prevent automatic sleeping",
            "Settings → Battery and device care → Battery → Background usage limits → Never sleeping apps. " +
                "Add 1loop if available and remove it from Sleeping or Deep sleeping apps.",
        )
    }

    if (shizukuInstalled) {
        Text(
            "If Phone Use disconnects in the background, also allow Shizuku to run in the background " +
                "in its battery settings. Shizuku still needs to be started again after a phone reboot.",
            style = Type.small,
        )
        Pill("Shizuku battery settings") {
            open(
                Intent(
                    Settings.ACTION_APPLICATION_DETAILS_SETTINGS,
                    Uri.parse("package:moe.shizuku.privileged.api"),
                ),
            )
        }
    }

    if (powerSaving) {
        Text(
            "System power saving is on. It can limit background work even with app exceptions. " +
                "1loop does not change this phone-wide setting.",
            style = Type.small,
        )
    }
    problem?.let {
        Text(it, style = Type.small.copy(color = Palette.danger))
    }
}

// Settings → Updates: this app's version and the release page's, the update
// of the app, and of the server it is connected to (Updates.kt).
@Composable
private fun UpdatesSection(app: RemoteApp) {
    val scope = rememberCoroutineScope()
    var serverStatus by remember(app.server) { mutableStateOf<String?>(null) }
    var serverBusy by remember { mutableStateOf(false) }
    Text("Updates", style = Type.headline)
    UpdateActions(app)
    app.serverUpdate?.let { status ->
        val name = app.server?.name ?: "The server"
        Text("$name runs ${status.optString("current")}; ${status.optString("latest")} is out.", style = Type.body)
        if (status.optBoolean("canUpdate")) {
            Pill(if (serverBusy) "Updating the server…" else "Update the server", primary = true, enabled = !serverBusy && app.connected) {
                serverBusy = true
                scope.launch {
                    serverStatus = try { "The server restarts with " + app.updateServer() + "; the app reconnects by itself." }
                    catch (cancelled: CancellationException) { throw cancelled }
                    catch (failure: Exception) { failure.message }
                    finally { serverBusy = false }
                }
            }
        } else Text(status.optString("problem"), style = Type.small)
    }
    serverStatus?.let { Text(it, style = Type.small) }
}

/** This app's version, the release page's, and the buttons to check and update: Settings, the update card and dialog. */
@Composable
fun UpdateActions(app: RemoteApp) {
    val scope = rememberCoroutineScope()
    val updates = app.updates
    var checking by remember { mutableStateOf(false) }
    val latest = updates.latest
    Text(
        when {
            checking -> "1loop ${updates.currentVersion}. Checking the release page…"
            updates.available && latest != null -> "1loop ${updates.currentVersion}. Version ${latest.version} is available."
            latest != null -> "1loop ${updates.currentVersion} is up to date."
            else -> "1loop ${updates.currentVersion}."
        },
        style = Type.body,
    )
    if (updates.available && latest != null && latest.notes.isNotBlank()) Markdown(latest.notes)
    Text("1loop checks its release page every six hours and notifies you of a new version. Updating keeps your chats and settings: Android's installer replaces this version after you confirm.", style = Type.small)
    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        Pill(if (checking) "Checking…" else "Check now", enabled = !checking && !updates.busy) {
            checking = true
            scope.launch { try { updates.check() } finally { checking = false } }
        }
        if (updates.available && latest != null) {
            Pill(if (updates.busy) "Downloading…" else "Update to ${latest.version}", primary = true, enabled = !updates.busy) {
                scope.launch { updates.install(latest) }
            }
        }
    }
    updates.problem?.let { Text(it, style = Type.small.copy(color = Palette.danger)) }
}

/** The update notification was tapped: the update, whatever the screen. */
@Composable
fun UpdateDialog(app: RemoteApp, onDismiss: () -> Unit) {
    LaunchedEffect(Unit) { app.updates.check() }
    AlertDialog(
        onDismissRequest = onDismiss,
        containerColor = Palette.surface,
        title = { Text("Update 1loop", style = Type.title) },
        text = { Column(Modifier.verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(10.dp)) { UpdateActions(app) } },
        confirmButton = { Pill("Close") { onDismiss() } },
    )
}
