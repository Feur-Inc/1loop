// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import androidx.compose.ui.graphics.Color
import java.io.File
import org.json.JSONArray
import org.junit.Assert.assertEquals
import org.junit.Test

// The desktop's accents fitted to each theme (internal/palette/testdata,
// written by its tests): the phone's derive gives the same colours.
class PaletteTest {
    private fun argb(c: Color) =
        "#%02x%02x%02x%02x".format(Math.round(c.alpha * 255), Math.round(c.red * 255), Math.round(c.green * 255), Math.round(c.blue * 255))

    @Test
    fun deriveMatchesTheDesktop() {
        val cases = JSONArray(File("../../internal/palette/testdata/derived.json").readText())
        for (i in 0 until cases.length()) {
            val case = cases.getJSONObject(i)
            val tokens = if (case.getString("theme") == "light") PaletteTokens.light else PaletteTokens.dark
            val got = derive(tokens, parseHex(case.getString("input"))!!)
            val what = "${case.getString("theme")} ${case.getString("input")}"
            assertEquals(what, case.getString("accent"), argb(got.accent))
            assertEquals(what, case.getString("accentHot"), argb(got.accentHot))
            assertEquals(what, case.getString("onAccent"), argb(got.onAccent))
            assertEquals(what, case.getString("link"), argb(got.link))
            assertEquals(what, case.getString("focus"), argb(got.focus))
            assertEquals(what, case.getString("selection"), argb(got.selection))
            assertEquals(what, case.getString("ring"), argb(got.ring))
        }
    }

    @Test
    fun readsTheSetting() {
        assertEquals(PaletteTokens.accents.first { it.id == "blue" }.color, accentColor("blue"))
        assertEquals(PaletteTokens.accents.first().color, accentColor("nonsense"))
        assertEquals("#123456", hex(accentColor("#123456")))
    }
}
