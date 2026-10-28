package io.oneloop.remote

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch
import org.json.JSONObject
import java.util.Locale

// Settings → Linux: the Debian the local agent runs in, which the user does
// not otherwise see. What it takes, a cleanup, its packages, a root
// terminal and a reset. AndroidLocalRuntime does the work.

private fun bytes(value: Long): String = when {
    value >= 1L shl 30 -> String.format(Locale.US, "%.1f GB", value / (1L shl 30).toDouble())
    value >= 1L shl 20 -> String.format(Locale.US, "%.0f MB", value / (1L shl 20).toDouble())
    value >= 1L shl 10 -> String.format(Locale.US, "%.0f KB", value / (1L shl 10).toDouble())
    else -> "$value B"
}

private class LinuxAsk(val title: String, val text: String, val verb: String, val run: suspend () -> String)

@Composable
fun LinuxScreen(app: RemoteApp, onBack: () -> Unit) {
    val local = app.local
    val scope = rememberCoroutineScope()
    var terminal by remember { mutableStateOf<String?>(null) } // the open Debian terminal's tab
    val home = "home:" + (app.selectedProject ?: app.workspace)
    terminal?.let { key ->
        LinuxTerminal(app, home, key) {
            scope.launch { closeLocalTab(app, home, key) }
            terminal = null
        }
        return
    }
    BackHandler(onBack = onBack)
    var usage by remember { mutableStateOf<LinuxUsage?>(null) }
    var packages by remember { mutableStateOf<List<LinuxPackage>?>(null) }
    var busy by app::linuxBusy
    var notice by app::linuxNotice
    var ask by remember { mutableStateOf<LinuxAsk?>(null) }
    var resetAsked by remember { mutableStateOf(false) }
    var query by remember { mutableStateOf("") }
    var everything by remember { mutableStateOf(false) }
    val installed = local.debianInstalled
    LaunchedEffect(installed, app.linuxRevision) {
        if (!installed || app.linuxBusy != null) return@LaunchedEffect // read again once the change is done
        usage = null
        packages = null
        usage = runCatching { local.linuxUsage() }.onFailure { if (it is CancellationException) throw it }.getOrNull()
        packages = try { local.linuxPackages() } catch (e: CancellationException) { throw e } catch (e: Exception) {
            if (app.linuxBusy == null) notice = (e.message ?: "The packages could not be listed.") to true
            emptyList()
        }
    }
    fun work(label: String, action: suspend () -> String) = app.linuxWork(label, action)
    fun prepare(label: String, plan: suspend () -> LinuxAsk) {
        busy = label
        notice = null
        scope.launch {
            try { ask = plan() }
            catch (e: CancellationException) { throw e }
            catch (e: Exception) { notice = (e.message ?: "$label failed.") to true }
            finally { busy = null }
        }
    }
    Column(Modifier.fillMaxSize().background(Palette.background).windowInsetsPadding(WindowInsets.safeDrawing)) {
        Row(Modifier.padding(12.dp), verticalAlignment = Alignment.CenterVertically) {
            RoundButton(Icons.back, Color.Transparent, Palette.muted, description = "Back", onClick = onBack)
            Text("Linux", style = Type.title, modifier = Modifier.padding(start = 12.dp))
        }
        // What is running and how it went, above the list: seen wherever it is scrolled.
        busy?.let {
            Row(Modifier.padding(horizontal = 20.dp, vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                WorkRing("wait", 22.dp)
                Text(it, style = Type.small, modifier = Modifier.padding(start = 10.dp))
            }
        }
        notice?.let { (text, failed) ->
            Text(text, style = Type.small.copy(color = if (failed) Palette.danger else Palette.added), modifier = Modifier.padding(horizontal = 20.dp, vertical = 4.dp))
        }
        val shown = packages.orEmpty().filter { (everything || it.manual) && (query.isBlank() || it.name.contains(query.trim(), ignoreCase = true)) }
        LazyColumn(Modifier.weight(1f), contentPadding = PaddingValues(20.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
            item { Text("The Debian the local agent works in: its tools, packages and your projects. 1loop installs it and keeps it in this app's storage.", style = Type.small) }
            if (!installed) {
                item {
                    Text(
                        "Debian is not installed. Prepare it from the Local phone setup." +
                            if (local.projectsKept) " Your kept projects go back into it." else "",
                        style = Type.body,
                    )
                }
                return@LazyColumn
            }
            item {
                Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                    Text("Storage", style = Type.headline)
                    LinuxCard {
                        val used = usage
                        if (used == null) WorkRing("wait", 26.dp) else {
                            Text("Debian uses ${bytes(used.total)}", style = Type.body.copy(fontWeight = FontWeight.Medium))
                            val packageBytes = packages?.sumOf { it.size }
                            UsageLine("Installed packages", packageBytes?.let(::bytes) ?: "…")
                            UsageLine("Your projects", bytes(used.projects))
                            UsageLine("Downloaded package files", bytes(used.packageFiles))
                            UsageLine("Local agent: chats, settings, versions", bytes(used.agentData))
                            Text("${bytes(used.phoneFree)} free of ${bytes(used.phoneTotal)} on this phone.", style = Type.caption)
                        }
                    }
                    Pill("Clean up", enabled = busy == null && usage != null) {
                        prepare("Checking what can go…") {
                            val plan = local.linuxCleanup()
                            val lines = buildList {
                                add("Downloaded package files: ${bytes(plan.packageFiles)}.")
                                if (plan.oldVersions > 0) add("Older versions of the local agent: ${bytes(plan.oldVersions)}.")
                                if (plan.packages.isNotEmpty()) add("Packages nothing needs any more (${plan.packages.size}): ${plan.packages.joinToString(", ")}.")
                            }
                            LinuxAsk("Clean up Debian?", lines.joinToString("\n\n") + "\n\nYour projects and installed tools stay.", "Clean up") {
                                val before = local.linuxUsage().total
                                local.cleanLinux()
                                "Cleaned up: ${bytes(maxOf(0L, before - local.linuxUsage().total))} freed."
                            }
                        }
                    }
                }
            }
            item {
                Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                    Text("Terminal", style = Type.headline)
                    Text("A root shell in Debian, outside the projects' sandbox: install, remove or inspect anything. It is yours only: the agent cannot use it.", style = Type.small)
                    val running = app.server?.localPhone == true && app.connected
                    Pill("Open a terminal", icon = Icons.terminal, enabled = running && busy == null) {
                        busy = "Opening the terminal…"
                        notice = null
                        scope.launch {
                            try {
                                val target = app.server ?: error("The local agent is not running.")
                                val reply = kotlinx.coroutines.withContext(kotlinx.coroutines.Dispatchers.IO) {
                                    Api(target).post(app.panelPath(home), JSONObject().put("action", "open").put("kind", "terminal").put("root", true), 20_000)
                                }
                                terminal = reply.optString("key").ifEmpty { error("Update the local agent to open a Debian terminal.") }
                            } catch (e: CancellationException) { throw e } catch (e: Exception) {
                                notice = (e.message ?: "The terminal did not open.") to true
                            } finally { busy = null }
                        }
                    }
                    if (!running) Text("Start the local agent to open a terminal.", style = Type.caption)
                }
            }
            item {
                Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                    val list = packages
                    Text("Packages", style = Type.headline)
                    Text(
                        if (list == null) "Listing the packages…"
                        else "${list.size} installed, ${list.count { it.manual }} of them chosen by you or 1loop; the others came with them. Marked Needed: Debian or 1loop needs them.",
                        style = Type.small,
                    )
                    Input(query, { query = it }, "Search the packages", KeyboardType.Ascii, ImeAction.Search, Modifier.fillMaxWidth()) {}
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Text("Show every package", style = Type.body, modifier = Modifier.weight(1f))
                        Toggle(everything, enabled = true) { everything = it }
                    }
                }
            }
            if (packages == null) item { WorkRing("wait", 26.dp) }
            items(shown, key = { it.name }) { item ->
                PackageRow(item, enabled = busy == null) {
                    prepare("Checking what ${item.name} takes with it…") {
                        val removed = local.linuxRemoval(listOf(item.name))
                        val freed = packages.orEmpty().filter { it.name in removed }.sumOf { it.size }
                        LinuxAsk(
                            "Remove ${item.name}?",
                            "These packages will be removed, with their settings (${bytes(freed)}):\n\n${removed.joinToString(", ")}",
                            "Remove",
                        ) {
                            local.removeLinuxPackages(listOf(item.name))
                            "Removed ${removed.joinToString(", ")}."
                        }
                    }
                }
            }
            item {
                Column(verticalArrangement = Arrangement.spacedBy(10.dp), modifier = Modifier.padding(top = 8.dp)) {
                    Text("Reset", style = Type.headline)
                    Text("Erase this Debian and install it again from the Local phone setup, when it is broken or too big.", style = Type.small)
                    Pill("Reset Linux…", enabled = busy == null) { resetAsked = true }
                }
            }
        }
    }
    ask?.let { shown ->
        AlertDialog(
            onDismissRequest = { ask = null },
            containerColor = Palette.surface,
            title = { Text(shown.title, style = Type.title) },
            text = { Text(shown.text, style = Type.small, modifier = Modifier.heightIn(max = 360.dp).verticalScroll(rememberScrollState())) },
            confirmButton = { Pill(shown.verb, primary = true) { ask = null; work(shown.verb + "…", shown.run) } },
            dismissButton = { Pill("Cancel") { ask = null } },
        )
    }
    if (resetAsked) {
        var keep by remember { mutableStateOf(true) }
        AlertDialog(
            onDismissRequest = { resetAsked = false },
            containerColor = Palette.surface,
            title = { Text("Reset Linux?", style = Type.title) },
            text = {
                Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    Text("This erases Debian and everything installed in it, the local agent's chats and settings, and Claude Code's sign-in on this phone. The local agent stops. It cannot be undone.", style = Type.small)
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Column(Modifier.weight(1f).padding(end = 12.dp)) {
                            Text("Keep my projects", style = Type.body)
                            Text(if (keep) "They go back into the new Debian." else "Your projects are erased too.", style = Type.caption)
                        }
                        Toggle(keep, enabled = true) { keep = it }
                    }
                }
            },
            confirmButton = {
                Pill("Reset", primary = true) {
                    resetAsked = false
                    work("Erasing Debian…") {
                        local.resetLinux(keep)
                        if (keep) "Debian was erased; your projects are kept. Prepare Debian again from the Local phone setup."
                        else "Debian was erased. Prepare it again from the Local phone setup."
                    }
                }
            },
            dismissButton = { Pill("Cancel") { resetAsked = false } },
        )
    }
}

