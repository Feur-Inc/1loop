package io.oneloop.remote

import android.graphics.BitmapFactory
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.Image
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch
import org.json.JSONObject

// The Files tab (the desktop's file_browser.go): the project's folders and
// files where the agent works, a preview of each file, and actions —
// download to the phone, attach to the next message, add files from the
// phone. Files shared to 1loop from another app land here too.

data class ProjectFile(val name: String, val path: String, val dir: Boolean, val size: Long)

fun byteSize(n: Long): String = when {
    n >= 1L shl 30 -> "%.1f GB".format(n / (1L shl 30).toDouble())
    n >= 1L shl 20 -> "%.1f MB".format(n / (1L shl 20).toDouble())
    n >= 1L shl 10 -> "${n shr 10} KB"
    else -> "$n B"
}

@Composable
fun FilesView(app: RemoteApp, workspace: String) {
    val scope = rememberCoroutineScope()
    var folder by remember(workspace) { mutableStateOf("") }
    var entries by remember(workspace) { mutableStateOf<List<ProjectFile>?>(null) }
    var open by remember(workspace) { mutableStateOf<ProjectFile?>(null) }
    var cited by remember(workspace) { mutableStateOf<IntRange?>(null) }
    LaunchedEffect(app.fileFocus) {
        val citation = app.fileFocus ?: return@LaunchedEffect
        app.fileFocus = null
        folder = citation.path.substringBeforeLast('/', "")
        open = ProjectFile(citation.path.substringAfterLast('/'), citation.path, false, 0)
        cited = if (citation.start > 0) citation.start..citation.end else null // a whole file: nothing to mark
    }
    var status by remember { mutableStateOf<String?>(null) }
    var round by remember { mutableIntStateOf(0) }
    var copying by remember { mutableStateOf(false) }
    if (copying) CopyProjectDialog(app, workspace) { copying = false; round++ }
    val picker = rememberLauncherForActivityResult(ActivityResultContracts.OpenMultipleDocuments()) { uris ->
        if (uris.isEmpty()) return@rememberLauncherForActivityResult
        val into = folder
        status = "Adding ${uris.size} file(s)…"
        scope.launch {
            status = try {
                uris.forEach { app.addToProject(it, workspace, into) }
                round++
                "Added ${uris.size} file(s)."
            } catch (cancelled: CancellationException) { throw cancelled } catch (failure: Exception) { failure.message }
        }
    }
    LaunchedEffect(workspace, folder, round) {
        entries = null
        entries = try { app.listFiles(workspace, folder) } catch (cancelled: CancellationException) { throw cancelled } catch (failure: Exception) { status = failure.message; emptyList() }
    }
    open?.let { file ->
        FilePreview(app, workspace, file, cited, onBack = { open = null; cited = null }) { status = it }
        return
    }
    BackHandler(enabled = folder.isNotEmpty()) { folder = folder.substringBeforeLast('/', "") }
    Column(Modifier.fillMaxSize().pointerInput(Unit) { detectTapGestures(onTap = {}) }) {
        Row(Modifier.fillMaxWidth().padding(horizontal = 8.dp, vertical = 6.dp), verticalAlignment = Alignment.CenterVertically) {
            if (folder.isNotEmpty()) RoundButton(Icons.back, Color.Transparent, Palette.muted, size = 36.dp, description = "Parent folder") { folder = folder.substringBeforeLast('/', "") }
            Text(
                (workspace.substringAfterLast('/') + if (folder.isEmpty()) "" else " / " + folder.replace("/", " / ")),
                style = Type.body.copy(fontWeight = FontWeight.Medium), maxLines = 1, overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f).padding(horizontal = 8.dp),
            )
            Pill("Copy") { copying = true }
            Spacer(Modifier.width(6.dp))
            Pill("Add", icon = Icons.plus) { picker.launch(arrayOf("*/*")) }
            Spacer(Modifier.width(6.dp))
            RoundButton(Icons.refresh, Color.Transparent, Palette.muted, size = 36.dp, iconSize = 18.dp, description = "Refresh") { round++ }
        }
        status?.let { Text(it, style = Type.small, modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp)) }
        val list = entries
        when {
            list == null -> Box(Modifier.fillMaxWidth().padding(24.dp), contentAlignment = Alignment.Center) { WorkRing("wait", 30.dp) }
            list.isEmpty() -> Text("This folder is empty.", style = Type.small, modifier = Modifier.padding(16.dp))
            else -> LazyColumn(Modifier.weight(1f)) {
                items(list, key = { it.path }) { entry ->
                    Row(
                        Modifier.fillMaxWidth().clickable { if (entry.dir) folder = entry.path else open = entry }.padding(horizontal = 16.dp, vertical = 12.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Icon(if (entry.dir) Icons.folder else Icons.document, null, tint = if (entry.dir) Palette.amber else Palette.muted, modifier = Modifier.size(18.dp))
                        Spacer(Modifier.width(12.dp))
                        Text(entry.name, style = Type.body, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
                        if (!entry.dir) Text(byteSize(entry.size), style = Type.caption.copy(color = Palette.faint))
                    }
                }
            }
        }
    }
}

