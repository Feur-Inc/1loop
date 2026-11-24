// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp

// The drawer's Search, as the desktop's (internal/ui/sidebar_search.go): one
// field and the tags All / Chats / Settings. A chat is found by its title (the
// phone holds the chats' messages only for the chat that is open), a setting
// by the same search as Settings' own. A result opens it.

/** Search shows this many chats at most: past that, more words narrow it. */
private const val SEARCH_LIMIT = 50

private enum class SearchTag(val label: String) { All("All"), Chats("Chats"), Settings("Settings") }

@Composable
fun SearchScreen(app: RemoteApp, onBack: () -> Unit) {
    val context = LocalContext.current
    val list = remember { SettingsList.load(context) }
    var query by rememberSaveable { mutableStateOf("") }
    var tag by rememberSaveable { mutableStateOf(SearchTag.All) }
    val back = predictiveBack(onBack = onBack)
    val local = app.server?.localPhone == true
    val adb = if (local) app.local.adb.collectAsState().value else null
    val settings = phoneSettingsShown(app, list, adb?.let { it.allowed && it.connected } == true)
    val words = query.lowercase().split(Regex("\\s+")).filter { it.isNotEmpty() }
    // Title matches, in the chats' order; with no word, every chat.
    val chats = app.chats.filter { chat -> words.all { chat.title.lowercase().contains(it) } }
    val found = if (query.isBlank()) emptyList() else SettingsSearch.search(list, settings, query)
    Column(back.fillMaxSize().background(Palette.background).windowInsetsPadding(WindowInsets.safeDrawing)) {
        Row(Modifier.padding(12.dp), verticalAlignment = Alignment.CenterVertically) {
            RoundButton(Icons.back, Color.Transparent, Palette.muted, description = "Back", onClick = onBack)
            Text("Search", style = Type.title, modifier = Modifier.padding(start = 12.dp))
        }
        Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(horizontal = 16.dp, vertical = 4.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Field(query, { query = it }, "Search chats and settings", KeyboardType.Text, Modifier.fillMaxWidth())
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                for (option in SearchTag.entries) {
                    Text(
                        option.label, style = Type.small.copy(color = if (tag == option) Palette.text else Palette.muted), maxLines = 1,
                        modifier = Modifier.clip(RoundedCornerShape(8.dp)).background(if (tag == option) Palette.selected else Color.Transparent)
                            .clickable { tag = option }.padding(horizontal = 12.dp, vertical = 7.dp),
                    )
                }
            }
            val shownChats = if (tag == SearchTag.Settings) emptyList() else chats.take(SEARCH_LIMIT)
            val shownSettings = if (tag == SearchTag.Chats) emptyList() else found
            if (shownChats.isEmpty() && shownSettings.isEmpty()) {
                Column(Modifier.fillMaxWidth().background(Palette.surface, RoundedCornerShape(14.dp)).padding(16.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    Text(if (query.isBlank()) "Nothing here yet" else "Nothing matches “${query.trim()}”", style = Type.body)
                    Text("Try another word: a chat's title, or a setting's name or what it does.", style = Type.small)
                }
            }
            if (shownChats.isNotEmpty()) {
                SearchHeading("Chats")
                Column(Modifier.fillMaxWidth().clip(RoundedCornerShape(14.dp)).background(Palette.surface)) {
                    for (chat in shownChats) {
                        SearchRow(
                            if (chat.parent.isNotEmpty()) Icons.subchat else Icons.chats, chat.title,
                            app.folderName(chat.workspace.takeIf { !chat.scratch } ?: NO_FOLDER), running = chat.running,
                        ) { app.searchOpen = false; app.open(chat.id) }
                    }
                }
                if (chats.size > SEARCH_LIMIT) Text("${chats.size - SEARCH_LIMIT} more: type more words to narrow it.", style = Type.caption, modifier = Modifier.padding(horizontal = 4.dp))
            }
            if (shownSettings.isNotEmpty()) {
                SearchHeading("Settings")
                Column(Modifier.fillMaxWidth().clip(RoundedCornerShape(14.dp)).background(Palette.surface)) {
                    for (setting in shownSettings) {
                        SearchRow(Icons.settings, setting.title, "${list.sectionTitle(setting.section)} · ${setting.description}") {
                            app.searchOpen = false
                            app.settingsSection = setting.section
                            app.settingsReveal = setting.key
                            app.settingsOpen = true
                        }
                    }
                }
            }
            Spacer(Modifier.height(24.dp))
        }
    }
}

@Composable
private fun SearchHeading(text: String) {
    Text(text, style = Type.small.copy(color = Palette.faint), modifier = Modifier.padding(start = 4.dp))
}

@Composable
private fun SearchRow(icon: androidx.compose.ui.graphics.vector.ImageVector, title: String, detail: String, running: Boolean = false, onClick: () -> Unit) {
    Row(Modifier.fillMaxWidth().clickable(onClick = onClick).padding(horizontal = 16.dp, vertical = 12.dp), verticalAlignment = Alignment.CenterVertically) {
        Icon(icon, null, tint = Palette.muted, modifier = Modifier.size(20.dp))
        Spacer(Modifier.width(14.dp))
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Text(title, style = Type.body, maxLines = 1, overflow = TextOverflow.Ellipsis)
            if (detail.isNotEmpty()) Text(detail, style = Type.small, maxLines = 2, overflow = TextOverflow.Ellipsis)
        }
        if (running) Box(Modifier.padding(start = 8.dp).size(7.dp).clip(androidx.compose.foundation.shape.CircleShape).background(Palette.amber))
    }
}
