// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import java.io.File
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

// The guides the app bundles (#443): the desktop's, read the same way.
class ServerSetupTest {
    private val methods = serverSetupMethods(File("src/main/assets/server_setup.json").readText())

    @Test
    fun fourWaysEachWithSteps() {
        assertEquals(listOf("docker", "compose", "linux", "windows"), methods.map { it.id })
        for (method in methods) {
            assertTrue(method.id, method.steps.isNotEmpty())
            for (step in method.steps) {
                assertTrue(method.id, step.text.isNotBlank())
                step.link?.let { assertTrue(it.url, it.url.startsWith("https://")) }
            }
        }
    }

    @Test
    fun dockerStartsTheServerImage() {
        val docker = methods.first { it.id == "docker" }
        assertTrue(docker.steps.any { "ghcr.io/feur-inc/1loop-server" in it.command })
        assertTrue(docker.steps.any { it.command == "docker attach 1loop-server" })
    }
}
