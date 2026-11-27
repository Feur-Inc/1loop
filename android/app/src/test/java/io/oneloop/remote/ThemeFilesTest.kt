// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import java.io.File
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test

// The phone reads the desktop's theme files (#704): docs/themes' examples,
// the colours left out derived from the ones set, and the errors named.
class ThemeFilesTest {
    @Test
    fun readsTheExamples() {
        for (name in listOf("nord", "paper", "aurora", "drift")) {
            val theme = ThemeFiles.parse(name, File("../../docs/themes/$name.toml").readText())
            assertTrue(name, theme.name.isNotEmpty())
        }
    }

    @Test
    fun derivesWhatIsLeftOut() {
        val theme = ThemeFiles.parse("t", """
            # SPDX-License-Identifier: MPL-2.0
            name = "Test # not a comment"
            base = "dark"
            accents = ["#88c0d0", "#a3be8c"] # two
            [colors]
            background = "#2e3440"
            text = "#eceff4"
            danger = "#bf616a80"
        """.trimIndent())
        assertEquals("Test # not a comment", theme.name)
        assertEquals(listOf(parseHex("#88c0d0"), parseHex("#a3be8c")), theme.accents)
        assertEquals(parseHex("#2e3440"), theme.tokens.background)
        assertEquals(0x80 / 255f, theme.tokens.danger.alpha, 0.002f)
        // The surfaces follow the background, not the dark base's.
        assertNotEquals(PaletteTokens.dark.surface, theme.tokens.surface)
        assertNotEquals(PaletteTokens.dark.muted, theme.tokens.muted)
    }

    @Test
    fun namesTheErrors() {
        for ((text, error) in listOf(
            "base = \"blue\"" to "base",
            "[colors]\nbackgroud = \"#000000\"" to "colors.backgroud",
            "accents = [\"red\"]" to "accents[0]",
            "name" to "line 1",
        )) {
            try {
                ThemeFiles.parse("t", text)
                fail("$text was read")
            } catch (expected: IllegalStateException) {
                assertTrue("${expected.message} for $text", expected.message!!.contains(error))
            } catch (expected: IllegalArgumentException) {
                assertTrue("${expected.message} for $text", expected.message!!.contains(error))
            }
        }
    }
}
