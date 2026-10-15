package io.oneloop.remote

import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectHorizontalDragGestures
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.launch
import kotlin.math.roundToInt
import java.util.Locale

// ---------------------------------------------------------------------------
// Reasoning effort, like the desktop (effort_slider.go) and Codex: a pill
// track, the amber part up to a white thumb inside it, dots at the stops.
// The thumb glides to a tapped stop and follows a drag, then settles.

@Composable
fun EffortSlider(steps: Int, index: Int, onChange: (Int) -> Unit) {
    val position = remember { Animatable(index.toFloat()) }
    val scope = rememberCoroutineScope()
    var dragging by remember { mutableStateOf(false) }
    LaunchedEffect(index) { if (!dragging) position.animateTo(index.toFloat(), tween(240, easing = { 1 - (1 - it).let { x -> x * x * x * x * x } })) }
    val grow by animateFloatAsState(if (dragging) 1.5f else 0f, tween(140), label = "thumb")
    val height = 34.dp
    Canvas(
        Modifier.fillMaxWidth().height(height)
            .pointerInput(steps) {
                detectTapGestures { offset ->
                    val inset = size.height / 2f
                    val stop = ((offset.x - inset) / (size.width - 2 * inset) * (steps - 1)).roundToInt().coerceIn(0, steps - 1)
                    onChange(stop)
                }
            }
            .pointerInput(steps) {
                val inset = size.height / 2f
                fun at(x: Float) = ((x - inset) / (size.width - 2 * inset) * (steps - 1)).coerceIn(0f, (steps - 1).toFloat())
                detectHorizontalDragGestures(
                    onDragStart = { dragging = true; scope.launch { position.snapTo(at(it.x)) } },
                    onDragEnd = {
                        dragging = false
                        val stop = position.value.roundToInt()
                        scope.launch { position.animateTo(stop.toFloat(), tween(240)) }
                        onChange(stop)
                    },
                    onDragCancel = { dragging = false },
                ) { change, _ -> scope.launch { position.snapTo(at(change.position.x)) } }
            },
    ) {
        val h = size.height
        val inset = h / 2f
        val span = size.width - 2 * inset
        fun stopX(i: Float) = if (steps < 2) inset else inset + span * i / (steps - 1)
        val radius = CornerRadius(h / 2, h / 2)
        drawRoundRect(Color(0xFF333333), cornerRadius = radius)
        val x = stopX(position.value)
        drawRoundRect(Palette.amber, size = Size(x + inset, h), cornerRadius = radius)
        for (i in 0 until steps) {
            val passed = i <= position.value + 0.01f
            drawCircle(if (passed) Palette.ink.copy(alpha = 0.35f) else Color.White.copy(alpha = 0.33f), 3.dp.toPx(), Offset(stopX(i.toFloat()), h / 2))
        }
        val thumb = 12.dp.toPx() + grow.dp.toPx()
        drawCircle(Color.Black.copy(alpha = 0.18f), thumb + 1.dp.toPx(), Offset(x, h / 2 + 1.dp.toPx()))
        drawCircle(Palette.text, thumb, Offset(x, h / 2))
    }
}

// ---------------------------------------------------------------------------
// Context use: amber for what is used, white for what is left. A tap shows
// the figures (phones have no hover).

@Composable
fun ContextRing(used: Int, window: Int) {
    var open by remember { mutableStateOf(false) }
    val fraction = if (window > 0) (used.toFloat() / window).coerceIn(0f, 1f) else 0f
    val sweep by animateFloatAsState(fraction * 360f, tween(400), label = "context")
    Box {
        Box(Modifier.size(36.dp).clip(RoundedCornerShape(50)).clickable { open = true }, contentAlignment = Alignment.Center) {
            Canvas(Modifier.size(17.dp)) {
                val stroke = 3.dp.toPx()
                val inset = stroke / 2
                val arc = Size(size.width - stroke, size.height - stroke)
                drawArc(Palette.text, 0f, 360f, false, Offset(inset, inset), arc, style = Stroke(stroke))
                if (sweep > 0f) drawArc(Palette.amber, -90f, sweep, false, Offset(inset, inset), arc, style = Stroke(stroke))
            }
        }
        DropdownMenu(open, { open = false }, containerColor = Palette.raised) {
            Column(Modifier.padding(horizontal = 14.dp, vertical = 6.dp)) {
                if (window <= 0) {
                    Text("Context usage", style = Type.body.copy(fontSize = 14.sp))
                    Text("Available once a model is selected", style = Type.small)
                } else {
                    Text("${(fraction * 100).toInt()}% of the context used", style = Type.body.copy(fontSize = 14.sp))
                    Text("${tokens(used)} of ${tokens(window)} tokens", style = Type.small)
                }
            }
        }
    }
}

private fun tokens(n: Int) = when {
    n >= 1_000_000 -> String.format(Locale.ROOT, "%.1fM", n / 1e6).replace(".0M", "M")
    n >= 1000 -> "${(n + 500) / 1000}k"
    else -> "$n"
}

// ---------------------------------------------------------------------------
// Model: a chip with the model and its effort; the sheet is the desktop's
// model popover (Codex layout: level, model, track) above the model list.

