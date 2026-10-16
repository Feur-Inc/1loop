package io.oneloop.remote

import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectHorizontalDragGestures
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
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
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.launch
import kotlin.math.roundToInt
import java.text.SimpleDateFormat
import java.util.Date
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
    val height = 48.dp
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
        val thumb = 18.dp.toPx() + grow.dp.toPx()
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
// model menu for a phone: accounts with their usage, the account's models,
// then the effort track.

@Composable
fun ModelChip(app: RemoteApp, onClick: () -> Unit, modifier: Modifier = Modifier) {
    val model = app.chatModel
    val effort = listOfNotNull(
        model?.efforts?.firstOrNull { it.value == app.chatEffort }?.label,
        model?.tiers?.firstOrNull { it.id == app.chatTier.ifEmpty { model.defaultTier } }?.name,
    ).joinToString(" · ").ifEmpty { null }
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
    LaunchedEffect(Unit) { app.loadLimits() }
    val model = app.chatModel
    // The account whose models show: the chosen model's, until another is tapped.
    var picked by remember { mutableStateOf<String?>(null) }
    val shown = picked ?: model?.account ?: app.accounts.firstOrNull()?.id.orEmpty()
    ModalBottomSheet(onDismissRequest = onDone, containerColor = Palette.surface, sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)) {
        Column(Modifier.verticalScroll(rememberScrollState()).padding(horizontal = 20.dp).padding(bottom = 24.dp)) {
            if (app.accounts.isNotEmpty()) {
                SheetCaption("Account")
                for (account in app.accounts) {
                    AccountRow(account, app.limits[account.id], account.id == shown) {
                        picked = account.id
                        // Like the desktop: the same model in that account, or its first one.
                        val options = app.models.filter { it.account == account.id }
                        if (options.isNotEmpty() && model?.account != account.id) {
                            app.setModel(label = (options.firstOrNull { it.name == model?.name } ?: options.first()).label)
                        }
                    }
                }
                Spacer(Modifier.height(18.dp))
            }
            SheetCaption("Model")
            // A computer older than this app sends no accounts: every model shows.
            val options = if (app.accounts.isEmpty()) app.models.toList() else app.models.filter { it.account == shown }
            if (options.isEmpty()) {
                val problem = app.accounts.firstOrNull { it.id == shown }?.problem.orEmpty()
                Text(
                    when {
                        app.modelsLoading -> "Loading models…"
                        problem.isNotEmpty() -> problem
                        app.accounts.isNotEmpty() -> "No models selected in settings."
                        else -> "No model available. Add a connection in 1loop's Settings."
                    },
                    style = Type.small, modifier = Modifier.padding(start = 4.dp),
                )
            }
            for (option in options) {
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
            // The plan's speeds, only when it offers some (Standard, Fast…).
            if (model != null && model.tiers.isNotEmpty()) {
                Spacer(Modifier.height(18.dp))
                SheetCaption("Speed")
                val standard = if (model.defaultTier.isNotEmpty()) "default" else ""
                val chosen = app.chatTier.ifEmpty { model.defaultTier }.ifEmpty { standard }
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp), modifier = Modifier.horizontalScroll(rememberScrollState())) {
                    SpeedChip("Standard", chosen == standard || chosen == "default") { app.setModel(tier = standard) }
                    for (tier in model.tiers) SpeedChip(tier.name, chosen == tier.id) { app.setModel(tier = tier.id) }
                }
            }
            // Effort last, within the thumb's reach, as in ChatGPT's app.
            if (model != null && model.efforts.size > 1) {
                val index = model.efforts.indexOfFirst { it.value == app.chatEffort }.coerceAtLeast(0)
                val effort = model.efforts[index]
                Spacer(Modifier.height(28.dp))
                Text(
                    buildAnnotatedString {
                        append("Effort: ")
                        withStyle(SpanStyle(color = Palette.amber)) { append(effort.label) }
                    },
                    style = Type.headline.copy(fontWeight = FontWeight.SemiBold), textAlign = TextAlign.Center, modifier = Modifier.fillMaxWidth(),
                )
                Text(
                    effort.about.ifEmpty { model.name }, style = Type.small, textAlign = TextAlign.Center,
                    modifier = Modifier.fillMaxWidth().padding(top = 2.dp, bottom = 16.dp),
                )
                EffortSlider(model.efforts.size, index) { app.setModel(effort = model.efforts[it].value) }
            }
        }
    }
}

