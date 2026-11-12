// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.animateContentSize
import androidx.compose.animation.core.tween
import androidx.compose.animation.expandVertically
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.shrinkVertically
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch

// The drawer, laid out as the desktop's sidebar (internal/ui/sidebar_groups.go):
// the actions New chat / Search / Add project, then every project as a group
// that folds, with its chats (the first few, then "Show N more"), a "No folder"
// group first and an "Other chats" group last. A chat or a project has its menu
// ("…" or a long press); a worktree's chat shows its branch.

/** A project shows this many chats until "Show more" is pressed (groupChatLimit on the desktop). */
internal const val GROUP_CHAT_LIMIT = 5

private const val SCRATCH_GROUP = ":scratch"
private const val OTHER_GROUP = ":other"

/** A group of the drawer: a project, the chats with no folder, or the chats no project lists. */
private class SideGroup(val key: String, val name: String, val project: String?, val chats: List<ChatSummary>)

/** A subchat is listed under its parent, wherever its folder is (its worktree); one whose parent is not listed stays a chat of its own. */
private class SideChats(all: List<ChatSummary>) {
    private val listed = all.map { it.id }.toSet()
    val children = all.filter { it.parent.isNotEmpty() && it.parent != it.id && it.parent in listed }.groupBy { it.parent }
    val top = all.filter { it.parent.isEmpty() || it.parent == it.id || it.parent !in listed }

    /** The chat is open, or one of its subchats is. */
    fun openHere(chat: ChatSummary, current: String?) = chat.id == current || children[chat.id].orEmpty().any { it.id == current }

    fun workingHere(chat: ChatSummary) = chat.running || children[chat.id].orEmpty().any { it.running }
}

private fun sideGroups(app: RemoteApp, chats: List<ChatSummary>): List<SideGroup> {
    val scratch = chats.filter { it.scratch }
    val rest = chats.filter { !it.scratch }
    val listed = app.projects.filter { it !in app.worktrees }
    val groups = mutableListOf<SideGroup>()
    if (scratch.isNotEmpty() || app.selectedProject == NO_FOLDER) groups += SideGroup(SCRATCH_GROUP, "No folder", null, scratch)
    for (project in listed) groups += SideGroup(project, projectName(project), project, rest.filter { app.projectOf(it.workspace) == project })
    val other = rest.filter { app.projectOf(it.workspace) !in listed }
    if (other.isNotEmpty()) groups += SideGroup(OTHER_GROUP, "Other chats", null, other)
    return groups
}

