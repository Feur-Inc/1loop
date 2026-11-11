// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class NextStepTest {
    private fun tool(name: String, args: String, state: String = "done", failed: String = "") =
        Item("tool", "", 0, name, "", "", false, "", "", state, args, "", 0, failed)
    private fun message(kind: String, text: String) = Item(kind, text, 0, "", "", "", false, "", "", "done", "", "", 0, "")

    private val call = """{"recap":"Added the card.","ask":"Open the PR?","actions":[{"label":"Open it","message":"Open the PR into dev"},{"label":"Not yet","message":null}],"blocked":true}"""

    @Test
    fun readsTheCall() {
        val wrap = WrapUp.from(tool("wrap_up", call))!!
        assertTrue(wrap.card && wrap.blocked)
        assertEquals("Open the PR into dev", wrap.actions[0].reply)
        assertEquals("Not yet", wrap.actions[1].reply) // no message: the label
        assertEquals("Not yet", wrap.chosen(" Not yet ")?.label)
        assertNull(wrap.chosen("something else"))
        assertFalse(WrapUp.from(tool("wrap_up", """{"recap":"Done.","ask":null,"actions":null,"blocked":null}"""))!!.card)
        assertNull(WrapUp.from(tool("wrap_up", "{}", failed = "recap is required")))
    }

    @Test
    fun liveUntilTheUserAnswers() {
        val turn = listOf(message("user", "go"), tool("mcp__1loop_todo__wrap_up", call), message("assistant", "Done, see above."))
        assertEquals("Open the PR?", WrapUp.live(turn)?.ask)
        assertNull(WrapUp.live(listOf(message("user", "go"), tool("wrap_up", call, state = "running"))))
        val answered = turn + message("user", "Open the PR into dev")
        assertNull(WrapUp.live(answered))
        assertEquals("Open it", WrapUp.answered(answered, 3)?.chosen(answered[3].text)?.label)
        assertNull(WrapUp.answered(answered, 0))
    }
}