@Composable
private fun FilePreview(app: RemoteApp, workspace: String, file: ProjectFile, cited: IntRange?, onBack: () -> Unit, onStatus: (String?) -> Unit) {
    BackHandler(onBack = onBack)
    val scope = rememberCoroutineScope()
    var preview by remember(file) { mutableStateOf<JSONObject?>(null) }
    var picture by remember(file) { mutableStateOf<android.graphics.Bitmap?>(null) }
    var problem by remember(file) { mutableStateOf<String?>(null) }
    var note by remember(file) { mutableStateOf<String?>(null) }
    var sending by remember(file) { mutableStateOf(false) }
    if (sending) SendFileDialog(app, workspace, file) { sending = false }
    LaunchedEffect(file) {
        try {
            val loaded = app.filePreview(workspace, file.path)
            if (loaded.optBoolean("image") && file.size <= 16L shl 20) {
                val bytes = app.fileBytes(workspace, file.path)
                picture = BitmapFactory.decodeByteArray(bytes, 0, bytes.size)
            }
            preview = loaded
        } catch (cancelled: CancellationException) { throw cancelled } catch (failure: Exception) { problem = failure.message }
    }
    Column(Modifier.fillMaxSize().pointerInput(Unit) { detectTapGestures(onTap = {}) }) {
        Row(Modifier.fillMaxWidth().padding(horizontal = 8.dp, vertical = 6.dp), verticalAlignment = Alignment.CenterVertically) {
            RoundButton(Icons.back, Color.Transparent, Palette.muted, size = 36.dp, description = "Back to the folder", onClick = onBack)
            Text(file.name, style = Type.body.copy(fontWeight = FontWeight.Medium), maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f).padding(horizontal = 8.dp))
        }
        Row(Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()).padding(horizontal = 12.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Pill("Download") {
                note = "Downloading…"
                scope.launch {
                    note = try { "Saved in " + app.downloadFile(workspace, file) } catch (cancelled: CancellationException) { throw cancelled } catch (failure: Exception) { failure.message }
                }
            }
            Pill("Send to…") { sending = true }
            Pill("Attach to message", icon = Icons.plus) {
                app.attachProjectFile(workspace, file)
                note = "Attached to your next message."
            }
        }
        note?.let { Text(it, style = Type.small, modifier = Modifier.padding(horizontal = 16.dp, vertical = 6.dp)) }
        Box(Modifier.weight(1f).fillMaxWidth().padding(12.dp)) {
            val shown = preview
            when {
                problem != null -> Text(problem!!, style = Type.small.copy(color = Palette.danger))
                shown == null -> WorkRing("wait", 30.dp)
                picture != null -> Image(picture!!.asImageBitmap(), file.name, contentScale = ContentScale.Fit, modifier = Modifier.fillMaxSize())
                shown.optBoolean("binary") || shown.optBoolean("image") -> Text("Binary file · ${byteSize(file.size)}. Download it to open it.", style = Type.small)
                else -> {
                    val scroll = rememberScrollState()
                    Column(Modifier.verticalScroll(scroll)) {
                        var text = shown.optString("text")
                        if (shown.optBoolean("truncated")) text += "\n… (the preview shows the first ${byteSize(text.length.toLong())} of ${byteSize(file.size)})"
                        FileCode(text, file.name, cited) { top -> scope.launch { scroll.scrollTo(top) } }
                    }
                }
            }
        }
    }
}

