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
import androidx.compose.ui.graphics.ColorFilter
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
// the actions New chat / Search / Add project, then every linked target's
// projects together (Targets.kt), each as a group that folds, with its chats
// (the first few, then "Show N more"), a "No folder" group first and a
// target's "Other chats" after its projects. A project of another target than
// this phone shows the target's icon after its name, and "offline" while the
// target does not answer. A chat or a project has its menu ("…" or a long
// press); a worktree's chat shows its branch. Opening a project or a chat of
// another target opens that target first.

/** A project shows this many chats until "Show more" is pressed (groupChatLimit on the desktop). */
internal const val GROUP_CHAT_LIMIT = 5

/** A subchat is listed under its parent, wherever its folder is (its worktree); one whose parent is not listed stays a chat of its own. */
private class SideChats(all: List<TargetChat>) {
    private fun TargetChat.id() = target + "\u0000" + chat.id
    private fun TargetChat.parentId() = target + "\u0000" + chat.parent
    private val listed = all.map { it.id() }.toSet()
    private fun nested(entry: TargetChat) = entry.chat.parent.isNotEmpty() && entry.chat.parent != entry.chat.id && entry.parentId() in listed
    private val children = all.filter { nested(it) }.groupBy { it.parentId() }
    val top = all.filterNot { nested(it) }.map { it.id() }.toSet()

    fun isTop(entry: TargetChat) = entry.id() in top
    fun children(entry: TargetChat) = children[entry.id()].orEmpty()

    /** The chat is open, or one of its subchats is. */
    fun openHere(entry: TargetChat, app: RemoteApp) = app.isOpen(entry) || children(entry).any { app.isOpen(it) }

    fun workingHere(entry: TargetChat) = entry.chat.running || children(entry).any { it.chat.running }
}

/** Whether a drawer chat is the one on screen: on the open target. */
private fun RemoteApp.isOpen(entry: TargetChat) = entry.target == server?.key && entry.chat.id == current

/** A group's fold key on its target: its project, or the key the group had before targets. */
private fun DrawerGroup.foldKey() = project ?: if (key == SCRATCH_GROUP) SCRATCH_GROUP else OTHER_GROUP

private fun markIcon(kind: TargetKind?) = when (kind) {
    TargetKind.Desktop -> Icons.computer
    TargetKind.Server -> Icons.server
    else -> null
}

