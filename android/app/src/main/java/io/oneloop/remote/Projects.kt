package io.oneloop.remote

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch
import org.json.JSONObject

internal fun projectName(path: String) = path.trimEnd('/', '\\').substringAfterLast('/').substringAfterLast('\\')

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ProjectsSheet(app: RemoteApp, onDone: () -> Unit) {
    var requested by remember { mutableStateOf("") }
    var page by remember { mutableStateOf<JSONObject?>(null) }
    var name by remember { mutableStateOf("") }
    var problem by remember { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    LaunchedEffect(app.server, requested) {
        problem = null
        page = null
        try { page = app.projectFolders(requested) }
        catch (cancelled: CancellationException) { throw cancelled }
        catch (failure: Exception) { problem = failure.message ?: "Could not load folders." }
    }
    fun choose(path: String, create: Boolean) {
        busy = true
        scope.launch {
            try { app.addProject(path, create); onDone() }
            catch (cancelled: CancellationException) { throw cancelled }
            catch (failure: Exception) { problem = failure.message ?: "Could not add the project." }
            finally { busy = false }
        }
    }
    ModalBottomSheet(onDismissRequest = onDone, containerColor = Palette.surface, sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)) {
        Column(Modifier.verticalScroll(rememberScrollState()).padding(20.dp).padding(bottom = 24.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Text("Choose a project folder", style = Type.title)
            Text("Folders are on the selected phone, desktop or server. Browsing stays within its authorized project area.", style = Type.small)
            page?.let { folders ->
                val path = folders.getString("path")
                Text(path, style = Type.small)
                val parent = folders.optString("parent")
                if (parent.isNotEmpty()) Pill("Parent folder", enabled = !busy) { requested = parent }
                val array = folders.getJSONArray("folders")
                for (i in 0 until array.length()) {
                    val child = array.getString(i)
                    Text("▸ " + projectName(child), style = Type.body, modifier = Modifier.fillMaxWidth().clickable(enabled = !busy) { requested = child }.padding(10.dp))
                }
                Pill("Use this folder", primary = true, enabled = !busy && !(app.server?.localPhone == true && path == folders.getString("base"))) { choose(path, false) }
                Text("Create a project folder here", style = Type.headline)
                Field(name, { name = it }, "Folder name", KeyboardType.Text)
                Pill("Create project", enabled = !busy && name.isNotBlank() && name !in listOf(".", "..") && name.none { it == '/' || it == '\\' }) {
                    choose(path.trimEnd('/', '\\') + folders.getString("separator") + name.trim(), true)
                }
            } ?: Text("Loading folders…", style = Type.small)
            problem?.let { Text(it, style = Type.small.copy(color = Palette.danger)) }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ProjectActionsSheet(app: RemoteApp, path: String, onDone: () -> Unit) {
    var action by remember(path) { mutableStateOf("") }
    var name by remember(path) { mutableStateOf(projectName(path)) }
    var busy by remember { mutableStateOf(false) }
    var problem by remember { mutableStateOf<String?>(null) }
    val scope = rememberCoroutineScope()
    fun apply() {
        busy = true
        problem = null
        scope.launch {
            try {
                app.changeProject(path, if (action == "rename") name.trim() else null)
                onDone()
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (failure: Exception) {
                problem = failure.message ?: "Could not change the project."
            } finally {
                busy = false
            }
        }
    }
    ModalBottomSheet(
        onDismissRequest = { if (!busy) onDone() },
        containerColor = Palette.surface,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
    ) {
        Column(
            Modifier.verticalScroll(rememberScrollState()).padding(24.dp).padding(bottom = 24.dp),
            verticalArrangement = Arrangement.spacedBy(14.dp),
        ) {
            Text(when (action) {
                "rename" -> "Rename project"
                "delete" -> "Delete project?"
                else -> projectName(path)
            }, style = Type.title)
            Text(path, style = Type.small)
            when (action) {
                "rename" -> {
                    Text("Renames this folder and keeps its contents and chats. Stop its agents and close its terminals first.", style = Type.body)
                    Field(name, { name = it }, "Folder name", KeyboardType.Text)
                    Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                        Pill("Cancel", enabled = !busy, onClick = onDone)
                        Pill("Rename", primary = true, enabled = !busy && name.isNotBlank() && name.trim() != projectName(path) && name.trim() !in listOf(".", "..") && name.none { it == '/' || it == '\\' }) { apply() }
                    }
                }
                "delete" -> {
                    Text("Permanently deletes this folder, ALL its files and subfolders, and its chats. This cannot be undone. Stop its agents and close its terminals first.", style = Type.body)
                    Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                        Pill("Cancel", enabled = !busy, onClick = onDone)
                        Pill(if (busy) "Deleting…" else "Delete project and files", primary = true, enabled = !busy) { apply() }
                    }
                }
                else -> {
                    Pill("Rename") { action = "rename" }
                    Pill("Delete project") { action = "delete" }
                }
            }
            problem?.let { Text(it, style = Type.small.copy(color = Palette.danger)) }
        }
    }
}
