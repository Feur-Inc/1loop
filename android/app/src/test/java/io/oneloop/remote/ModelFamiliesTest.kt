package io.oneloop.remote

import org.junit.Assert.assertEquals
import org.junit.Test

class ModelFamiliesTest {
    private fun model(label: String, family: String = "") = ModelOption(label, label, "", "a", emptyList(), family = family)

    @Test
    fun groupsByFamilyInTheComputersOrder() {
        val options = listOf(
            model("gpt-5.5", "GPT"), model("gpt-5.4", "GPT"), model("o3", "o-series"),
            model("claude-opus-5", "Claude Opus"), model("claude-opus-4.1", "Claude Opus"), model("gemini-3-pro", "Gemini"),
        )
        val groups = modelFamilies(options)
        assertEquals(listOf("GPT", "o-series", "Claude Opus", "Gemini"), groups.map { it.first })
        assertEquals(listOf("claude-opus-5", "claude-opus-4.1"), groups[2].second.map { it.label })
    }

    @Test
    fun oneFamilyOrAnOlderComputerShowsNoCaption() {
        val one = listOf(model("opus", "Claude Opus"), model("opus[1m]", "Claude Opus"))
        assertEquals(listOf("" to one), modelFamilies(one))
        val older = listOf(model("b"), model("a"))
        assertEquals(listOf("" to older), modelFamilies(older))
        assertEquals(listOf("" to emptyList<ModelOption>()), modelFamilies(emptyList()))
    }
}
