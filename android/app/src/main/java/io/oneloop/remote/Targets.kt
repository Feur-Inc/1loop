// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import org.json.JSONArray
import org.json.JSONObject

// Projects belong to a target (#359, #380), as on the desktop
// (internal/ui/targets.go): this phone, a paired desktop or a 1loop server.
// The phone links targets instead of picking one: the drawer lists every
// linked target's projects together, those of another target than this
// phone marked with its icon, and a target that does not answer stays
// listed, marked offline, with the projects it had last time.
// One target is open at a time (the app's connection): opening a project
// or a chat of another target opens that target first.

/** The kind of place a project lives on. */
enum class TargetKind { Phone, Desktop, Server }

/** The key of this phone's own target: the local engine's [Server.key]. */
const val PHONE_TARGET = "local-phone"

val Server.targetKind: TargetKind get() = when {
    localPhone -> TargetKind.Phone
    desktop -> TargetKind.Desktop
    else -> TargetKind.Server
}

/** A target the phone is linked to: this phone once its engine ran, or a paired desktop or server. */
data class LinkedTarget(val key: String, val name: String, val kind: TargetKind)

/**
 * The linked targets in the drawer's order: this phone first (the device in
 * use, as the desktop lists this computer first), then the paired desktops,
 * then the servers, each in the order they were paired.
 */
fun linkedTargets(phoneLinked: Boolean, phoneName: String, servers: List<Server>): List<LinkedTarget> {
    val paired = servers.filter { it.paired && !it.localPhone }.distinctBy { it.key }
    return listOfNotNull(LinkedTarget(PHONE_TARGET, phoneName, TargetKind.Phone).takeIf { phoneLinked }) +
        paired.filter { it.desktop }.map { LinkedTarget(it.key, it.name, TargetKind.Desktop) } +
        paired.filter { !it.desktop }.map { LinkedTarget(it.key, it.name, TargetKind.Server) }
}

/** Whether a target answers, as last seen. */
enum class Reach { Unknown, Online, Offline }

/** What a target lists of itself for the drawer. */
data class TargetListing(
    val projects: List<String> = emptyList(),
    val chats: List<ChatSummary> = emptyList(),
    val worktrees: Map<String, String> = emptyMap(), // folder → project
)

/**
 * A target as the drawer lists it. known: its chats are at hand (it answered
 * this session); else only the projects it had last time are listed.
 */
data class TargetGroup(
    val target: LinkedTarget,
    val reach: Reach,
    val active: Boolean,
    val projects: List<String>,
    val chats: List<ChatSummary>,
    val known: Boolean,
    val worktrees: Map<String, String> = emptyMap(),
) {
    /** Its projects the drawer lists: not its worktrees, which show under their project. */
    val listed get() = projects.filter { it !in worktrees }

    fun projectOf(folder: String) = worktrees[folder] ?: folder
}

/**
 * The drawer's targets, every linked one, in [linked]'s order. The open
 * target ([active]) shows its live listing once read ([live]); another shows
 * what it last answered ([listings]), else the projects it had the last time
 * it answered ([remembered]). A target with neither a project nor a chat is
 * left out, but for the open one.
 */
fun targetGroups(
    linked: List<LinkedTarget>,
    active: String?,
    live: TargetListing?,
    listings: Map<String, TargetListing>,
    reaches: Map<String, Reach>,
    remembered: Map<String, List<String>>,
): List<TargetGroup> = linked.mapNotNull { target ->
    val isActive = target.key == active
    val listing = if (isActive && live != null) live else listings[target.key]
    val reach = if (isActive && live != null) Reach.Online else reaches[target.key] ?: Reach.Unknown
    val group = if (listing != null) {
        TargetGroup(target, reach, isActive, listing.projects, listing.chats, true, listing.worktrees)
    } else {
        TargetGroup(target, reach, isActive, remembered[target.key].orEmpty(), emptyList(), false)
    }
    group.takeIf { isActive || it.projects.isNotEmpty() || it.chats.isNotEmpty() }
}