@Composable
fun ModelChip(app: RemoteApp, onClick: () -> Unit, modifier: Modifier = Modifier) {
    val model = app.chatModel
    val effort = model?.efforts?.firstOrNull { it.value == app.chatEffort }?.label
    Row(
        modifier.clip(RoundedCornerShape(8.dp)).clickable(onClick = onClick).padding(horizontal = 6.dp, vertical = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            model?.name ?: if (app.modelsLoading) "Loading models…" else "Choose a model",
            style = Type.small.copy(color = Palette.text.copy(alpha = 0.86f)), maxLines = 1, overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f, fill = false),
        )
        if (effort != null) {
            Spacer(Modifier.width(6.dp))
            Text(effort, style = Type.small.copy(color = Palette.faint), maxLines = 1)
        }
        Spacer(Modifier.width(2.dp))
        Icon(Icons.chevron, null, tint = Palette.faint, modifier = Modifier.size(15.dp))
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ModelSheet(app: RemoteApp, onDone: () -> Unit) {
    ModalBottomSheet(onDismissRequest = onDone, containerColor = Palette.surface, sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)) {
        Column(Modifier.verticalScroll(rememberScrollState()).padding(horizontal = 20.dp).padding(bottom = 24.dp)) {
            val model = app.chatModel
            if (model != null && model.efforts.size > 1) {
                val index = model.efforts.indexOfFirst { it.value == app.chatEffort }.coerceAtLeast(0)
                Text(model.efforts[index].label, style = Type.title.copy(color = Palette.amber), textAlign = TextAlign.Center, modifier = Modifier.fillMaxWidth())
                Text("Reasoning effort · ${model.name}", style = Type.small, textAlign = TextAlign.Center, modifier = Modifier.fillMaxWidth().padding(top = 2.dp, bottom = 14.dp))
                EffortSlider(model.efforts.size, index) { app.setModel(effort = model.efforts[it].value) }
                Spacer(Modifier.height(22.dp))
            }
            Text("Model", style = Type.small.copy(color = Palette.faint), modifier = Modifier.padding(start = 4.dp, bottom = 6.dp))
            if (app.models.isEmpty()) Text(if (app.modelsLoading) "Loading models…" else "No model available. Add a connection in 1loop's Settings.", style = Type.small)
            for (option in app.models) {
                val selected = option.label == model?.label
                Row(
                    Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).background(if (selected) Palette.selected else Color.Transparent)
                        .clickable { app.setModel(label = option.label) }.padding(horizontal = 12.dp, vertical = 10.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Column(Modifier.weight(1f)) {
                        Text(option.name, style = Type.body.copy(fontSize = 14.5.sp), maxLines = 1, overflow = TextOverflow.Ellipsis)
                        Text(option.detail, style = Type.caption.copy(color = Palette.muted), maxLines = 1, overflow = TextOverflow.Ellipsis)
                    }
                    if (selected) Icon(Icons.check, null, tint = Palette.amber, modifier = Modifier.size(18.dp))
                }
            }
        }
    }
}

// ---------------------------------------------------------------------------
// Approval mode: the desktop's menu, a shield or a bolt with a description.

@Composable
fun ApprovalChip(app: RemoteApp) {
    var open by remember { mutableStateOf(false) }
    val yolo = app.approvalMode == "yolo"
    val auto = app.approvalMode == "auto"
    Box {
        Row(
            Modifier.clip(RoundedCornerShape(8.dp)).clickable { open = true }.padding(horizontal = 6.dp, vertical = 6.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            val ink = if (yolo) Palette.danger else Palette.muted
            Icon(if (yolo) Icons.bolt else if (auto) Icons.shieldCheck else Icons.shield, null, tint = ink, modifier = Modifier.size(16.dp))
            Spacer(Modifier.width(6.dp))
            Text(if (yolo) "YOLO" else if (auto) "Auto" else "Approval", style = Type.small.copy(color = ink))
        }
        DropdownMenu(open, { open = false }, containerColor = Palette.popover) {
            ModeItem(Icons.shield, "Approval required", "Ask before each command and edit", !yolo && !auto) { app.chooseApprovalMode("approval"); open = false }
            if (app.autoMode) { // only Claude Code's harness has this mode
                ModeItem(Icons.shieldCheck, "Auto mode", "Claude Code runs what it judges safe", auto) { app.chooseApprovalMode("auto"); open = false }
            }
            ModeItem(Icons.bolt, "YOLO mode", "Run commands and edits automatically", yolo, danger = true) { app.chooseApprovalMode("yolo"); open = false }
        }
    }
}

@Composable
private fun ModeItem(icon: ImageVector, title: String, detail: String, checked: Boolean, danger: Boolean = false, onClick: () -> Unit) {
    Row(
        Modifier.widthIn(min = 260.dp).clickable(onClick = onClick).padding(horizontal = 14.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(icon, null, tint = if (danger) Palette.danger else Palette.text, modifier = Modifier.size(18.dp))
        Column(Modifier.weight(1f).padding(horizontal = 12.dp)) {
            Text(title, style = Type.body.copy(fontSize = 14.5.sp, color = if (danger) Palette.danger else Palette.text, fontWeight = FontWeight.Normal))
            Text(detail, style = Type.caption.copy(color = Palette.muted))
        }
        if (checked) Icon(Icons.check, null, tint = Palette.amber, modifier = Modifier.size(18.dp))
    }
}
