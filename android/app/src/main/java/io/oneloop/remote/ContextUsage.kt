package io.oneloop.remote

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.launch
import org.json.JSONObject
import java.util.Locale

// A chat's context window by category, under its task list (#106), as the
// desktop and Claude Code's /context show it: the used share as a bar and a
// percentage, a row per category, the deferred tools apart, and the MCP
// tools in a list that opens (asked from the computer when opened).

/** A row: kind is "used", "free", "buffer" or "deferred" (outside the window). */
data class ContextCategory(val id: String, val name: String, val tokens: Int, val kind: String)

data class ContextTool(val name: String, val server: String, val tokens: Int, val loaded: Boolean)

data class ContextUsage(val used: Int, val window: Int, val categories: List<ContextCategory>, val mcpTools: List<ContextTool>, val mcpToolCount: Int) {
    companion object {
        fun from(o: JSONObject): ContextUsage {
            val categories = o.optJSONArray("categories")
            val tools = o.optJSONArray("mcpTools")
            return ContextUsage(
                o.optInt("used"), o.optInt("window"),
                (0 until (categories?.length() ?: 0)).map { i ->
                    categories!!.getJSONObject(i).let { ContextCategory(it.optString("id"), it.optString("name"), it.optInt("tokens"), it.optString("kind")) }
                },
                (0 until (tools?.length() ?: 0)).map { i ->
                    tools!!.getJSONObject(i).let { ContextTool(it.optString("name"), it.optString("server"), it.optInt("tokens"), it.optBoolean("loaded")) }
                },
                o.optInt("mcpToolCount"),
            )
        }
    }
}

private fun contextColor(category: ContextCategory) = when (category.id) {
    "messages" -> Color(0xFF4C8DFF)
    "system_tools" -> Color(0xFFE8813A)
    "mcp_tools" -> Color(0xFF2FB59B)
    // Greys between the border and the muted text, as on the desktop (contextGrey).
    "autocompact_buffer", "compact_buffer" -> mix(Palette.border, Palette.muted, 0.33)
    "free_space" -> Palette.border
    else -> mix(Palette.border, Palette.muted, if (category.kind == "deferred") 0.17 else 1.15)
}

private fun share(tokens: Int, window: Int) = String.format(Locale.ROOT, "%.1f%%", tokens * 100.0 / window)

@Composable
fun ContextBreakdown(app: RemoteApp, chat: String, usage: ContextUsage) {
    val scope = rememberCoroutineScope()
    var closed by rememberSaveable { mutableStateOf(false) }
    var toolsOpen by rememberSaveable { mutableStateOf(false) }
    var tools by remember(chat) { mutableStateOf<List<ContextTool>?>(null) }
    val window = maxOf(1, usage.window)
    Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
        Row(Modifier.clip(RoundedCornerShape(6.dp)).clickable { closed = !closed }, verticalAlignment = Alignment.CenterVertically) {
            Text("Context", style = Type.small.copy(color = Palette.text))
            Icon(if (closed) Icons.chevronRight else Icons.chevron, null, tint = Palette.muted, modifier = Modifier.size(14.dp))
        }
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text("${minOf(100, usage.used * 100 / window)}% used", style = Type.small.copy(color = Palette.text), modifier = Modifier.weight(1f))
            Text("${tokens(usage.used)} / ${tokens(usage.window)}", style = Type.caption.copy(color = Palette.muted))
        }
        Row(Modifier.fillMaxWidth().height(4.dp).clip(RoundedCornerShape(2.dp)).background(Palette.border)) {
            usage.categories.filter { it.kind == "used" && it.tokens > 0 }.forEach {
                Box(Modifier.weight(it.tokens.toFloat() / window).height(4.dp).background(contextColor(it)))
            }
            val rest = 1f - usage.categories.filter { it.kind == "used" }.sumOf { it.tokens }.toFloat() / window
            if (rest > 0f) Spacer(Modifier.weight(rest))
        }
        if (closed) return@Column
        @Composable
        fun line(category: ContextCategory) {
            val deferred = category.kind == "deferred"
            Row(verticalAlignment = Alignment.CenterVertically) {
                Box(Modifier.size(9.dp).clip(RoundedCornerShape(2.dp)).background(contextColor(category)))
                Text(category.name, style = Type.caption.copy(color = if (deferred) Palette.muted else Palette.text), maxLines = 1, overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f).padding(start = 8.dp))
                Text(tokens(category.tokens), style = Type.caption.copy(color = Palette.muted))
                Text(if (deferred) "—" else share(category.tokens, window), style = Type.caption.copy(color = Palette.muted), textAlign = TextAlign.End,
                    modifier = Modifier.width(48.dp))
            }
        }
        Column(Modifier.padding(top = 4.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            usage.categories.filter { it.kind != "deferred" && (it.tokens > 0 || it.kind != "used") }.forEach { line(it) }
        }
        val deferred = usage.categories.filter { it.kind == "deferred" && it.tokens > 0 }
        if (deferred.isNotEmpty()) Column(Modifier.padding(top = 6.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) { deferred.forEach { line(it) } }
        val count = usage.mcpTools.size + usage.mcpToolCount
        if (count > 0) {
            val list = usage.mcpTools.ifEmpty { null } ?: tools
            Row(Modifier.padding(top = 4.dp).clip(RoundedCornerShape(6.dp)).clickable {
                toolsOpen = !toolsOpen
                if (toolsOpen && list == null) scope.launch { tools = runCatching { app.contextTools(chat) }.getOrNull() }
            }, verticalAlignment = Alignment.CenterVertically) {
                Icon(if (toolsOpen) Icons.chevron else Icons.chevronRight, null, tint = Palette.muted, modifier = Modifier.size(14.dp))
                Text("MCP tools · $count", style = Type.caption.copy(color = Palette.text), modifier = Modifier.padding(start = 4.dp))
            }
            if (toolsOpen) {
                if (list == null) Text("Loading…", style = Type.caption.copy(color = Palette.muted))
                list?.forEach { tool ->
                    Row {
                        Text(tool.name, style = Type.caption.copy(color = if (tool.loaded) Palette.text else Palette.muted), maxLines = 1, overflow = TextOverflow.Ellipsis,
                            modifier = Modifier.weight(1f))
                        Text(tokens(tool.tokens), style = Type.caption.copy(color = Palette.muted))
                    }
                }
            }
        }
    }
}
