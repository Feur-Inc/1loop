// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.material3.ripple
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp

// The settings' home and search, as on the desktop (settings_search.go,
// the same sections, setting names and words): typing filters the settings
// live, by their name, description, section and the words people use for
// them; a result opens its section on the setting.

/** A section of the settings, with what it holds in a few words. */
data class SettingsSection(val name: String, val icon: ImageVector, val summary: String)

/** A setting the search finds: its title on its section's page, a short description and other words for it. */
data class SettingEntry(val section: String, val title: String, val detail: String, val words: String)

/** The sections this phone shows, in the desktop's order; "This phone" holds what only the phone has. */
fun settingsSections(app: RemoteApp): List<SettingsSection> {
    val local = app.server?.localPhone == true
    return listOf(
        SettingsSection("Execution target", Icons.terminal, app.server?.name?.let { "Now: $it" } ?: "Where the agent works"),
        SettingsSection("Connections", Icons.plug, "Accounts and models"),
        SettingsSection("Agent", Icons.sliders, "Permissions, rate limits, context, hooks"),
        SettingsSection("Instructions", Icons.document, "Your preferences and memory"),
        SettingsSection("Browser", Icons.globe, "Browsing data of the agent's browser"),
        SettingsSection("Extensions", Icons.puzzle, "Skills, plugins, MCP servers, GitHub"),
        SettingsSection("This phone", Icons.phone, if (local) "Phone access, sharing, Linux, status while running" else "Status while a model runs"),
        SettingsSection("Updates", Icons.refresh, "Version and updates"),
    )
}

val settingsIndex = listOf(
    SettingEntry("Execution target", "Execution target", "Where the agent works: this phone, a computer or a server.", "switch target server computer desktop remote"),
    SettingEntry("Connections", "Connections", "Accounts and services whose models the agent uses.", "accounts models api key provider chatgpt claude code openrouter vllm"),
    SettingEntry("Connections", "Default model preset", "The models a new chat starts with.", "default model preset"),
    SettingEntry("Agent", "Tool permissions", "Approval, YOLO or Auto: what the agent may do without asking.", "approval yolo auto mode sandbox permissions approve ask"),
    SettingEntry("Agent", "Automatic resumption", "What happens when a conversation reaches a rate limit.", "rate limit quota wait fallback model resume"),
    SettingEntry("Agent", "Context compaction", "When a conversation is compacted, for each model.", "context compaction summary tokens compact"),
    SettingEntry("Agent", "Hooks", "Your commands run at the agent's events.", "hooks scripts events"),
    SettingEntry("Instructions", "Your preferences", "What the agent should know about you and how to work.", "personalization language tone instructions custom prompt"),
    SettingEntry("Instructions", "Memory", "Let the agent add what it learns about you to your preferences.", "remember learn"),
    SettingEntry("Browser", "Browsing data", "Cookies, logins, cache and history of the agent's browser.", "browser cookies cache history clear logins browser use"),
    SettingEntry("Extensions", "Extensions", "Skills, plugins and MCP servers.", "skills plugins mcp servers store install tools"),
    SettingEntry("Extensions", "GitHub connector", "Let the agent use your GitHub repositories.", "github repositories connector token"),
    SettingEntry("This phone", "While a model runs", "What shows over your other apps while the agent works.", "badge overlay notification status live update"),
    SettingEntry("This phone", "Phone access", "Let the agent use this phone (Phone Use), through Shizuku.", "shizuku adb phone use access"),
    SettingEntry("This phone", "Work profile", "Let the agent reach the apps of your work profile.", "work profile apps"),
    SettingEntry("This phone", "Background execution", "Keep the agent running when 1loop is in the background.", "background battery doze"),
    SettingEntry("This phone", "Share this phone", "Let a computer or another device use this phone.", "share sharing pair devices"),
    SettingEntry("This phone", "Linux", "The Debian the local agent works in.", "linux debian terminal packages storage reset"),
    SettingEntry("Updates", "Updates", "Check for a new version and install it.", "update version upgrade release install"),
)