@Composable
private fun SpeedChip(text: String, selected: Boolean, onClick: () -> Unit) {
    Text(
        text, style = Type.small.copy(color = if (selected) Palette.ink else Palette.text),
        modifier = Modifier.clip(RoundedCornerShape(50)).background(if (selected) Palette.amber else Palette.raised).clickable(onClick = onClick)
            .padding(horizontal = 14.dp, vertical = 8.dp),
    )
}

@Composable
private fun SheetCaption(text: String) {
    Text(text, style = Type.small.copy(color = Palette.faint), modifier = Modifier.padding(start = 4.dp, bottom = 6.dp))
}

// An account with what is left of its usage windows, as in the desktop's
// strip; a local server whose models could not be listed cannot be chosen.
@Composable
private fun AccountRow(account: Account, limits: AccountLimits?, selected: Boolean, onClick: () -> Unit) {
    val enabled = !(account.provider == "vllm" && account.problem.isNotEmpty())
    Column(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(12.dp)).background(if (selected) Palette.selected else Color.Transparent)
            .clickable(enabled = enabled, onClick = onClick).padding(horizontal = 12.dp, vertical = 10.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f)) {
                Text(
                    account.name, style = Type.body.copy(fontSize = 14.5.sp, color = if (enabled) Palette.text else Palette.faint),
                    maxLines = 1, overflow = TextOverflow.Ellipsis,
                )
                Text(account.problem.ifEmpty { account.kind }, style = Type.caption.copy(color = Palette.muted), maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
            if (selected) Icon(Icons.check, null, tint = Palette.amber, modifier = Modifier.size(18.dp))
        }
        if (enabled) when {
            limits == null -> Text("Loading limits…", style = Type.caption, modifier = Modifier.padding(top = 6.dp))
            limits.windows.isEmpty() -> Text(limits.note.ifEmpty { "Limits not provided" }, style = Type.caption, modifier = Modifier.padding(top = 6.dp))
            else -> Row(Modifier.padding(top = 10.dp).horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(14.dp)) {
                for (window in limits.windows) LimitGauge(window)
            }
        }
    }
}

private val resetFormat = SimpleDateFormat("dd/MM HH:mm", Locale.ROOT)

@Composable
private fun LimitGauge(window: LimitWindow) {
    // What is left, said so, red under a fifth: as on the desktop.
    val low = window.left <= 20f
    Column(Modifier.width(118.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(window.label.replace(" · ", " "), style = Type.caption.copy(color = Palette.muted), maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
            Text(
                "${window.left.roundToInt()} % left", maxLines = 1,
                style = Type.caption.copy(color = if (low) Palette.danger else Palette.text, fontWeight = FontWeight.SemiBold),
            )
        }
        Box(Modifier.padding(vertical = 5.dp).fillMaxWidth().height(4.dp).clip(RoundedCornerShape(2.dp)).background(Color(0xFF333333))) {
            Box(Modifier.fillMaxWidth((window.left / 100f).coerceIn(0f, 1f)).fillMaxHeight().background(if (low) Palette.danger else Palette.amber))
        }
        Text(if (window.reset > 0) "↻ " + resetFormat.format(Date(window.reset)) else "Reset not provided", style = Type.caption, maxLines = 1)
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
            if (app.autoMode) { // a computer older than this app may not offer it
                ModeItem(Icons.shieldCheck, "Auto mode", "Approves safe actions, asks you for risky ones", auto) { app.chooseApprovalMode("auto"); open = false }
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
