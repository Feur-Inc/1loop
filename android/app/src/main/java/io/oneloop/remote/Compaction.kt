// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.CancellationException
import org.json.JSONObject
import java.time.ZoneId
import java.time.format.DateTimeFormatter

// A compaction compared (#697, the desktop's #128): the context before it,
// each message abbreviated, and after it, the summary whole. The note in
// the conversation names the compaction ("~120000 → ~18000 tokens"); the
// chat keeps its last three (GET /api/chats/{id}/compactions).

/** A message of the context, abbreviated (agent.CompactedMessage). */
data class CompactedMessage(val role: String, val text: String, val tokens: Int)

/** One compaction (agent.CompactionRecord). */
data class CompactionRecord(val at: String, val before: Int, val after: Int, val count: Int, val was: List<CompactedMessage>, val now: List<CompactedMessage>) {
    /** What the conversation's note says of it, which finds it (agent.CompactionRecord.Label). */
    val label get() = "~$before → ~$after tokens"

    companion object {
        private fun messages(o: JSONObject, key: String) = o.optJSONArray(key).let { array ->
            (0 until (array?.length() ?: 0)).map { i -> array!!.getJSONObject(i).let { CompactedMessage(it.optString("role"), it.optString("text"), it.optInt("tokens")) } }
        }

        fun from(o: JSONObject) = CompactionRecord(
            o.optString("at"), o.optInt("before"), o.optInt("after"), o.optInt("count"), messages(o, "was"), messages(o, "now"),
        )
    }
}

/** A note of the conversation that tells a compaction of 1loop's, which can be compared (Claude Code's own has no numbers). */
fun compactionNote(item: Item) = item.kind == "note" && item.text.startsWith("Context compacted") && item.text.contains(" → ~")

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun CompactionSheet(app: RemoteApp, chat: String, note: String, onDone: () -> Unit) {
    var record by remember(note) { mutableStateOf<CompactionRecord?>(null) }
    var problem by remember(note) { mutableStateOf<String?>(null) }
    var after by remember(note) { mutableStateOf(false) } // the phone shows one side at a time
    LaunchedEffect(chat, note) {
        try {
            record = app.compactions(chat).lastOrNull { note.contains(it.label) }
            if (record == null) problem = "This compaction is no longer kept: a chat keeps its last three."
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (failure: Exception) {
            problem = failure.message ?: "The compaction could not be read."
        }
    }
    GlassBackdrop()
    ModalBottomSheet(onDismissRequest = onDone, containerColor = Palette.surface, scrimColor = GlassScrim, sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)) {
        Column(Modifier.verticalScroll(rememberScrollState()).padding(horizontal = 20.dp).padding(bottom = 24.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Text("Context compaction", style = Type.title)
            problem?.let { Text(it, style = Type.small) }
            val shown = record
            if (shown == null && problem == null) WorkRing("wait", 28.dp)
            if (shown != null) {
                val freed = if (shown.before > 0) 100 - shown.after * 100 / shown.before else 0
                val at = runCatching { java.time.Instant.parse(shown.at).atZone(ZoneId.systemDefault()).format(DateTimeFormatter.ofPattern("d MMMM HH:mm")) }.getOrDefault("")
                Text(
                    listOf(at, "$freed% freed").filter { it.isNotEmpty() }.joinToString(" · ") +
                        ". Before, each message is abbreviated; after, the summary is whole. Everything stays in the conversation and can be recalled.",
                    style = Type.small,
                )
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    Pill("Before · ~${compactTokens(shown.before)}", primary = !after) { after = false }
                    Pill("After · ~${compactTokens(shown.after)}", primary = after) { after = true }
                }
                Text(if (after) "${shown.now.size} messages" else "${shown.count} messages", style = Type.caption)
                for (message in if (after) shown.now else shown.was) {
                    Column(
                        Modifier.fillMaxWidth().background(Palette.code, RoundedCornerShape(10.dp)).border(1.dp, Palette.borderSoft, RoundedCornerShape(10.dp)).padding(12.dp),
                        verticalArrangement = Arrangement.spacedBy(4.dp),
                    ) {
                        val who = mapOf("user" to "You", "assistant" to "Assistant", "tool" to "Tool result", "1loop" to "Summary and notes")[message.role] ?: message.role
                        Text(
                            who + if (message.tokens > 0) " · ~${compactTokens(message.tokens)}" else "",
                            style = Type.caption.copy(color = if (message.role == "1loop") Palette.amber else Palette.muted),
                        )
                        Text(message.text, style = Type.small.copy(color = Palette.text))
                    }
                }
            }
        }
    }
}

/** 950, 12.3k, 1.2M, as core.CompactCount. */
private fun compactTokens(n: Int): String = when {
    n < 1000 -> "$n"
    n < 999_950 -> String.format(java.util.Locale.ROOT, "%.1f", n / 1e3).removeSuffix(".0") + "k"
    else -> String.format(java.util.Locale.ROOT, "%.1f", n / 1e6).removeSuffix(".0") + "M"
}
