// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import org.json.JSONArray

/**
 * An SSH machine of the headless server: a project can be a folder of it, so
 * the agent's commands, files and terminals run there (docs/SERVER.md).
 */
data class SshMachine(val id: String, val label: String, val projects: String)

internal fun parseMachines(array: JSONArray?): List<SshMachine> =
    (0 until (array?.length() ?: 0)).mapNotNull { index ->
        val machine = array!!.optJSONObject(index) ?: return@mapNotNull null
        val id = machine.optString("id")
        if (id.isEmpty()) null else SshMachine(id, machine.optString("label").ifEmpty { id }, machine.optString("projects"))
    }

/** The machine a project (or one of its worktrees) is on, null for the target's own folders. */
internal fun projectMachine(machines: List<SshMachine>, projectMachines: Map<String, String>, project: String?): SshMachine? {
    val id = projectMachines[project ?: return null] ?: return null
    return machines.firstOrNull { it.id == id } ?: SshMachine(id, "a removed SSH machine", "")
}