/** What matches [query], best first: every word must appear in the entry; a title that starts with the first word ranks first. */
fun searchSettings(query: String, sections: Set<String>): List<SettingEntry> {
    val words = query.lowercase().split(Regex("\\s+")).filter { it.isNotEmpty() }
    if (words.isEmpty()) return emptyList()
    return settingsIndex.filter { it.section in sections }.mapNotNull { entry ->
        val title = entry.title.lowercase()
        val haystack = "$title ${entry.detail} ${entry.section} ${entry.words}".lowercase()
        if (words.any { it !in haystack }) return@mapNotNull null
        val first = words.first()
        entry to when {
            title.startsWith(first) -> 3
            first in title -> 2
            first in entry.section.lowercase() -> 1
            else -> 0
        }
    }.sortedByDescending { it.second }.map { it.first }
}

/** The settings' home: the search field, then the sections, or what matches the search. */
@Composable
fun SettingsHome(app: RemoteApp, query: String, onQuery: (String) -> Unit, onOpen: (String, String?) -> Unit) {
    val sections = settingsSections(app)
    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(horizontal = 16.dp, vertical = 8.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Row(
            Modifier.fillMaxWidth().background(Palette.surface, RoundedCornerShape(14.dp)).border(1.dp, Palette.borderSoft, RoundedCornerShape(14.dp))
                .padding(horizontal = 14.dp, vertical = 12.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Icon(Icons.search, null, tint = Palette.muted, modifier = Modifier.size(18.dp))
            Spacer(Modifier.width(10.dp))
            Box(Modifier.weight(1f)) {
                if (query.isEmpty()) Text("Search settings", style = Type.body.copy(color = Palette.faint))
                BasicTextField(query, onQuery, singleLine = true, textStyle = Type.body, cursorBrush = SolidColor(Palette.amber), modifier = Modifier.fillMaxWidth())
            }
            if (query.isNotEmpty()) RoundButton(Icons.close, Palette.raised, Palette.muted, description = "Clear the search", size = 28.dp, iconSize = 14.dp) { onQuery("") }
        }
        if (query.isBlank()) {
            Column(Modifier.fillMaxWidth().clip(RoundedCornerShape(14.dp)).background(Palette.surface)) {
                for (section in sections) SettingsRow(section.icon, section.name, section.summary) { onOpen(section.name, null) }
            }
            return@Column
        }
        val matches = searchSettings(query, sections.map { it.name }.toSet())
        if (matches.isEmpty()) {
            Column(Modifier.fillMaxWidth().background(Palette.surface, RoundedCornerShape(14.dp)).padding(16.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Text("No setting matches “${query.trim()}”", style = Type.body)
                Text("Try another word, such as server, model, phone or browser, or clear the search to see every section.", style = Type.small)
            }
        } else {
            Column(Modifier.fillMaxWidth().clip(RoundedCornerShape(14.dp)).background(Palette.surface)) {
                for (match in matches) {
                    val icon = sections.firstOrNull { it.name == match.section }?.icon ?: Icons.settings
                    SettingsRow(icon, match.title, "${match.section} · ${match.detail}") { onOpen(match.section, match.title) }
                }
            }
        }
    }
}

/** A row that opens a page: its icon, title, one line saying what is there, and a chevron. */
@Composable
private fun SettingsRow(icon: ImageVector, title: String, summary: String, onClick: () -> Unit) {
    val press = remember { MutableInteractionSource() }
    Row(
        Modifier.fillMaxWidth().clickable(press, ripple(), onClick = onClick).padding(horizontal = 16.dp, vertical = 14.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(icon, null, tint = Palette.muted, modifier = Modifier.size(20.dp))
        Spacer(Modifier.width(14.dp))
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Text(title, style = Type.body)
            Text(summary, style = Type.small, maxLines = 2, overflow = TextOverflow.Ellipsis)
        }
        Icon(Icons.chevronRight, null, tint = Palette.faint, modifier = Modifier.size(18.dp))
    }
}