/** Files shared to 1loop from another app: into the open chat's project, or attached to the next message. */
@Composable
fun SharedFilesDialog(app: RemoteApp) {
    if (app.shared.isEmpty()) return
    val scope = rememberCoroutineScope()
    var problem by remember { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf(false) }
    val workspace = app.chats.firstOrNull { it.id == app.current }?.workspace ?: app.selectedProject ?: app.workspace
    AlertDialog(
        onDismissRequest = { if (!busy) app.shared.clear() },
        containerColor = Palette.surface,
        title = { Text("${app.shared.size} file(s) shared to 1loop", style = Type.title) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text("Add them to the project “${workspace.substringAfterLast('/')}” on ${app.server?.name ?: "the computer"}, or attach them to your next message.", style = Type.small)
                problem?.let { Text(it, style = Type.small.copy(color = Palette.danger)) }
            }
        },
        confirmButton = {
            Pill(if (busy) "Adding…" else "Add to project", primary = true, enabled = !busy && app.connected) {
                busy = true
                scope.launch {
                    try {
                        app.shared.toList().forEach { app.addToProject(it, workspace, "") }
                        app.shared.clear()
                    } catch (cancelled: CancellationException) { throw cancelled } catch (failure: Exception) { problem = failure.message } finally { busy = false }
                }
            }
        },
        dismissButton = {
            Pill("Attach to message", enabled = !busy && app.connected) {
                app.attach(app.shared.toList(), null)
                app.shared.clear()
            }
        },
    )
}

/**
 * Copy project: this project, files and conversations, between this phone
 * and a paired desktop or server. A copy first says what it will send; each
 * one sends only what changed since the last.
 */
@Composable
private fun CopyProjectDialog(app: RemoteApp, workspace: String, onDone: () -> Unit) {
    val scope = rememberCoroutineScope()
    val name = workspace.substringAfterLast('/')
    val here = app.server
    // On this phone's engine: the paired desktops and servers. Elsewhere: that computer and this phone.
    val peers = if (here?.localPhone == true) app.servers.filter { !it.localPhone && it.token.isNotEmpty() } else listOfNotNull(here)
    var status by remember { mutableStateOf<String?>(null) }
    var pending by remember { mutableStateOf<Pair<Server, Boolean>?>(null) } // the planned copy: peer, pull
    var busy by remember { mutableStateOf(false) }
    fun run(peer: Server, pull: Boolean, planOnly: Boolean) {
        busy = true
        status = if (planOnly) "Comparing both sides…" else "Copying…"
        scope.launch {
            try {
                val local = if (here?.localPhone == true) workspace else name
                val remote = if (here?.localPhone == true) name else workspace
                val reply = app.copyProject(peer, pull, local, remote, planOnly)
                val plan = reply.optJSONObject("report")?.optJSONObject("plan")
                val empty = plan == null || listOf("copy", "conflicts", "delete", "chats", "chatConflicts").all { (plan.optJSONArray(it)?.length() ?: 0) == 0 }
                if (planOnly && !empty) {
                    pending = peer to pull
                    status = "Will copy: " + reply.optString("summary")
                } else {
                    pending = null
                    val conflicts = reply.optJSONObject("report")?.optJSONObject("result")?.optJSONArray("conflicts")
                    status = (if (planOnly) "" else "Copied: ") + reply.optString("summary") +
                        if ((conflicts?.length() ?: 0) > 0) "\nKept twice: " + (0 until conflicts!!.length()).joinToString { conflicts.getString(it) } else ""
                }
            } catch (cancelled: CancellationException) { throw cancelled } catch (failure: Exception) { status = failure.message } finally { busy = false }
        }
    }
    AlertDialog(
        onDismissRequest = { if (!busy) onDone() },
        containerColor = Palette.surface,
        title = { Text("Copy “$name”", style = Type.title) },
        text = {
            Column(Modifier.verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                Text("Copies the project's files and its conversations. Each copy sends only what changed since the last one; what changed on both sides is kept twice, and a file deleted on one side goes to the other side's trash. Local credentials, .env files, private keys and generated caches are excluded. Links are not copied. Compare again after an interruption to retry only what is still missing.", style = Type.small)
                if (peers.isEmpty()) Text("Pair a desktop or a server first: it then appears here.", style = Type.small.copy(color = Palette.danger))
                for (peer in peers) {
                    if (here?.localPhone == true) {
                        Pill("Send to ${peer.name}", enabled = !busy) { run(peer, false, true) }
                        Pill("Get from ${peer.name}", enabled = !busy) { run(peer, true, true) }
                    } else {
                        Pill("Copy to this phone", enabled = !busy) { run(peer, true, true) }
                        Pill("Get this phone's version", enabled = !busy) { run(peer, false, true) }
                    }
                }
                status?.let { Text(it, style = Type.small) }
            }
        },
        confirmButton = {
            pending?.let { (peer, pull) -> Pill("Copy now", primary = true, enabled = !busy) { run(peer, pull, false) } }
        },
        dismissButton = { Pill("Close", enabled = !busy, onClick = onDone) },
    )
}