/** The small icon after the name of a target's projects: none for this phone's. */
fun projectMark(kind: TargetKind): TargetKind? = kind.takeIf { it != TargetKind.Phone }

/** What a project shows, in place of its branch, while its target cannot be reached. */
fun offlineMark(kind: TargetKind) = if (kind == TargetKind.Phone) "stopped" else "offline"

/**
 * Where a new chat with no folder runs: the device in use, this phone, when
 * it is linked; else the open target, else the first linked one. The target
 * chip under the composer moves a new chat elsewhere.
 */
fun noFolderTarget(linked: List<LinkedTarget>, active: String?): String? =
    linked.firstOrNull { it.kind == TargetKind.Phone }?.key
        ?: active?.takeIf { key -> linked.any { it.key == key } }
        ?: linked.firstOrNull()?.key

/**
 * The target the app opens at start: the last one open while it is still
 * linked, else the device in use, else the first linked one; null when
 * nothing is linked (first-time setup). This phone counts only when its
 * engine can start without the setup ([phoneReady]).
 */
fun startTarget(linked: List<LinkedTarget>, last: String?, phoneReady: Boolean): String? {
    val usable = linked.filter { it.kind != TargetKind.Phone || phoneReady }
    return last?.takeIf { key -> usable.any { it.key == key } }
        ?: usable.firstOrNull()?.key
        ?: linked.firstOrNull()?.key
}

/** A chat in the drawer, with the target it is on. */
data class TargetChat(val target: String, val chat: ChatSummary)

/** A group of the drawer: a project of a target, the chats with no folder of every target, or a target's chats no project lists. */
data class DrawerGroup(val key: String, val name: String, val project: String?, val target: TargetGroup?, val chats: List<TargetChat>)

const val SCRATCH_GROUP = ":scratch"
const val OTHER_GROUP = ":other"

/**
 * The drawer's groups, as the desktop's sidebar (renderSidebar): the chats
 * with no folder of every target first, then each target's projects, each
 * with its chats, then that target's chats no project lists. A project's key
 * is its path on this phone (the key it had before targets) and
 * "target|path" on another target, so folds never mix.
 */
fun drawerGroups(groups: List<TargetGroup>, noFolderSelected: Boolean): List<DrawerGroup> {
    val scratch = groups.flatMap { group -> group.chats.filter { it.scratch }.map { TargetChat(group.target.key, it) } }
    val result = mutableListOf<DrawerGroup>()
    if (scratch.isNotEmpty() || noFolderSelected) result += DrawerGroup(SCRATCH_GROUP, "No folder", null, null, scratch)
    for (group in groups) {
        val key = group.target.key
        val rest = group.chats.filter { !it.scratch }
        val listed = group.listed
        for (project in listed) {
            result += DrawerGroup(groupKey(key, project), projectName(project), project, group,
                rest.filter { group.projectOf(it.workspace) == project }.map { TargetChat(key, it) })
        }
        val other = rest.filter { group.projectOf(it.workspace) !in listed }
        if (other.isNotEmpty()) result += DrawerGroup(groupKey(key, OTHER_GROUP), "Other chats", null, group, other.map { TargetChat(key, it) })
    }
    return result
}

/** A drawer group's key on a target: its own on this phone, "target|key" elsewhere. */
fun groupKey(target: String, key: String) = if (target == PHONE_TARGET) key else "$target|$key"

/** The projects each target had the last time it answered, as kept in the preferences. */
fun parseRemembered(text: String?): Map<String, List<String>> = runCatching {
    val json = JSONObject(text ?: return emptyMap())
    json.keys().asSequence().associateWith { key ->
        val array = json.optJSONArray(key) ?: JSONArray()
        List(array.length()) { array.optString(it) }
    }
}.getOrDefault(emptyMap())

fun rememberedText(remembered: Map<String, List<String>>): String =
    JSONObject().apply { remembered.forEach { (key, projects) -> put(key, JSONArray(projects)) } }.toString()
