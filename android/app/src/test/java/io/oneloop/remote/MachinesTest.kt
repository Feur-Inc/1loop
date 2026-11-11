// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import org.json.JSONArray
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class MachinesTest {
    private val machines = parseMachines(JSONArray("""[{"id":"ssh-1","label":"Host","projects":"~/dev"},{"label":"no id"},{"id":"ssh-2"}]"""))

    @Test
    fun readsTheServersMachines() {
        assertEquals(listOf(SshMachine("ssh-1", "Host", "~/dev"), SshMachine("ssh-2", "ssh-2", "")), machines)
        assertEquals(emptyList<SshMachine>(), parseMachines(null))
    }

    @Test
    fun namesTheMachineOfAProject() {
        val projectMachines = mapOf("/home/mopi/dev/app" to "ssh-1", "/home/mopi/dev/gone" to "ssh-9")
        assertEquals("Host", projectMachine(machines, projectMachines, "/home/mopi/dev/app")?.label)
        assertNull(projectMachine(machines, projectMachines, "/projects/app"))
        assertNull(projectMachine(machines, projectMachines, null))
        assertEquals("a removed SSH machine", projectMachine(machines, projectMachines, "/home/mopi/dev/gone")?.label)
    }
}
