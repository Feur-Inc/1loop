// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.input.TextFieldValue
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import org.json.JSONObject

// "@" mentions in the composer, as on the desktop: typing "@" at the start of
// a word offers the tools and MCP servers the chat can name, asked of the
// computer; the model is told about the ones a message names.

/** A name offered after "@": a tool (browser_use…) or an MCP server. */
data class Mention(val name: String, val label: String, val about: String) {
    companion object {
        fun list(o: JSONObject): List<Mention> {
            val array = o.optJSONArray("mentions") ?: return emptyList()
            return List(array.length()) { i -> array.getJSONObject(i).let { Mention(it.optString("name"), it.optString("label"), it.optString("about")) } }
        }
    }
}

/** The mention being typed at the cursor: the "@"'s place and what follows it. */
internal fun mentionTyped(value: TextFieldValue): Pair<Int, String>? {
    if (!value.selection.collapsed) return null
    val text = value.text
    val cursor = value.selection.start.coerceIn(0, text.length)
    var start = cursor
    while (start > 0 && mentionChar(text[start - 1])) start--
    if (start == 0 || text[start - 1] != '@') return null
    val at = start - 1
    if (at > 0 && !text[at - 1].isWhitespace() && text[at - 1] !in "([{\"'") return null // an e-mail address
    return at to text.substring(start, cursor)
}

private fun mentionChar(c: Char) = c == '_' || c == '-' || c.isLetterOrDigit()

/** Names that start with what is typed first, then names that contain it. */
internal fun matchMentions(all: List<Mention>, typed: String): List<Mention> {
    val t = typed.lowercase()
    return all.filter { it.name.startsWith(t) } + all.filter { !it.name.startsWith(t) && it.name.contains(t) }
}

/** The text with the chosen mention in place of the one being typed. */
internal fun completeMention(value: TextFieldValue, at: Int, name: String): TextFieldValue {
    val text = value.text
    var end = at + 1
    while (end < text.length && mentionChar(text[end])) end++
    if (end < text.length && text[end] == ' ') end++ // the space after it is already there
    val inserted = "@$name "
    return TextFieldValue(text.substring(0, at) + inserted + text.substring(end), TextRange(at + inserted.length))
}

/** The matching mentions over the composer; null while they are asked. */
@Composable
internal fun MentionTray(matches: List<Mention>?, onPick: (Mention) -> Unit) {
    Column(
        Modifier.widthIn(max = 728.dp).fillMaxWidth().background(Palette.popover, RoundedCornerShape(topStart = 12.dp, topEnd = 12.dp))
            .border(1.dp, Palette.borderSoft, RoundedCornerShape(topStart = 12.dp, topEnd = 12.dp)).padding(horizontal = 6.dp, vertical = 6.dp),
        verticalArrangement = Arrangement.spacedBy(2.dp),
    ) {
        if (matches == null) Text("Loading…", style = Type.small.copy(color = Palette.faint), modifier = Modifier.padding(horizontal = 8.dp, vertical = 6.dp))
        matches?.take(6)?.forEach { mention ->
            Row(
                Modifier.fillMaxWidth().clip(RoundedCornerShape(8.dp)).clickable { onPick(mention) }.padding(horizontal = 8.dp, vertical = 8.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text("@" + mention.name, style = Type.small.copy(color = Palette.text), maxLines = 1)
                Text(
                    mention.about, style = Type.caption.copy(color = Palette.muted), maxLines = 1, overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f).padding(start = 10.dp),
                )
            }
        }
    }
}
