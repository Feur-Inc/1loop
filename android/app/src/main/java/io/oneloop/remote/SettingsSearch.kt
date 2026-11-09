// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import android.content.Context
import org.json.JSONObject

// The settings' list and its search, the same as the desktop's. The list is
// internal/settings in Go, copied into assets/settings.json when it changes
// (a Go test keeps the copy in step); the search is settings/search.go's,
// and SettingsSearchTest runs the same cases.

data class SettingsSection(val id: String, val title: String, val intro: String)

data class SettingEntry(
    val key: String,
    val section: String,
    val title: String,
    val description: String,
    val keywords: List<String>,
    val values: List<String>, // the labels of a choice's values
    val where: String,
)

class SettingsList(val sections: List<SettingsSection>, val settings: List<SettingEntry>) {
    fun sectionTitle(id: String) = sections.firstOrNull { it.id == id }?.title.orEmpty()

    /** The settings the phone's page shows. */
    fun onPhone() = settings.filter { it.where == "both" || it.where == "phone" }

    fun onDesktop() = settings.filter { it.where == "both" || it.where == "desktop" }

    companion object {
        fun parse(text: String): SettingsList {
            val json = JSONObject(text)
            val sections = json.getJSONArray("sections").let { array ->
                (0 until array.length()).map { i ->
                    val section = array.getJSONObject(i)
                    SettingsSection(section.getString("id"), section.getString("title"), section.optString("intro"))
                }
            }
            val settings = json.getJSONArray("settings").let { array ->
                (0 until array.length()).map { i ->
                    val setting = array.getJSONObject(i)
                    val keywords = setting.optJSONArray("keywords")
                    val values = setting.optJSONArray("values")
                    SettingEntry(
                        setting.getString("key"), setting.getString("section"), setting.getString("title"),
                        setting.optString("description"),
                        if (keywords == null) emptyList() else (0 until keywords.length()).map { keywords.getString(it) },
                        if (values == null) emptyList() else (0 until values.length()).map { values.getJSONObject(it).optString("label") },
                        setting.optString("where", "both"),
                    )
                }
            }
            return SettingsList(sections, settings)
        }

        @Volatile private var loaded: SettingsList? = null

        fun load(context: Context): SettingsList = loaded ?: parse(
            context.assets.open("settings.json").bufferedReader().use { it.readText() },
        ).also { loaded = it }
    }
}

object SettingsSearch {
    // How much a word found in each part of a setting counts.
    private const val IN_TITLE = 10
    private const val IN_KEYWORDS = 7
    private const val IN_SECTION = 3
    private const val IN_DESCRIPTION = 2

    // Words too common to say anything ("turn off the badge").
    private val stopWords = setOf(
        "a", "an", "the", "of", "to", "for", "in", "on", "off", "my", "and", "or", "is", "how",
        "do", "i", "turn", "set", "change", "make", "enable", "disable",
    )

    /** The settings matching a query, best first; a blank query matches none. */
    fun search(list: SettingsList, settings: List<SettingEntry>, query: String): List<SettingEntry> {
        var words = words(query)
        words.filterNot { it in stopWords }.takeIf { it.isNotEmpty() }?.let { words = it }
        if (words.isEmpty()) return emptyList()
        val phrase = words.joinToString(" ")
        // sortedByDescending is stable: equal scores keep the page's order.
        return settings.map { it to score(list, it, words, phrase) }.filter { it.second > 0 }.sortedByDescending { it.second }.map { it.first }
    }

    private fun score(list: SettingsList, setting: SettingEntry, words: List<String>, phrase: String): Int {
        val title = words(setting.title)
        val keywords = words(setting.keywords.joinToString(" ")) + setting.values.flatMap { words(it) }
        val section = words(list.sectionTitle(setting.section))
        val description = words(setting.description)
        var total = 0
        for (word in words) {
            val best = maxOf(
                IN_TITLE * wordScore(word, title), IN_KEYWORDS * wordScore(word, keywords),
                IN_SECTION * wordScore(word, section), IN_DESCRIPTION * wordScore(word, description),
            )
            if (best == 0) return 0 // every word must be somewhere
            total += best
        }
        // The title itself, typed whole or from its start, comes first.
        val whole = title.joinToString(" ")
        total += when {
            whole == phrase -> 400
            whole.startsWith(phrase) -> 200
            whole.contains(phrase) -> 100
            else -> 0
        }
        return total
    }

    private fun wordScore(typed: String, words: List<String>) = words.maxOfOrNull { matchWord(typed, it) } ?: 0

    private fun matchWord(typed: String, word: String): Int {
        if (typed == word) return 10
        if (word.startsWith(typed)) return 8
        val allowed = typos(typed.length)
        if (allowed == 0) return 0
        // The whole word misspelled, or its start while it is being typed.
        var distance = editDistance(typed, word)
        if (word.length > typed.length) distance = minOf(distance, editDistance(typed, word.take(typed.length)))
        return if (distance > allowed) 0 else 6 - 2 * distance
    }

    // None in a short word, where any word would be one or two away.
    private fun typos(length: Int) = when {
        length >= 8 -> 2
        length >= 4 -> 1
        else -> 0
    }

    /** The letters to add, remove, change or swap with the next one to go from a to b. */
    fun editDistance(a: String, b: String): Int {
        val rows = Array(a.length + 1) { i -> IntArray(b.length + 1) { j -> if (i == 0) j else if (j == 0) i else 0 } }
        for (i in 1..a.length) for (j in 1..b.length) {
            val cost = if (a[i - 1] == b[j - 1]) 0 else 1
            rows[i][j] = minOf(rows[i - 1][j] + 1, rows[i][j - 1] + 1, rows[i - 1][j - 1] + cost)
            if (i > 1 && j > 1 && a[i - 1] == b[j - 2] && a[i - 2] == b[j - 1]) rows[i][j] = minOf(rows[i][j], rows[i - 2][j - 2] + 1)
        }
        return rows[a.length][b.length]
    }

    /** Lower-case words of letters and digits. */
    fun words(text: String): List<String> = text.lowercase().split(Regex("[^\\p{L}\\p{Nd}]+")).filter { it.isNotEmpty() }
}
