// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import org.junit.Assert.assertEquals
import org.junit.Test

class ChatSubtreeTest {
    private fun chat(id: String, parent: String = "") = ChatSummary(id, id, "/p", running = false, parent = parent)

    @Test
    fun deletingAParentTakesEveryLevelUnderIt() {
        val chats = listOf(
            chat("root"), chat("a", "root"), chat("b", "root"), chat("a1", "a"), chat("a1x", "a1"),
            chat("other"), chat("o1", "other"),
        )
        assertEquals(listOf("a", "b", "a1", "a1x"), chatSubtree(chats, "root"))
        assertEquals(listOf("a1x"), chatSubtree(chats, "a1"))
        assertEquals(emptyList<String>(), chatSubtree(chats, "a1x"))
    }

    @Test
    fun aLoopOfLinksEnds() {
        val chats = listOf(chat("x", "y"), chat("y", "x"), chat("self", "self"))
        assertEquals(listOf("y"), chatSubtree(chats, "x"))
        assertEquals(emptyList<String>(), chatSubtree(chats, "self"))
    }

    @Test
    fun theConfirmationCountsThem() {
        assertEquals("", subtreeNote(0))
        assertEquals(" Its subchat will be deleted with it.", subtreeNote(1))
        assertEquals(" Its 4 subchats, at every level, will be deleted with it.", subtreeNote(4))
    }
}
