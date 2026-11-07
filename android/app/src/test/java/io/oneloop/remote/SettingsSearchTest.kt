// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import java.io.File
import org.json.JSONArray
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

// The same cases as the Go search's (internal/settings/testdata), on the
// list the app bundles.
class SettingsSearchTest {
    private val list = SettingsList.parse(File("src/main/assets/settings.json").readText())

    @Test
    fun sharedCases() {
        val cases = JSONArray(File("../../internal/settings/testdata/search_cases.json").readText())
        for (i in 0 until cases.length()) {
            val case = cases.getJSONObject(i)
            val query = case.getString("query")
            val first = case.optString("first")
            var platform = case.optString("platform", "desktop")
            if (platform == "desktop" && first.isNotEmpty() && list.onDesktop().none { it.key == first }) platform = "phone"
            val shown = if (platform == "phone") list.onPhone() else list.onDesktop()
            val keys = SettingsSearch.search(list, shown, query).map { it.key }
            val why = "\"$query\" (${case.optString("why")}): got $keys"
            if (case.optBoolean("none")) {
                assertTrue(why, keys.isEmpty())
                continue
            }
            assertEquals(why, first, keys.firstOrNull())
            case.optJSONArray("also")?.let { also -> for (j in 0 until also.length()) assertTrue(why, also.getString(j) in keys) }
        }
    }

    @Test
    fun editDistance() {
        assertEquals(0, SettingsSearch.editDistance("abc", "abc"))
        assertEquals(1, SettingsSearch.editDistance("permisions", "permissions"))
        assertEquals(1, SettingsSearch.editDistance("permissoins", "permissions"))
        assertEquals(3, SettingsSearch.editDistance("kitten", "sitting"))
        assertEquals(2, SettingsSearch.editDistance("ab", ""))
    }

    @Test
    fun everyPhoneSectionIsKnown() {
        for (setting in list.onPhone()) assertTrue(setting.key, list.sectionTitle(setting.section).isNotEmpty())
    }
}