@Composable
private fun SendFileDialog(app: RemoteApp, workspace: String, file: ProjectFile, close: () -> Unit) {
    val scope = rememberCoroutineScope()
    val local = app.local.activeServer.collectAsState().value
    val peers = (app.servers + listOfNotNull(local)).filter { it.token.isNotEmpty() && it.key != app.server?.key }.distinctBy { it.key }
    var selected by remember { mutableStateOf(peers.firstOrNull()) }
    var destination by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var status by remember { mutableStateOf<String?>(null) }
    AlertDialog(
        onDismissRequest = { if (!busy) close() }, containerColor = Palette.surface,
        title = { Text("Send “${file.name}”", style = Type.title) },
        text = {
            Column(Modifier.verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text("Copies this file to a paired device. Existing names get a number.", style = Type.small)
                if (peers.isEmpty()) Text("Pair a destination first, or start Local phone to copy here.", style = Type.small)
                peers.forEach { peer -> Pill(peer.name, primary = selected?.key == peer.key, enabled = !busy) { selected = peer } }
                Field(destination, { destination = it }, "Destination project folder (empty: current project)", KeyboardType.Text)
                status?.let { Text(it, style = Type.small) }
            }
        },
        confirmButton = {
            Pill(if (busy) "Sending…" else "Send", primary = true, enabled = selected != null && !busy) {
                val peer = selected ?: return@Pill
                busy = true
                scope.launch {
                    try { status = "Sent: " + app.sendProjectFile(workspace, file, peer, destination) }
                    catch (cancelled: CancellationException) { throw cancelled }
                    catch (failure: Exception) { status = failure.message }
                    finally { busy = false }
                }
            }
        },
        dismissButton = { Pill("Close", enabled = !busy, onClick = close) },
    )
}

@Composable
private fun FileCode(text: String, name: String, cited: IntRange? = null, onCitedTop: (Int) -> Unit = {}) {
    val colored = remember(text) {
        val token = Regex(""""(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|//[^\n]*|#[^\n]*|\b(?:func|fun|def|class|package|import|from|return|if|else|for|while|val|var|const|let|true|false|null|nil|public|private|void|int|string|bool)\b""")
        buildAnnotatedString {
            text.trimEnd('\n').lines().forEachIndexed { index, line ->
                if (index > 0) append('\n')
                val marked = cited != null && index + 1 in cited
                if (marked) pushStyle(SpanStyle(background = Palette.raised))
                withStyle(SpanStyle(color = if (marked) Palette.amber else Palette.faint)) { append("${index + 1}  ") }
                var position = 0
                token.findAll(line).forEach { match ->
                    append(line.substring(position, match.range.first))
                    val color = when { match.value.startsWith("//") || match.value.startsWith("#") -> Palette.faint; match.value.startsWith("\"") || match.value.startsWith("'") -> Palette.added; else -> Palette.amber }
                    withStyle(SpanStyle(color = color)) { append(match.value) }
                    position = match.range.last + 1
                }
                append(line.substring(position))
                if (marked) pop()
            }
        }
    }
    var scrolled by remember(text, cited) { mutableStateOf(false) }
    Column(Modifier.fillMaxWidth()) {
        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
            Text(name, style = Type.caption, modifier = Modifier.weight(1f))
            CopyChip { text }
        }
        Text(colored, style = Type.mono, softWrap = false, modifier = Modifier.horizontalScroll(rememberScrollState()), onTextLayout = { layout ->
            // The cited lines in view, a few lines above them too.
            if (cited != null && !scrolled && cited.first - 1 < layout.lineCount) {
                scrolled = true
                onCitedTop(layout.getLineTop(maxOf(cited.first - 4, 0)).toInt())
            }
        })
    }
}

/** A citation of the model's answer (<cite file lines/>): the file and its lines, 0 for a whole file. */
data class Citation(val path: String, val start: Int, val end: Int)

/** Second FileProvider of the app (Android wants one class per provider): cited files opened in another app. */
class OpenedFiles : androidx.core.content.FileProvider()