@Composable
private fun LinuxCard(content: @Composable ColumnScope.() -> Unit) {
    Column(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(14.dp)).background(Palette.surface)
            .border(1.dp, Palette.borderSoft, RoundedCornerShape(14.dp)).padding(horizontal = 16.dp, vertical = 14.dp),
        verticalArrangement = Arrangement.spacedBy(6.dp), content = content,
    )
}

@Composable
private fun UsageLine(label: String, value: String) {
    Row {
        Text(label, style = Type.small, modifier = Modifier.weight(1f))
        Text(value, style = Type.small.copy(color = Palette.text))
    }
}

@Composable
private fun PackageRow(item: LinuxPackage, enabled: Boolean, onRemove: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(12.dp)).background(Palette.surface)
            .border(1.dp, Palette.borderSoft, RoundedCornerShape(12.dp)).padding(horizontal = 14.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f).padding(end = 10.dp)) {
            Text(item.name, style = Type.body.copy(fontWeight = FontWeight.Medium), maxLines = 1, overflow = TextOverflow.Ellipsis)
            Text("${bytes(item.size)} · ${item.version}", style = Type.caption, maxLines = 1, overflow = TextOverflow.Ellipsis)
            if (item.summary.isNotEmpty()) Text(item.summary, style = Type.caption.copy(color = Palette.faint), maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
        if (item.protected) Text("Needed", style = Type.caption)
        else Box(
            Modifier.clip(RoundedCornerShape(50)).border(1.dp, Palette.border, RoundedCornerShape(50))
                .clickable(enabled = enabled, onClick = onRemove).padding(horizontal = 12.dp, vertical = 6.dp),
        ) { Text("Remove", style = Type.small.copy(color = if (enabled) Palette.danger else Palette.faint)) }
    }
}

/** The Debian terminal, full screen; leaving closes it. */
@Composable
private fun LinuxTerminal(app: RemoteApp, home: String, key: String, onClose: () -> Unit) {
    BackHandler(onBack = onClose)
    Column(Modifier.fillMaxSize().background(Palette.background).windowInsetsPadding(WindowInsets.safeDrawing).imePadding()) {
        Row(Modifier.padding(12.dp), verticalAlignment = Alignment.CenterVertically) {
            RoundButton(Icons.back, Color.Transparent, Palette.muted, description = "Close the terminal", onClick = onClose)
            Column(Modifier.padding(start = 12.dp)) {
                Text("Debian terminal", style = Type.title)
                Text("root · closed when you leave", style = Type.caption)
            }
        }
        TerminalView(app, home, PanelTab(key, "terminal", "Debian", "", 0))
    }
}
