package io.oneloop.remote

import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.PowerManager
import android.provider.Settings
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.platform.LocalContext
import androidx.core.net.toUri
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch
import org.json.JSONObject

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
            AutoResumeSection(app)
            if (app.server?.localPhone == true) {
                Text("Connections", style = Type.headline)
                Pill("Accounts and models", onClick = { app.connectionsOpen = true })
                Text("Phone access · Shizuku", style = Type.headline)
                val adb by app.local.adb.collectAsState()
                LocalAdbSection(adb, app::setAdbAllowed, app.local::openShizuku, manualApproval = app.approvalMode == "approval")
                BackgroundExecutionSection(adb.installed)
                LaunchedEffect(Unit) { runCatching { app.refreshSharing() } }
                SharingSection(app)
                Text("Linux", style = Type.headline)
                Text("The Debian the local agent works in: storage, packages, a terminal and a reset.", style = Type.small)
                Pill("Manage Linux", icon = Icons.terminal) { app.linuxOpen = true }
            }
            RunStatusSection()
            Text("Extensions", style = Type.headline)
            Text("Skills and plugins from GitHub, and the MCP servers the agent uses.", style = Type.small)
            Pill("Skills, plugins and MCP servers", enabled = app.connected) { app.extensionsOpen = true }
            GitHubConnectorSection(app)
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
            app.memoryEnabled?.let { enabled ->
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Column(Modifier.weight(1f).padding(end = 12.dp), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                        Text("Memory", style = Type.body.copy(fontWeight = FontWeight.Medium))
                        Text("The agent adds what it learns about you to these preferences, each change shown to you for approval. Off: it is not offered the memory tool.", style = Type.small)
                    }
                    Toggle(enabled, enabled = app.connected) { on ->
                        scope.launch {
                            try { app.setMemory(on); notice = null }
                            catch (cancelled: CancellationException) { throw cancelled }
                            catch (failure: Exception) { notice = failure.message ?: "Could not change memory." }
                        }
                    }
                }
            }
            if (app.defaultPresets) DefaultModelsSection(app)
            ModelCompactionSection(app)
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
        Text(
            if (Build.MANUFACTURER.equals("samsung", ignoreCase = true)) {
                // One UI lists only its own and partner apps there: the rest needs this option.
                "Live Update in the status bar and the Now Bar: on Samsung, turn on Developer options → Live notifications for all apps, and keep 1loop's notifications allowed on the lock screen."
            } else "Live Update in the status bar: in 1loop's notification settings, allow live notifications.",
            style = Type.small,
        )
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

@Composable
private fun AutoResumeSection(app: RemoteApp) {
    val scope = rememberCoroutineScope()
    val saved = app.defaultAutoResume
    var enabled by remember(app.server, saved?.toString()) { mutableStateOf(saved?.optBoolean("enabled") ?: false) }
    val steps = remember(app.server, saved?.toString()) {
        mutableStateListOf<JSONObject>().apply {
            saved?.optJSONArray("steps")?.let { array -> for (i in 0 until array.length()) add(JSONObject(array.getJSONObject(i).toString())) }
        }
    }
    var expanded by remember { mutableStateOf(false) }
    var search by remember { mutableStateOf("") }
    var saving by remember { mutableStateOf(false) }
    var notice by remember { mutableStateOf<String?>(null) }
    Text("Automatic resumption", style = Type.headline)
    Text("When a rate limit interrupts a conversation, 1loop tries these options in order, for every conversation. Stop cancels a wait. Changes apply to the next run.", style = Type.small)
    Row(verticalAlignment = Alignment.CenterVertically) {
        Text("Enabled", style = Type.body, modifier = Modifier.weight(1f))
        Toggle(enabled, enabled = app.connected) { enabled = it }
    }
    steps.forEachIndexed { index, step ->
        val action = step.optString("action")
        val choice = app.models.firstOrNull { it.account == step.optString("connectionId") && it.id == step.optString("model") }
        Column(Modifier.fillMaxWidth().background(Palette.surface, RoundedCornerShape(10.dp)).padding(12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Text("${index + 1}. " + if (action == "wait") "Wait for the limit to reset" else choice?.name ?: step.optString("model"), style = Type.body)
            if (action == "switch") {
                if (choice != null && choice.efforts.isNotEmpty()) {
                    Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        Pill("Default", step.optString("effort").isEmpty()) { steps[index] = JSONObject(step.toString()).put("effort", "") }
                        choice.efforts.forEach { effort ->
                            Pill(effort.label, step.optString("effort") == effort.value) { steps[index] = JSONObject(step.toString()).put("effort", effort.value) }
                        }
                    }
                }
                Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    Pill("Send as is", step.optString("handoff", "as_is") == "as_is") { steps[index] = JSONObject(step.toString()).put("handoff", "as_is") }
                    Pill("Summarize", step.optString("handoff") == "summary") { steps[index] = JSONObject(step.toString()).put("handoff", "summary").put("level", "everything") }
                }
                if (step.optString("handoff") == "summary") {
                    Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        for ((id, label) in listOf("everything" to "Everything", "no_outputs" to "No tool outputs", "messages" to "Messages", "recent" to "Recent")) {
                            Pill(label, step.optString("level") == id) { steps[index] = JSONObject(step.toString()).put("level", id) }
                        }
                    }
                }
            }
            Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Pill("Up", enabled = index > 0) { val previous = steps[index - 1]; steps[index - 1] = step; steps[index] = previous }
                Pill("Down", enabled = index + 1 < steps.size) { val next = steps[index + 1]; steps[index + 1] = step; steps[index] = next }
                Pill("Remove") { steps.removeAt(index) }
            }
        }
    }
    Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
        Pill("Add wait", enabled = steps.size < 16) { steps.add(JSONObject().put("action", "wait")) }
        Box {
            Pill("Add model", enabled = steps.size < 16 && app.models.any { it.id.isNotEmpty() }) { expanded = true }
            DropdownMenu(expanded = expanded, onDismissRequest = { expanded = false }) {
                Field(search, { search = it }, "Search models…", androidx.compose.ui.text.input.KeyboardType.Text, Modifier.width(260.dp))
                app.models.filter { it.id.isNotEmpty() && "${it.name} ${it.detail}".contains(search.trim(), ignoreCase = true) }.take(80).forEach { model ->
                    DropdownMenuItem(text = { Text("${model.name} · ${model.detail}") }, onClick = {
                        steps.add(JSONObject().put("action", "switch").put("connectionId", model.account).put("model", model.id).put("handoff", "as_is"))
                        expanded = false; search = ""
                    })
                }
            }
        }
    }
    Pill(if (saving) "Saving…" else "Save resumption", primary = true, enabled = app.connected && !saving) {
        if (enabled && steps.isEmpty()) { notice = "Add at least one option." } else {
            saving = true
            val policy = JSONObject().put("enabled", enabled).put("steps", org.json.JSONArray(steps.toList()))
            scope.launch {
                try { app.saveAutoResume(policy); notice = "Saved" }
                catch (cancelled: CancellationException) { throw cancelled }
                catch (failure: Exception) { notice = failure.message ?: "Could not save resumption settings." }
                finally { saving = false }
            }
        }
    }
    notice?.let { Text(it, style = Type.small) }
}

