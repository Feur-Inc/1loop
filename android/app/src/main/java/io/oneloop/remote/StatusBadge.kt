package io.oneloop.remote

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectVerticalDragGestures
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp

/**
 * The floating badge over other apps while a model runs (AgentStatusService):
 * 1loop's working ring; tapped, a pill with Stop, Talk to the agent (opens
 * the chat) and the mini preview of what it does and its tasks. Small by
 * design: it never covers the screen, and it moves up and down by dragging.
 */
@Composable
fun StatusBadge(onStop: (String) -> Unit, onTalk: (String) -> Unit, onDrag: (Float) -> Unit) {
    val status by AgentStatus.status.collectAsState()
    val run = status ?: return
    var open by remember { mutableStateOf(false) }
    var preview by remember { mutableStateOf(false) }
    val drag = Modifier.pointerInput(Unit) { detectVerticalDragGestures { change, dy -> change.consume(); onDrag(dy) } }
    // No size animations: the overlay window wraps its content, so each frame
    // of one would resize the window through the window manager.
    Column(Modifier.padding(8.dp), horizontalAlignment = Alignment.End) {
        if (!open || run.phoneUse) {
            // Collapsed, and always during phone use (click-through then).
            Box(
                drag.size(52.dp).clip(CircleShape).background(Palette.surface).border(1.dp, Palette.borderSoft, CircleShape)
                    .clickable { open = true }.semantics { contentDescription = "1loop is working: ${run.label}" },
                contentAlignment = Alignment.Center,
            ) { WorkRing(run.mark, 32.dp) }
        } else {
            Row(
                drag.clip(RoundedCornerShape(26.dp)).background(Palette.surface).border(1.dp, Palette.borderSoft, RoundedCornerShape(26.dp))
                    .padding(start = 10.dp, end = 4.dp, top = 4.dp, bottom = 4.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(2.dp),
            ) {
                Box(Modifier.clickable { open = false }) { WorkRing(run.mark, 26.dp) }
                Spacer(Modifier.width(6.dp))
                Text(
                    run.label.ifEmpty { "Working…" }, style = Type.small.copy(color = Palette.text),
                    maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.widthIn(max = 150.dp),
                )
                RoundButton(Icons.close, Color.Transparent, Palette.danger, description = "Stop", size = 40.dp, iconSize = 18.dp) { onStop(run.chat) }
                RoundButton(Icons.chats, Color.Transparent, Palette.muted, description = "Talk to the agent", size = 40.dp, iconSize = 18.dp) { onTalk(run.chat) }
                RoundButton(Icons.tasks, if (preview) Palette.code else Color.Transparent, Palette.muted, description = if (preview) "Hide preview" else "Show preview", size = 40.dp, iconSize = 18.dp) { preview = !preview }
            }
            if (preview) Preview(run)
        }
    }
}

/** What the agent does and its tasks, in a few lines. */
@Composable
private fun Preview(run: RunStatus) {
    Column(
        Modifier.padding(top = 6.dp).widthIn(max = 280.dp).clip(RoundedCornerShape(14.dp)).background(Palette.surface)
            .border(1.dp, Palette.borderSoft, RoundedCornerShape(14.dp)).padding(12.dp),
        verticalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        Text(run.title.ifEmpty { "1loop" }, style = Type.body, maxLines = 1, overflow = TextOverflow.Ellipsis)
        Text(run.label.ifEmpty { "Working…" }, style = Type.small, maxLines = 2, overflow = TextOverflow.Ellipsis)
        if (run.todo.isNotEmpty()) {
            Text("Tasks ${run.done}/${run.todo.size}", style = Type.caption.copy(color = Palette.muted))
            for (task in run.todo.take(6)) {
                val (mark, color) = when (task.status) {
                    "completed" -> "✓" to Palette.added
                    "in_progress" -> "•" to Palette.amber
                    else -> "○" to Palette.faint
                }
                Row(verticalAlignment = Alignment.Top) {
                    Text(mark, style = Type.small.copy(color = color), modifier = Modifier.width(16.dp))
                    Text(task.text, style = Type.small, maxLines = 1, overflow = TextOverflow.Ellipsis)
                }
            }
            if (run.todo.size > 6) Text("+${run.todo.size - 6} more", style = Type.caption)
        }
    }
}
