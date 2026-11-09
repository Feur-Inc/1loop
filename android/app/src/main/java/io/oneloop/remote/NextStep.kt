// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import org.json.JSONObject

/**
 * The model's wrap_up at the end of a turn (#215), read from the tool row's
 * arguments as the desktop does: a recap and, when something waits on the
 * user, one question with one or two replies.
 */
data class WrapUp(val recap: String, val ask: String, val actions: List<WrapUpAction>, val blocked: Boolean) {
    /** Only a question is drawn: a recap alone is the chat's status. */
    val card get() = ask.isNotEmpty() && actions.isNotEmpty()

    /** The button whose reply the user sent, null for their own words. */
    fun chosen(sent: String): WrapUpAction? = actions.firstOrNull { it.reply.trim() == sent.trim() }

    companion object {
        fun isTool(name: String) = name == "wrap_up" || name == "mcp__1loop_todo__wrap_up"

        /** The call's arguments; null for a call 1loop refused. */
        fun from(item: Item): WrapUp? {
            if (item.kind != "tool" || !isTool(item.tool) || item.failed.isNotEmpty()) return null
            val o = runCatching { JSONObject(item.args) }.getOrNull() ?: return null
            val actions = o.optJSONArray("actions")?.let { list ->
                (0 until list.length()).mapNotNull { list.optJSONObject(it) }.map {
                    WrapUpAction(it.optString("label").trim(), if (it.isNull("message")) "" else it.optString("message").trim())
                }
            }.orEmpty()
            val ask = if (o.isNull("ask")) "" else o.optString("ask").trim()
            return WrapUp(o.optString("recap").trim(), ask, actions, o.optBoolean("blocked"))
        }

        /** The current turn's card, once its call has run, while the user has not answered. */
        fun live(items: List<Item>): WrapUp? {
            for (i in items.indices.reversed()) {
                val item = items[i]
                if (item.kind == "user") return null
                if (item.kind == "tool" && isTool(item.tool)) return from(item)?.takeIf { it.card && item.state == "done" }
            }
            return null
        }

        /** The card of the turn the user's message at index answers. */
        fun answered(items: List<Item>, index: Int): WrapUp? {
            for (i in index - 1 downTo 0) {
                val item = items[i]
                if (item.kind == "user") return null
                if (item.kind == "tool" && isTool(item.tool)) return from(item)?.takeIf { it.card }
            }
            return null
        }
    }
}

/** A reply button: it sends message, or the label when there is none. */
data class WrapUpAction(val label: String, val message: String) {
    val reply get() = message.ifEmpty { label }
}

/** The next-step card. onReply null draws it answered, with the reply chosen. */
@Composable
fun NextStepCard(wrap: WrapUp, onReply: ((String) -> Unit)?, sent: String = "") {
    val shape = RoundedCornerShape(14.dp)
    Column(
        Modifier.fillMaxWidth().background(Palette.surface, shape).border(1.dp, Palette.border, shape).padding(14.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        Text(wrap.recap, style = Type.small.copy(fontSize = 14.sp, lineHeight = 20.sp))
        Text(wrap.ask, style = Type.body.copy(fontWeight = FontWeight.SemiBold))
        if (onReply != null) {
            Row(Modifier.fillMaxWidth().padding(top = 4.dp), horizontalArrangement = Arrangement.spacedBy(8.dp, Alignment.End)) {
                // The model's pick, the main button, last: under the thumb.
                for ((i, action) in wrap.actions.withIndex().reversed()) Pill(action.label, primary = i == 0) { onReply(action.reply) }
            }
        } else wrap.chosen(sent)?.let { action ->
            Row(verticalAlignment = Alignment.CenterVertically) {
                Icon(Icons.check, null, tint = Palette.faint, modifier = Modifier.size(14.dp))
                Spacer(Modifier.width(6.dp))
                Text(action.label, style = Type.caption)
            }
        }
    }
}
