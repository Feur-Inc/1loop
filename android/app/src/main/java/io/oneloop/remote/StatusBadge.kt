package io.oneloop.remote

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
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
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.SideEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.scale
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.lerp
import androidx.compose.ui.layout.boundsInRoot
import androidx.compose.ui.layout.onGloballyPositioned
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
 * design: it never covers the screen. It moves anywhere by dragging, settles
 * on a side when let go near it (BadgeWindow), and goes away on the ✕ ring.
 * A question from the agent opens it on the question, answered there.
 */
@Composable
fun StatusBadge(
    ui: BadgeUi, onStop: (String) -> Unit, onTalk: (String) -> Unit,
    onAnswer: (Questions, Map<String, String>) -> Unit, onTyping: (Boolean) -> Unit,
    onDismiss: (String) -> Unit,
) {
    val status by AgentStatus.status.collectAsState()
    val run = status ?: return
    var open by remember { mutableStateOf(false) }
    var preview by remember { mutableStateOf(false) }
    LaunchedEffect(run.question?.id) { if (run.question != null) open = true }
    // Moving it folds the pill: the badge alone follows the finger.
    LaunchedEffect(ui.dragging) { if (ui.dragging) open = false }
    SideEffect { ui.expanded = open && !run.phoneUse }
    // Where a drag may start (BadgeWindow reads the touches): the badge, or the pill's row.
    val drag = Modifier.onGloballyPositioned { ui.handle = it.boundsInRoot() }
    // No size animations: the overlay window wraps its content, so each frame
    // of one would resize the window through the window manager.
    // On the left half, it opens toward the right.
    Column(Modifier.padding(8.dp), horizontalAlignment = if (ui.onRight) Alignment.End else Alignment.Start) {
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
                // Weighted, so measured after the buttons: a long label is cut short rather
                // than squeezing the last button.
                Text(
                    run.label.ifEmpty { "Working…" }, style = Type.small.copy(color = Palette.text),
                    maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f, fill = false).widthIn(max = 150.dp),
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

/** Working: 1loop's ring; driving the phone: the ring around a phone; finished: the ring closed on its check. */
@Composable
private fun StatusMark(run: RunStatus, size: Dp) {
    when {
        run.finished -> WorkRing("done", size) // the ring closes on its check, as in the app
        run.phoneUse -> WorkRing("phone", size)
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
                // The marks of the tasks panel: the closed ring on its check, the working ring, an empty ring.
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Box(Modifier.size(16.dp), contentAlignment = Alignment.Center) {
                        if (task.status == "in_progress") WorkRing("think", 16.dp) else TaskMark(task.status == "completed", 14.dp)
                    }
                    Spacer(Modifier.width(6.dp))
                    Text(task.text, style = Type.small, maxLines = 1, overflow = TextOverflow.Ellipsis)
                }
            }
            if (run.todo.size > 6) Text("+${run.todo.size - 6} more", style = Type.caption)
        }
    }
}

/**
 * Where the badge goes away, at the bottom of the screen while it moves: the
 * ring closed on its ✕ (the work ring's stopped state), red once the badge
 * is on it.
 */
@Composable
fun RemoveTarget(ui: BadgeUi) {
    val shown = remember { Animatable(0f) }
    LaunchedEffect(ui.targetShown) { shown.animateTo(if (ui.targetShown) 1f else 0f, tween(180, easing = CssEase)) }
    val near by animateFloatAsState(if (ui.nearTarget) 1f else 0f, tween(150, easing = CssEase), label = "near")
    Box(
        Modifier.fillMaxSize().graphicsLayer {
            alpha = shown.value
            translationY = (1 - shown.value) * 24.dp.toPx()
        },
        contentAlignment = Alignment.Center,
    ) {
        val color = lerp(Palette.muted, Palette.danger, near)
        Canvas(Modifier.size(64.dp).graphicsLayer { scaleX = 1 + 0.2f * near; scaleY = 1 + 0.2f * near }.semantics { contentDescription = "Remove the badge" }) {
            scale(size.width / 120f, size.width / 120f, pivot = Offset.Zero) {
                drawCircle(Palette.surface, 58f, Offset(60f, 60f)) // over any app
                drawCircle(color, 46f, Offset(60f, 60f), style = Stroke(12f))
                drawPath(removeCross, color, style = Stroke(9f, cap = StrokeCap.Round))
            }
        }
    }
}

private val removeCross = Path().apply { moveTo(47f, 47f); lineTo(73f, 73f); moveTo(73f, 47f); lineTo(47f, 73f) }