@Composable
private fun ModelCompactionSection(app: RemoteApp) {
    val scope = rememberCoroutineScope()
    var selected by remember(app.server) { mutableStateOf("") }
    var expanded by remember { mutableStateOf(false) }
    var search by remember { mutableStateOf("") }
    var notice by remember { mutableStateOf<String?>(null) }
    var saving by remember { mutableStateOf(false) }
    val model = app.models.firstOrNull { it.label == selected } ?: app.chatModel
    Text("Model context", style = Type.headline)
    Text("When 1loop compacts a conversation, for each model: choose a model, then its target in tokens.", style = Type.small)
    Box {
        Pill(model?.name ?: "Choose a model", enabled = app.models.isNotEmpty()) { expanded = true }
        DropdownMenu(expanded = expanded, onDismissRequest = { expanded = false }) {
            Field(search, { search = it }, "Search models…", KeyboardType.Text, Modifier.width(260.dp))
            app.models.filter { "${it.name} ${it.detail}".contains(search.trim(), ignoreCase = true) }.take(80).forEach { choice ->
                DropdownMenuItem(text = { Text("${choice.name} · ${choice.detail}") }, onClick = {
                    selected = choice.label
                    expanded = false
                    search = ""
                    notice = null
                })
            }
        }
    }
    if (model != null && model.compactSupported) {
        var value by remember(model.label, model.compactAt) { mutableStateOf(model.compactAt.takeIf { it > 0 }?.toString().orEmpty()) }
        val placeholder = if (model.compactDefault > 0) "Default · " + "%,d".format(java.util.Locale.US, model.compactDefault) else "Default · the model's window"
        Field(value, { value = it; notice = null }, placeholder, KeyboardType.Number)
        Text(model.compactHelp.ifEmpty { "Optional, for this model only. Leave it empty for the default." }, style = Type.small)
        Pill(if (saving) "Saving…" else "Save context target", enabled = app.connected && !saving) {
            val limit = value.trim().toIntOrNull()
            if (value.isNotBlank() && (limit == null || limit !in 1..1000000)) {
                notice = "Enter 1–1,000,000 tokens or leave the field empty."
            } else {
                saving = true
                scope.launch {
                    try { app.setModelCompaction(model.label, limit ?: 0); notice = "Saved" }
                    catch (cancelled: CancellationException) { throw cancelled }
                    catch (failure: Exception) { notice = failure.message ?: "Could not save the context target." }
                    finally { saving = false }
                }
            }
        }
    } else if (model != null) {
        Text("This connection manages its own context, or the connected target needs an update.", style = Type.small)
    }
    notice?.let { Text(it, style = Type.small) }
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