@Composable
internal fun ChatsPane(app: RemoteApp, modifier: Modifier, onPicked: () -> Unit) {
    var deleting by remember(app.server) { mutableStateOf<ChatSummary?>(null) }
    var renaming by remember(app.server) { mutableStateOf<ChatSummary?>(null) }
    var chatMenu by remember(app.server) { mutableStateOf<ChatSummary?>(null) }
    var groupMenu by remember(app.server) { mutableStateOf<String?>(null) } // a group's key
    var projectActions by remember(app.server) { mutableStateOf<String?>(null) }
    var worktreeActions by remember(app.server) { mutableStateOf<Pair<String, Worktree>?>(null) }
    var newWorktree by remember(app.server) { mutableStateOf<String?>(null) }
    val shownInFull = remember(app.server) { mutableStateListOf<String>() } // groups whose every chat is listed
    val side = SideChats(app.chats)
    Column(modifier.background(Palette.sidebar).windowInsetsPadding(WindowInsets.safeDrawing).padding(horizontal = 12.dp)) {
        Image(painterResource(if (Palette.dark) R.drawable.logo else R.drawable.logo_light), "1loop", modifier = Modifier.padding(start = 6.dp, top = 14.dp, bottom = 14.dp).height(28.dp))
        // The labelled actions, in the desktop's order.
        NavRow("New chat", Icons.plus, selected = false) { app.open(null); onPicked() }
        NavRow("Search", Icons.search, selected = false) { app.searchOpen = true; onPicked() }
        if (app.projectManagement) NavRow("Add project", Icons.folderAdd, selected = false) { app.projectsOpen = true; onPicked() }
        Column(Modifier.weight(1f).verticalScroll(rememberScrollState())) {
            Text("Projects", style = Type.small.copy(color = Palette.faint), modifier = Modifier.padding(start = 10.dp, top = 14.dp, bottom = 2.dp))
            val groups = sideGroups(app, app.chats)
            val opened = app.projectOf(app.selectedProject)
            for (group in groups) {
                val folded = app.folded(group.key)
                val git = group.project?.let { app.repositories[it] }?.takeIf { it.loaded && it.error == null }
                NavRow(
                    group.name, null, selected = app.current == null && (if (group.key == SCRATCH_GROUP) app.selectedProject == NO_FOLDER else group.project != null && group.project == app.selectedProject),
                    chevron = if (folded) Icons.chevronRight else Icons.chevron,
                    running = folded && group.chats.any { side.workingHere(it) },
                    // A project on an SSH machine says so, beside its branch.
                    trailing = listOfNotNull(git?.current?.takeIf { it.isNotEmpty() }, group.project?.let { app.machineOf(it) }?.let { "on ${it.label}" })
                        .joinToString(" · ").ifEmpty { null },
                    onLongClick = if (group.key == OTHER_GROUP) null else ({ groupMenu = group.key }),
                    longClickLabel = "Project menu",
                    onMore = if (group.key == OTHER_GROUP) null else ({ groupMenu = group.key }),
                    moreLabel = "Menu of ${group.name}",
                ) { app.fold(group.key, !folded) }
                AnimatedVisibility(
                    !folded,
                    enter = expandVertically(motion(tween(200, easing = CssEase))) + fadeIn(motion(tween(160, 40))),
                    exit = shrinkVertically(motion(tween(180, easing = CssEase))) + fadeOut(motion(tween(120))),
                ) {
                    Column(Modifier.animateContentSize(motion(tween(200, easing = CssEase)))) {
                        GroupChats(app, group, side, group.key in shownInFull, { full -> if (full) shownInFull.add(group.key) else shownInFull.remove(group.key) }, onPicked,
                            onMenu = { chatMenu = it })
                        // Its worktrees, after its chats, while the project is the open one.
                        if (group.project != null && group.project == opened) {
                            Column(Modifier.padding(start = 16.dp)) {
                                WorktreeEntries(app, group.project, {}, { worktreeActions = group.project to it }) { newWorktree = group.project }
                            }
                        }
                    }
                }
            }
            if (groups.isEmpty()) Text("No projects yet", style = Type.caption, modifier = Modifier.padding(10.dp))
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
        NavRow("Settings", Icons.settings, selected = false) { app.settingsOpen = true; onPicked() }
        NavRow("Switch execution target", Icons.refresh, selected = false) { app.switchTarget() }
        Spacer(Modifier.height(8.dp))
    }
    // A group's menu: a new chat in it, then the project's own actions.
    groupMenu?.let { key ->
        val group = sideGroups(app, app.chats).firstOrNull { it.key == key }
        when {
            group == null -> groupMenu = null
            group.project != null -> ProjectActionsSheet(
                app, group.project, onNewWorktree = { newWorktree = group.project },
                onNewChat = { app.selectProject(group.project); app.open(null); onPicked() },
            ) { groupMenu = null }
            else -> SideMenuSheet("No folder", listOf(SideAction("New chat with no folder", Icons.plus) { app.selectProject(NO_FOLDER); onPicked() })) { groupMenu = null }
        }
    }
    chatMenu?.let { chat ->
        val parent = app.chats.firstOrNull { it.id == chat.parent }
        SideMenuSheet(
            chat.title,
            listOfNotNull(
                if (chat.parent.isNotEmpty() && chat.running) SideAction("Stop", Icons.close) { app.stop(chat.id) } else null,
                if (parent != null) SideAction("Open parent", Icons.subchat) { app.open(parent.id); onPicked() } else null,
                SideAction("Rename", Icons.pencil) { renaming = chat },
                SideAction("Delete", Icons.trash, danger = true) { deleting = chat },
            ),
        ) { chatMenu = null }
    }
    worktreeActions?.let { (project, worktree) -> WorktreeSheet(app, project, worktree) { worktreeActions = null } }
    newWorktree?.let { project -> NewWorktreeSheet(app, project) { newWorktree = null } }
    renaming?.let { chat -> RenameChatSheet(app, chat) { renaming = null } }
    deleting?.let { chat -> DeleteChatSheet(app, chat) { deleting = null } }
}

/** A group's chats: the first few unless it was unfolded in full or the open chat is further down. */
@Composable
private fun GroupChats(app: RemoteApp, group: SideGroup, side: SideChats, full: Boolean, setFull: (Boolean) -> Unit, onPicked: () -> Unit, onMenu: (ChatSummary) -> Unit) {
    val chats = group.chats.filter { chat -> side.top.any { it.id == chat.id } }
    Column(Modifier.padding(start = 16.dp)) {
        if (chats.isEmpty()) {
            Text("No chats yet", style = Type.caption, modifier = Modifier.padding(10.dp))
            return@Column
        }
        var shown = chats
        if (!full && chats.size > GROUP_CHAT_LIMIT) {
            shown = chats.take(GROUP_CHAT_LIMIT)
            if (chats.drop(GROUP_CHAT_LIMIT).any { side.openHere(it, app.current) }) shown = chats
        }
        for (chat in shown) {
            // A worktree's chat: its branch's last part, short.
            val branch = if (group.project != null && chat.workspace != group.project) {
                (app.repositories[group.project]?.worktrees?.firstOrNull { it.path == chat.workspace }?.label ?: projectName(chat.workspace)).substringAfterLast('/')
            } else null
            NavRow(
                chat.title, null, selected = chat.id == app.current, running = chat.running, status = chat.status, blocked = chat.blocked,
                trailing = branch, onLongClick = { onMenu(chat) }, longClickLabel = "Chat menu", onMore = { onMenu(chat) }, moreLabel = "Menu of ${chat.title}",
            ) { app.open(chat.id); onPicked() }
            side.children[chat.id]?.let { subs ->
                Column(Modifier.padding(start = 16.dp)) {
                    for (sub in subs) NavRow(
                        sub.title, Icons.subchat, selected = sub.id == app.current, running = sub.running,
                        onLongClick = { onMenu(sub) }, longClickLabel = "Chat menu", onMore = { onMenu(sub) }, moreLabel = "Menu of ${sub.title}",
                    ) { app.open(sub.id); onPicked() }
                }
            }
        }
        if (chats.size > GROUP_CHAT_LIMIT) {
            Text(
                if (shown.size == chats.size) "Show less" else "Show ${chats.size - shown.size} more",
                style = Type.caption.copy(color = Palette.faint, fontSize = 13.sp),
                modifier = Modifier.fillMaxWidth().clip(RoundedCornerShape(8.dp)).clickable { setFull(shown.size < chats.size) }.padding(horizontal = 10.dp, vertical = 10.dp),
            )
        }
    }
}

/** One entry of a bottom-sheet menu. */
internal class SideAction(val label: String, val icon: ImageVector, val danger: Boolean = false, val run: () -> Unit)

/** A chat's or a group's menu, one row per action, as the desktop's "…" menu. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun SideMenuSheet(title: String, actions: List<SideAction>, onDone: () -> Unit) {
    GlassBackdrop()
    ModalBottomSheet(
        onDismissRequest = onDone, containerColor = Palette.surface, scrimColor = GlassScrim,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
    ) {
        Column(Modifier.padding(horizontal = 12.dp).padding(bottom = 24.dp)) {
            Text(title, style = Type.title, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.padding(horizontal = 12.dp, vertical = 8.dp))
            for (action in actions) {
                Row(
                    Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).clickable { onDone(); action.run() }.padding(horizontal = 12.dp, vertical = 14.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    val ink = if (action.danger) Palette.danger else Palette.text
                    Icon(action.icon, null, tint = ink, modifier = Modifier.size(20.dp))
                    Spacer(Modifier.width(14.dp))
                    Text(action.label, style = Type.body.copy(color = ink))
                }
            }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun RenameChatSheet(app: RemoteApp, chat: ChatSummary, onDone: () -> Unit) {
    var title by remember(chat.id) { mutableStateOf(chat.title) }
    var busy by remember { mutableStateOf(false) }
    var problem by remember { mutableStateOf<String?>(null) }
    val scope = rememberCoroutineScope()
    GlassBackdrop()
    ModalBottomSheet(
        onDismissRequest = { if (!busy) onDone() }, containerColor = Palette.surface, scrimColor = GlassScrim,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
    ) {
        Column(Modifier.padding(horizontal = 24.dp).padding(bottom = 24.dp), verticalArrangement = Arrangement.spacedBy(14.dp)) {
            Text("Rename chat", style = Type.title)
            Field(title, { title = it }, "Chat title", KeyboardType.Text, Modifier.fillMaxWidth())
            problem?.let { Text(it, style = Type.small.copy(color = Palette.danger)) }
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
                Pill("Cancel", enabled = !busy, onClick = onDone)
                Spacer(Modifier.width(10.dp))
                Pill(if (busy) "Renaming…" else "Rename", primary = true, enabled = !busy && title.isNotBlank() && title.trim() != chat.title) {
                    busy = true
                    problem = null
                    scope.launch {
                        try {
                            app.renameChat(chat.id, title.trim())
                            onDone()
                        } catch (cancelled: CancellationException) {
                            throw cancelled
                        } catch (failure: Exception) {
                            problem = failure.message ?: "Could not rename the chat."
                        } finally {
                            busy = false
                        }
                    }
                }
            }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun DeleteChatSheet(app: RemoteApp, chat: ChatSummary, onDone: () -> Unit) {
    GlassBackdrop()
    ModalBottomSheet(
        onDismissRequest = onDone, containerColor = Palette.surface, scrimColor = GlassScrim,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
    ) {
        Column(Modifier.padding(horizontal = 24.dp).padding(bottom = 24.dp)) {
            Text("Delete chat?", style = Type.title)
            Spacer(Modifier.height(12.dp))
            Text(
                if (chat.scratch) "“${chat.title}” and the folder 1loop made for it will be permanently deleted. Its running tasks and tabs will stop."
                else "“${chat.title}” will be permanently deleted. Its running tasks and tabs will stop. Project files are kept.",
                style = Type.body,
            )
            Spacer(Modifier.height(20.dp))
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
                Pill("Cancel", onClick = onDone)
                Spacer(Modifier.width(10.dp))
                Pill("Delete", primary = true) {
                    app.deleteChat(chat.id)
                    onDone()
                }
            }
        }
    }
}