@Composable
internal fun ChatsPane(app: RemoteApp, modifier: Modifier, onPicked: () -> Unit) {
    var deleting by remember(app.server) { mutableStateOf<ChatSummary?>(null) }
    var renaming by remember(app.server) { mutableStateOf<ChatSummary?>(null) }
    var chatMenu by remember(app.server) { mutableStateOf<TargetChat?>(null) }
    var groupMenu by remember(app.server) { mutableStateOf<String?>(null) } // a group's key
    var worktreeActions by remember(app.server) { mutableStateOf<Pair<String, Worktree>?>(null) }
    var newWorktree by remember(app.server) { mutableStateOf<String?>(null) }
    val shownInFull = remember { mutableStateListOf<String>() } // groups whose every chat is listed
    val active = app.server?.key
    val targets = app.drawerTargets()
    val groups = drawerGroups(targets, app.selectedProject == NO_FOLDER)
    val side = SideChats(groups.flatMap { it.chats })
    Column(modifier.background(Palette.sidebar).windowInsetsPadding(WindowInsets.safeDrawing).padding(horizontal = 12.dp)) {
        // The ring and the "1" take the accent, drawn over the rest of the logo.
        Box(Modifier.padding(start = 6.dp, top = 14.dp, bottom = 14.dp).height(28.dp)) {
            Image(painterResource(if (Palette.dark) R.drawable.logo else R.drawable.logo_light), "1loop", modifier = Modifier.height(28.dp))
            Image(painterResource(R.drawable.logo_accent), null, modifier = Modifier.height(28.dp), colorFilter = ColorFilter.tint(Palette.amber))
        }
        // The labelled actions, in the desktop's order.
        NavRow("New chat", Icons.plus, selected = false) { app.open(null); onPicked() }
        NavRow("Search", Icons.search, selected = false) { app.searchOpen = true; onPicked() }
        if (app.projectManagement) NavRow("Add project", Icons.folderAdd, selected = false) { app.projectsOpen = true; onPicked() }
        Column(Modifier.weight(1f).verticalScroll(rememberScrollState())) {
            Text("Projects", style = Type.small.copy(color = Palette.faint), modifier = Modifier.padding(start = 10.dp, top = 14.dp, bottom = 2.dp))
            val opened = app.projectOf(app.selectedProject)
            for (group in groups) {
                val target = group.target
                val onOpenTarget = target == null || target.active
                val folded = app.folded(target?.target?.key ?: "", group.foldKey())
                val git = group.project?.takeIf { onOpenTarget }?.let { app.repositories[it] }?.takeIf { it.loaded && it.error == null }
                val trailing = when {
                    target == null -> null
                    target.reach == Reach.Offline && !target.active -> offlineMark(target.target.kind)
                    target.active && !app.connected -> "reconnecting…"
                    // A project on an SSH machine says so, beside its branch.
                    else -> listOfNotNull(git?.current?.takeIf { it.isNotEmpty() }, group.project?.takeIf { onOpenTarget }?.let { app.machineOf(it) }?.let { "on ${it.label}" })
                        .joinToString(" · ").ifEmpty { null }
                }
                val hasMenu = group.project != null || group.key == SCRATCH_GROUP
                NavRow(
                    group.name, null,
                    selected = app.current == null && (if (group.key == SCRATCH_GROUP) app.selectedProject == NO_FOLDER else onOpenTarget && group.project != null && group.project == app.selectedProject),
                    chevron = if (folded) Icons.chevronRight else Icons.chevron,
                    mark = markIcon(target?.let { projectMark(it.target.kind) }),
                    markLabel = target?.target?.name,
                    running = folded && group.chats.any { side.workingHere(it) },
                    trailing = trailing,
                    onLongClick = if (hasMenu) ({ groupMenu = group.key }) else null,
                    longClickLabel = "Project menu",
                    onMore = if (hasMenu) ({ groupMenu = group.key }) else null,
                    moreLabel = "Menu of ${group.name}",
                ) { app.fold(target?.target?.key ?: "", group.foldKey(), !folded) }
                AnimatedVisibility(
                    !folded,
                    enter = expandVertically(motion(tween(200, easing = CssEase))) + fadeIn(motion(tween(160, 40))),
                    exit = shrinkVertically(motion(tween(180, easing = CssEase))) + fadeOut(motion(tween(120))),
                ) {
                    Column(Modifier.animateContentSize(motion(tween(200, easing = CssEase)))) {
                        if (target != null && !target.known) {
                            // A target that has not answered: its chats are not at hand.
                            Text(
                                if (target.reach == Reach.Offline) "Chats show once it's back" else "Connecting…",
                                style = Type.caption, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.padding(start = 26.dp, top = 6.dp, bottom = 6.dp),
                            )
                        } else {
                            GroupChats(app, group, side, group.key in shownInFull, { full -> if (full) shownInFull.add(group.key) else shownInFull.remove(group.key) }, onPicked,
                                onMenu = { chatMenu = it })
                        }
                        // Its worktrees, after its chats, while the project is the open one.
                        if (onOpenTarget && group.project != null && group.project == opened) {
                            Column(Modifier.padding(start = 16.dp)) {
                                WorktreeEntries(app, group.project, {}, { worktreeActions = group.project to it }) { newWorktree = group.project }
                            }
                        }
                    }
                }
            }
            if (groups.isEmpty()) Text("No projects yet", style = Type.caption, modifier = Modifier.padding(10.dp))
        }
        Box(Modifier.fillMaxWidth().height(1.dp).background(Palette.borderSoft))
        Spacer(Modifier.height(8.dp))
        // Projects come from every linked target: another one is linked here, none is switched to.
        NavRow("Link a target", Icons.plus, selected = false) { app.linkTarget() }
        NavRow("Settings", Icons.settings, selected = false) { app.settingsOpen = true; onPicked() }
        Spacer(Modifier.height(8.dp))
    }
    // A group's menu: a new chat in it, then the project's own actions on the open target.
    groupMenu?.let { key ->
        val group = groups.firstOrNull { it.key == key }
        val target = group?.target
        when {
            group == null -> groupMenu = null
            group.project != null && target != null && !target.active -> SideMenuSheet(
                group.name, listOf(SideAction("New chat in ${group.name}", Icons.plus) { app.openOn(target.target.key, group.project); onPicked() }),
            ) { groupMenu = null }
            group.project != null -> ProjectActionsSheet(
                app, group.project, onNewWorktree = { newWorktree = group.project },
                onNewChat = { app.selectProject(group.project); app.open(null); onPicked() },
            ) { groupMenu = null }
            else -> SideMenuSheet("No folder", listOf(SideAction("New chat with no folder", Icons.plus) { app.newChatWithNoFolder(); onPicked() })) { groupMenu = null }
        }
    }
    chatMenu?.let { entry ->
        val chat = entry.chat
        if (entry.target != active) {
            // Another target's chat: opened there first; its actions are there.
            SideMenuSheet(chat.title, listOf(SideAction("Open", Icons.chats) { app.openOn(entry.target, chat = chat.id); onPicked() })) { chatMenu = null }
        } else {
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
    }
    worktreeActions?.let { (project, worktree) -> WorktreeSheet(app, project, worktree) { worktreeActions = null } }
    newWorktree?.let { project -> NewWorktreeSheet(app, project) { newWorktree = null } }
    renaming?.let { chat -> RenameChatSheet(app, chat) { renaming = null } }
    deleting?.let { chat -> DeleteChatSheet(app, chat) { deleting = null } }
}

/** A group's chats: the first few unless it was unfolded in full or the open chat is further down. */
@Composable
private fun GroupChats(app: RemoteApp, group: DrawerGroup, side: SideChats, full: Boolean, setFull: (Boolean) -> Unit, onPicked: () -> Unit, onMenu: (TargetChat) -> Unit) {
    val chats = group.chats.filter { side.isTop(it) }
    Column(Modifier.padding(start = 16.dp)) {
        if (chats.isEmpty()) {
            Text("No chats yet", style = Type.caption, modifier = Modifier.padding(10.dp))
            return@Column
        }
        var shown = chats
        if (!full && chats.size > GROUP_CHAT_LIMIT) {
            shown = chats.take(GROUP_CHAT_LIMIT)
            if (chats.drop(GROUP_CHAT_LIMIT).any { side.openHere(it, app) }) shown = chats
        }
        for (entry in shown) {
            val chat = entry.chat
            val onOpenTarget = entry.target == app.server?.key
            // A worktree's chat: its branch's last part, short.
            val branch = if (group.project != null && chat.workspace != group.project) {
                (app.repositories[group.project]?.worktrees?.takeIf { onOpenTarget }?.firstOrNull { it.path == chat.workspace }?.label ?: projectName(chat.workspace)).substringAfterLast('/')
            } else null
            NavRow(
                chat.title, null, selected = app.isOpen(entry), running = chat.running, status = chat.status, blocked = chat.blocked,
                trailing = branch, onLongClick = { onMenu(entry) }, longClickLabel = "Chat menu", onMore = { onMenu(entry) }, moreLabel = "Menu of ${chat.title}",
            ) { app.openOn(entry.target, chat = chat.id); onPicked() }
            side.children(entry).takeIf { it.isNotEmpty() }?.let { subs ->
                Column(Modifier.padding(start = 16.dp)) {
                    for (sub in subs) NavRow(
                        sub.chat.title, Icons.subchat, selected = app.isOpen(sub), running = sub.chat.running,
                        onLongClick = { onMenu(sub) }, longClickLabel = "Chat menu", onMore = { onMenu(sub) }, moreLabel = "Menu of ${sub.chat.title}",
                    ) { app.openOn(sub.target, chat = sub.chat.id); onPicked() }
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
