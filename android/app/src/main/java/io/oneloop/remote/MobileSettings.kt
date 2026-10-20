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
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
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
            }
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
            HooksSection(app)
        }
    }
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
    Text(
        "For long Local phone tasks, open 1loop's battery settings and choose Unrestricted, " +
            "or exclude 1loop from battery optimization. This can increase battery use. " +
            "Android and the phone manufacturer may still stop background work.",
        style = Type.small,
    )
    Pill("1loop battery settings") {
        open(
            Intent(
                Settings.ACTION_APPLICATION_DETAILS_SETTINGS,
                Uri.parse("package:${context.packageName}"),
            ),
        )
    }
    if (!exempt) {
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
