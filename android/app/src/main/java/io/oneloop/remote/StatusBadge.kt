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
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.delay

/**
 * The floating badge over other apps while a model runs (AgentStatusService):
 * 1loop's working ring; tapped, a pill with Stop, Talk to the agent (opens
 * the chat) and the mini preview of what it does and its tasks. Small by
 * design: it never covers the screen, and it moves up and down by dragging.
 * A question from the agent opens it on the question, answered there.
 */
@Composable
fun StatusBadge(
    onStop: (String) -> Unit, onTalk: (String) -> Unit, onDrag: (Float) -> Unit,
    onAnswer: (Questions, Map<String, String>) -> Unit, onTyping: (Boolean) -> Unit,
    onDismiss: (String) -> Unit,
) {
    val status by AgentStatus.status.collectAsState()
    val run = status ?: return
    var open by remember { mutableStateOf(false) }
    var preview by remember { mutableStateOf(false) }
    LaunchedEffect(run.question?.id) { if (run.question != null) open = true }
    val drag = Modifier.pointerInput(Unit) { detectVerticalDragGestures { change, dy -> change.consume(); onDrag(dy) } }
    // No size animations: the overlay window wraps its content, so each frame
    // of one would resize the window through the window manager.
    Column(Modifier.padding(8.dp), horizontalAlignment = Alignment.End) {
        if (!open || run.phoneUse) {
            // Collapsed, and always during phone use (click-through then).
            Box(
                drag.size(52.dp).clip(CircleShape).background(Palette.surface).border(1.dp, Palette.borderSoft, CircleShape)
                    .clickable { open = true }
                    .semantics { contentDescription = if (run.finished) "1loop is done" else "1loop is working: ${run.label}" },
                contentAlignment = Alignment.Center,
            ) {
                StatusMark(run, 32.dp)
            }
        } else {
            Row(
                drag.clip(RoundedCornerShape(26.dp)).background(Palette.surface).border(1.dp, Palette.borderSoft, RoundedCornerShape(26.dp))
                    .padding(start = 10.dp, end = 4.dp, top = 4.dp, bottom = 4.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(2.dp),
            ) {
                Box(Modifier.clickable { open = false }) { StatusMark(run, 30.dp) }
                Spacer(Modifier.width(6.dp))
                Text(
                    run.label.ifEmpty { "Working…" }, style = Type.small.copy(color = Palette.text),
                    maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.widthIn(max = 150.dp),
                )
                // Finished: nothing to stop, the badge only waits to be put away.
                if (run.finished) RoundButton(Icons.close, Color.Transparent, Palette.muted, description = "Dismiss", size = 46.dp, iconSize = 24.dp) { onDismiss(run.chat) }
                else RoundButton(Icons.close, Color.Transparent, Palette.danger, description = "Stop", size = 46.dp, iconSize = 24.dp) { onStop(run.chat) }
                RoundButton(Icons.chats, Color.Transparent, Palette.muted, description = "Talk to the agent", size = 46.dp, iconSize = 24.dp) {
                    onTalk(run.chat)
                    if (run.finished) onDismiss(run.chat)
                }
                // Open: its icon lights up, with no disc that would make it look bigger than the others.
                RoundButton(Icons.tasks, Color.Transparent, if (preview) Palette.amber else Palette.muted, description = if (preview) "Hide preview" else "Show preview", size = 46.dp, iconSize = 24.dp) { preview = !preview }
            }
            val question = run.question
            if (question != null) QuestionCard(question, onAnswer, onTyping)
            else if (preview) Preview(run)
        }
    }
}

/**
 * The agent's questions, one after the other: an option, or an answer written
 * under Other (the keyboard comes up for it only), sent once all are answered.
 */
@Composable
private fun QuestionCard(request: Questions, onAnswer: (Questions, Map<String, String>) -> Unit, onTyping: (Boolean) -> Unit) {
    var index by remember(request.id) { mutableStateOf(0) }
    val answers = remember(request.id) { mutableStateMapOf<String, String>() }
    var other by remember(request.id, index) { mutableStateOf<String?>(null) } // null: not writing one
    val question = request.questions.getOrNull(index) ?: return
    DisposableEffect(request.id) { onDispose { onTyping(false) } }
    fun choose(value: String) {
        answers[question.id] = value
        other = null
        onTyping(false)
        if (index + 1 < request.questions.size) index++ else onAnswer(request, answers.toMap())
    }
    Column(
        Modifier.padding(top = 6.dp).widthIn(max = 300.dp).clip(RoundedCornerShape(14.dp)).background(Palette.surface)
            .border(1.dp, Palette.borderSoft, RoundedCornerShape(14.dp)).padding(12.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        val header = listOfNotNull(
            question.header.ifEmpty { null },
            if (request.questions.size > 1) "${index + 1}/${request.questions.size}" else null,
        ).joinToString(" · ")
        if (header.isNotEmpty()) Text(header, style = Type.caption.copy(color = Palette.amber))
        Text(question.text, style = Type.body)
        for (option in question.options) {
            Column(
                Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).background(Palette.code)
                    .clickable { choose(option.label) }.padding(horizontal = 12.dp, vertical = 10.dp),
            ) {
                Text(option.label, style = Type.body)
                if (option.description.isNotEmpty()) Text(option.description, style = Type.small, maxLines = 2, overflow = TextOverflow.Ellipsis)
            }
        }
        val written = other
        if (written == null) {
            Pill("Other…") { other = ""; onTyping(true) }
        } else {
            val focus = remember { FocusRequester() }
            val keyboard = LocalSoftwareKeyboardController.current
            Field(written, { other = it }, "Your answer", KeyboardType.Text, Modifier.focusRequester(focus))
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Pill("Cancel") { other = null; onTyping(false) }
                Pill("Send", primary = true, enabled = written.isNotBlank()) { choose(Questions.OTHER + written.trim()) }
            }
            // The window takes the keyboard once focusable (onTyping): then focus the field.
            LaunchedEffect(Unit) {
                delay(150)
                focus.requestFocus()
                keyboard?.show()
            }
        }
    }
}

/** Working: 1loop's ring; driving the phone: a phone; finished: the ring closed on its check. */
@Composable
private fun StatusMark(run: RunStatus, size: Dp) {
    when {
        run.finished -> WorkRing("done", size) // the ring closes on its check, as in the app
        run.phoneUse -> Icon(Icons.phone, null, tint = Palette.amber, modifier = Modifier.size(size * 0.8f))
        else -> WorkRing(run.mark, size)
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
