// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import android.content.ActivityNotFoundException
import android.content.Intent
import android.speech.RecognizerIntent
import android.speech.tts.TextToSpeech
import android.widget.Toast
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.DrawerValue
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.ModalDrawerSheet
import androidx.compose.material3.ModalNavigationDrawer
import androidx.compose.material3.Text
import androidx.compose.material3.rememberDrawerState
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.rotate
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import org.json.JSONObject
import java.time.LocalDate
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale
import kotlin.math.PI
import kotlin.math.cos
import kotlin.math.sin

// The Loopy space (#352), as on the desktop (internal/ui/loopy_view.go): its
// own drawer, one continuous conversation in bubbles, cards to act on, and
// pages for its memory, folder, routines and settings (LoopyPages.kt).

private val loopyMe = Color(0xFF0F7A6E)

/** Loopy's face: the ring of 1loop's logo, its dot and a second eye, in the art direction's 120 unit grid. */
@Composable
fun LoopyFace(size: Dp, state: String, modifier: Modifier = Modifier) {
    val ring = when (state) { "blocked" -> Color(0xFFE86A5F); "done" -> Color(0xFF7FC88F); else -> Palette.amber }
    val busy = state in listOf("thinking", "reading", "copy", "typing")
    // At rest it only blinks, now and then: nothing is drawn in between.
    val blink = remember { Animatable(1f) }
    LaunchedEffect(Motion.enabled, state == "still") {
        while (Motion.enabled && state != "still") {
            delay(3500L + (Math.random() * 3500).toLong())
            blink.animateTo(0.1f, tween(70)); blink.animateTo(1f, tween(120))
        }
    }
    val sway = if (Motion.enabled && busy) rememberInfiniteTransition(label = "loopy").animateFloat(0f, 1f, infiniteRepeatable(tween(1400, easing = LinearEasing)), label = "sway") else null
    val eyes = if (Palette.dark) Color.White else Palette.text
    Canvas(modifier.size(size)) {
        val unit = this.size.minDimension / 120f
        val turn = sin((sway?.value ?: 0f) * 2 * PI).toFloat()
        rotate(turn * 9f) {
            drawArc(ring, -34f, 308f, false, Offset(20 * unit, 20 * unit), Size(80 * unit, 80 * unit), style = Stroke(15 * unit, cap = StrokeCap.Round))
            val open = blink.value
            fun eye(x: Float, y: Float, color: Color) = drawOval(color, Offset((x - 10) * unit, (y - 10 * open) * unit), Size(20 * unit, 20 * open * unit))
            val look = if (busy) turn * 3 else 0f
            eye(50 + look, 47f, eyes)
            val a = -60 * PI / 180
            eye(60 + 40 * cos(a).toFloat() + look, 60 + 40 * sin(a).toFloat(), eyes)
        }
    }
}

/** The logo of a space, with its menu: 1loop or Loopy. */
@Composable
fun SpaceSwitch(app: RemoteApp, inLoopy: Boolean, onPicked: () -> Unit, logo: @Composable () -> Unit) {
    var open by remember { mutableStateOf(false) }
    Box {
        Row(
            Modifier.clip(RoundedCornerShape(10.dp)).clickable(onClickLabel = "Switch between 1loop and Loopy") { open = true }.padding(horizontal = 6.dp, vertical = 4.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            logo()
            Icon(Icons.chevron, null, tint = Palette.muted, modifier = Modifier.padding(start = 6.dp).size(16.dp))
        }
        DropdownMenu(open, { open = false }, containerColor = Palette.popover) {
            for ((name, about, loopy) in listOf(Triple("1loop", "Build, debug and ship in your projects", false), Triple("Loopy", "Your personal agent: mail, errands, routines", true))) {
                DropdownMenuItem(
                    text = { Column { Text(name, style = Type.body); Text(about, style = Type.caption) } },
                    trailingIcon = { if (loopy == inLoopy) Icon(Icons.check, "Selected", tint = Palette.amber, modifier = Modifier.size(18.dp)) },
                    onClick = {
                        open = false
                        if (loopy != inLoopy) { if (loopy) app.openLoopy() else app.closeLoopy() }
                        onPicked()
                    },
                )
            }
        }
    }
}

@Composable
fun LoopyScreen(app: RemoteApp) {
    val loopy = app.loopy
    var panel by remember { mutableStateOf(false) }
    val drawer = rememberDrawerState(DrawerValue.Closed)
    val scope = rememberCoroutineScope()
    LaunchedEffect(app.server?.key, app.connected) { if (app.connected) loopy.reload() }
    BackHandler(app.loopyPage == null && !drawer.isOpen) { app.closeLoopy() }
    BackHandler(drawer.isOpen) { scope.launch { drawer.close() } }
    val close: () -> Unit = { scope.launch { drawer.close() } }
    val page = app.loopyPage
    if (page != null) {
        LoopyPage(app, page) { app.loopyPage = null }
        return
    }
    BoxWithConstraints(Modifier.fillMaxSize().background(Palette.background)) {
        if (maxWidth >= 720.dp) {
            Row(Modifier.fillMaxSize()) {
                LoopyPane(app, Modifier.width(300.dp).fillMaxHeight()) {}
                LoopyConversation(app, null, Modifier.weight(1f)) { panel = true }
            }
        } else ModalNavigationDrawer(
            drawerState = drawer, gesturesEnabled = drawer.isOpen || !panel,
            drawerContent = {
                ModalDrawerSheet(drawerContainerColor = Palette.sidebar, drawerShape = RoundedCornerShape(topEnd = 18.dp, bottomEnd = 18.dp)) {
                    LoopyPane(app, Modifier.fillMaxHeight(), close)
                }
            },
        ) { LoopyConversation(app, { scope.launch { drawer.open() } }, Modifier.fillMaxSize()) { panel = true } }
        if (panel) Box(Modifier.fillMaxSize()) { LoopyBrowser(app) { panel = false } }
    }
}

/** Loopy's own browser, live, and who has it. */
@Composable
private fun LoopyBrowser(app: RemoteApp, onClose: () -> Unit) {
    val loopy = app.loopy
    Column(Modifier.fillMaxSize().background(Palette.background)) {
        Box(Modifier.weight(1f)) { PanelScreen(app, LOOPY_CHAT, onClose) }
        Row(
            Modifier.fillMaxWidth().background(Palette.sidebar).windowInsetsPadding(WindowInsets.navigationBars).padding(horizontal = 14.dp, vertical = 8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(if (loopy.taken) "You have the browser. Loopy waits." else "Loopy's browser", style = Type.small, modifier = Modifier.weight(1f))
            if (loopy.taken) Pill("I'm done", primary = true) { loopy.change("browser.give"); onClose() }
            else Pill("Take over") { loopy.change("browser.take") }
        }
    }
}

/** Loopy's drawer. */
@Composable
private fun LoopyPane(app: RemoteApp, modifier: Modifier, onPicked: () -> Unit) {
    val loopy = app.loopy
    var clearing by remember { mutableStateOf(false) }
    Column(modifier.background(Palette.sidebar).windowInsetsPadding(WindowInsets.safeDrawing).padding(horizontal = 12.dp)) {
        Box(Modifier.padding(top = 12.dp)) { SpaceSwitch(app, true, onPicked) { LoopyLogo() } }
        Column(Modifier.fillMaxWidth().padding(vertical = 14.dp), horizontalAlignment = Alignment.CenterHorizontally) {
            LoopyFace(72.dp, loopy.state)
            Spacer(Modifier.height(8.dp))
            Text("Loopy", style = Type.body.copy(fontWeight = FontWeight.SemiBold))
            Text(loopy.label.ifEmpty { if (app.connected) "Here when you need me" else "Reconnecting…" }, style = Type.caption, textAlign = TextAlign.Center)
            if (loopy.host.isNotEmpty()) Text("Lives on ${loopy.host}", style = Type.caption)
        }
        Column(Modifier.weight(1f).verticalScroll(rememberScrollState())) {
            SideTitle("Waiting for you")
            if (loopy.waiting.isEmpty()) SideNote("Nothing right now")
            for (id in loopy.waiting) loopy.message(id)?.card?.let { card ->
                NavRow(card.title, loopyIcon(card.kind), selected = false) { app.loopyShow = id; onPicked() }
            }
            SideTitle("Delegated tasks")
            if (loopy.tasks.isEmpty()) SideNote("None yet")
            for (task in loopy.tasks.take(8)) NavRow(task.title, Icons.terminal, selected = false, running = task.status == "running", trailing = task.status, blocked = task.status == "waiting") {
                app.openLoopyTask(task); onPicked()
            }
            SideTitle("Routines")
            if (loopy.routines.isEmpty()) SideNote("Ask Loopy to set one up")
            for (routine in loopy.routines) NavRow(routine.title, Icons.refresh, selected = false, trailing = if (routine.on) "on" else "off") { app.loopyPage = "routine:${routine.id}"; onPicked() }
            SideTitle("Loopy")
            NavRow("What I remember", Icons.bookmark, selected = false, trailing = loopy.facts.toString()) { app.loopyPage = "memory"; onPicked() }
            NavRow("My folder", Icons.folder, selected = false) { app.loopyPage = "files"; onPicked() }
            NavRow("Sum up now", Icons.star, selected = false) { loopy.change("summary"); onPicked() }
            NavRow("Start fresh", Icons.plus, selected = false) { clearing = true }
        }
        Box(Modifier.fillMaxWidth().height(1.dp).background(Palette.borderSoft))
        NavRow("Loopy's settings", Icons.settings, selected = false) { app.loopyPage = "settings"; onPicked() }
        Spacer(Modifier.height(8.dp))
    }
    if (clearing) ConfirmDialog("Start fresh?", "The conversation empties. What Loopy remembers, its folder and its routines stay.", "Start fresh", { clearing = false }) { clearing = false; loopy.post("clear", JSONObject()) { loopy.reload() }; onPicked() }
}

@Composable
private fun LoopyLogo() = Row(verticalAlignment = Alignment.CenterVertically) {
    LoopyFace(26.dp, "still")
    Text("Loopy", style = Type.title, modifier = Modifier.padding(start = 8.dp))
}

@Composable
private fun SideTitle(text: String) = Text(text, style = Type.small.copy(color = Palette.faint), modifier = Modifier.padding(start = 10.dp, top = 12.dp, bottom = 2.dp))

@Composable
private fun SideNote(text: String) = Text(text, style = Type.caption, modifier = Modifier.padding(horizontal = 10.dp, vertical = 5.dp))

private fun loopyIcon(kind: String): ImageVector = when (kind) {
    "mail", "message" -> Icons.send
    "event", "routine" -> Icons.refresh
    "signin", "secret", "unlock" -> Icons.lock
    "autolink", "autolink_run" -> Icons.terminal
    "approval" -> Icons.shield
    "delegate" -> Icons.terminal
    "summary" -> Icons.tasks
    else -> Icons.chats
}

@Composable
private fun LoopyConversation(app: RemoteApp, onMenu: (() -> Unit)?, modifier: Modifier, onBrowser: () -> Unit) {
    val loopy = app.loopy
    val list = rememberLazyListState()
    // The newest first, from the bottom: the list stays at its end as bubbles grow.
    val shown = loopy.messages.reversed()
    LaunchedEffect(app.loopyShow, loopy.loaded) {
        val id = app.loopyShow ?: return@LaunchedEffect
        val at = loopy.messages.indexOfLast { it.id == id }
        if (at >= 0) { list.scrollToItem(loopy.messages.size - 1 - at); app.loopyShow = null }
    }
    // Scrolled up by hand, it stays there; what the user sends brings the end back.
    var away by remember { mutableStateOf(false) }
    LaunchedEffect(list.isScrollInProgress) { if (!list.isScrollInProgress) away = list.firstVisibleItemIndex > 0 || list.firstVisibleItemScrollOffset > 300 }
    val last = loopy.messages.lastOrNull()
    LaunchedEffect(last?.id, loopy.messages.size, loopy.running, last?.text?.length) { if (app.loopyShow == null && (!away || last?.mine == true)) list.scrollToItem(0) }
    SpeakAnswers(app)
    Column(modifier.fillMaxSize().background(Palette.background).windowInsetsPadding(WindowInsets.safeDrawing.only(WindowInsetsSides.Top + WindowInsetsSides.Horizontal))) {
        Row(Modifier.fillMaxWidth().padding(horizontal = 8.dp, vertical = 6.dp), verticalAlignment = Alignment.CenterVertically) {
            if (onMenu != null) RoundButton(Icons.chats, Color.Transparent, Palette.muted, description = "Loopy's menu", onClick = onMenu)
            LoopyFace(28.dp, loopy.state, Modifier.padding(start = 4.dp))
            Column(Modifier.weight(1f).padding(horizontal = 8.dp)) {
                Text("Loopy", style = Type.body.copy(fontWeight = FontWeight.Medium))
                Text(
                    if (!app.connected) "Reconnecting to ${app.server?.name}…" else loopy.label.ifEmpty { listOf(loopy.host, loopy.model).filter { it.isNotEmpty() }.joinToString(" · ") },
                    style = Type.caption.copy(color = if (app.connected) Palette.faint else Palette.amber), maxLines = 1, overflow = TextOverflow.Ellipsis,
                )
            }
            if (loopy.browser) RoundButton(Icons.globe, Color.Transparent, if (loopy.taken) Palette.amber else Palette.muted, description = "Loopy's browser", onClick = onBrowser)
        }
        Box(Modifier.weight(1f).fillMaxWidth()) {
            when {
                !loopy.supported -> Centered("No Loopy here", "${app.server?.name ?: "This target"} runs a 1loop without Loopy: update it.")
                loopy.loaded && loopy.messages.isEmpty() -> Column(Modifier.fillMaxSize().padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Bubble("Hi, I'm Loopy. I take care of the everyday things: mail, errands, reminders, and the coding tasks I can hand to an agent in your projects. I always ask before anything that matters.", false)
                    Bubble("Tell me what you need, and how you like things: I learn your ways as we talk.", false)
                }
                else -> LazyColumn(Modifier.fillMaxSize().padding(horizontal = 12.dp), list, reverseLayout = true, verticalArrangement = Arrangement.spacedBy(6.dp), contentPadding = PaddingValues(vertical = 10.dp)) {
                    if (loopy.running) item("typing") {
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            LoopyFace(22.dp, loopy.state)
                            Text(loopy.label.ifEmpty { "Thinking…" }, style = Type.small, modifier = Modifier.padding(start = 8.dp))
                        }
                    }
                    itemsIndexed(shown, key = { _, message -> message.id }) { i, message ->
                        val before = shown.getOrNull(i + 1)
                        Column(Modifier.fillMaxWidth()) {
                            val date = message.time.atZone(ZoneId.systemDefault()).toLocalDate()
                            if (before == null || before.time.atZone(ZoneId.systemDefault()).toLocalDate() != date) DayLine(date)
                            MessageView(app, message)
                        }
                    }
                    if (loopy.more) item("earlier") { Box(Modifier.fillMaxWidth(), contentAlignment = Alignment.Center) { Pill("Earlier messages") { loopy.earlier() } } }
                }
            }
        }
        LoopyComposer(app)
    }
}

@Composable
internal fun Centered(title: String, detail: String) = Column(Modifier.fillMaxSize().padding(32.dp), verticalArrangement = Arrangement.Center, horizontalAlignment = Alignment.CenterHorizontally) {
    Text(title, style = Type.body.copy(fontWeight = FontWeight.Medium), textAlign = TextAlign.Center)
    Spacer(Modifier.height(6.dp))
    Text(detail, style = Type.small, textAlign = TextAlign.Center)
}

@Composable
private fun DayLine(date: LocalDate) {
    val today = LocalDate.now()
    val text = when (date) { today -> "Today"; today.minusDays(1) -> "Yesterday"; else -> DateTimeFormatter.ofPattern("EEEE d MMMM", Locale.ENGLISH).format(date) }
    Text(text.uppercase(), style = Type.caption.copy(fontSize = 10.sp), textAlign = TextAlign.Center, modifier = Modifier.fillMaxWidth().padding(vertical = 10.dp))
}

@Composable
private fun Bubble(text: String, mine: Boolean, dim: Boolean = false, reaction: String = "", onHold: (() -> Unit)? = null) {
    Column(Modifier.fillMaxWidth(), horizontalAlignment = if (mine) Alignment.End else Alignment.Start) {
        Text(
            text, style = Type.body.copy(color = if (mine) Color.White else Palette.text),
            modifier = Modifier.widthIn(max = 520.dp).fillMaxWidth(0.86f).wrapContentWidth(if (mine) Alignment.End else Alignment.Start)
                .clip(RoundedCornerShape(16.dp)).background(if (mine) loopyMe.copy(alpha = if (dim) 0.5f else 1f) else Palette.raised)
                .combinedClickable(onLongClickLabel = if (onHold != null) "React" else null, onLongClick = onHold, onClick = {})
                .padding(horizontal = 13.dp, vertical = 8.dp),
        )
        if (reaction.isNotEmpty()) Text(
            loopyEmoji(reaction), fontSize = 14.sp,
            modifier = Modifier.padding(horizontal = 8.dp).offset(y = (-7).dp).background(Palette.popover, CircleShape).border(1.dp, Palette.border, CircleShape).padding(horizontal = 6.dp, vertical = 2.dp),
        )
    }
}

@Composable
private fun MessageView(app: RemoteApp, message: LoopyMessage) {
    val loopy = app.loopy
    var reacting by remember { mutableStateOf(false) }
    when {
        message.card != null -> CardView(app, message, message.card)
        message.memory.isNotEmpty() -> Row(Modifier.fillMaxWidth().padding(vertical = 2.dp), verticalAlignment = Alignment.CenterVertically) {
            Icon(Icons.bookmark, null, tint = Palette.amber, modifier = Modifier.size(15.dp))
            Text(message.memory, style = Type.small, modifier = Modifier.weight(1f).padding(horizontal = 8.dp))
            if (message.memoryId.isNotEmpty()) Text("Forget", style = Type.small.copy(color = Palette.textSoft), modifier = Modifier.clip(RoundedCornerShape(8.dp)).clickable { loopy.change("fact.forget", message.memoryId) }.padding(8.dp))
        }
        message.note.isNotEmpty() -> Text(message.note, style = Type.caption, modifier = Modifier.padding(vertical = 2.dp))
        message.mine -> Bubble((listOf(message.text) + message.files.map { "📎 " + it.substringAfterLast('/') }).filter { it.isNotEmpty() }.joinToString("\n"), true, message.pending, message.reaction)
        else -> Box {
            if (loopyMarkup(message.text)) Markdown(message.text, Modifier.padding(vertical = 4.dp))
            else Bubble(message.text, false, reaction = message.reaction) { reacting = true }
            DropdownMenu(reacting, { reacting = false }, containerColor = Palette.popover) {
                for (row in LOOPY_REACTIONS.chunked(5)) Row {
                    for ((name, emoji) in row) Text(emoji, fontSize = 22.sp, modifier = Modifier.clip(CircleShape).clickable {
                        reacting = false
                        loopy.post("react", JSONObject().put("message", message.id).put("reaction", if (message.reaction == name) "" else name))
                    }.padding(9.dp))
                }
            }
        }
    }
}

/** A text that needs more than a bubble (loopyMarkup on the desktop). */
fun loopyMarkup(text: String) = listOf("```", "\n- ", "\n|", "\n1. ", "**", "](http").any { it in text } || text.startsWith("- ") || text.startsWith("1. ")

/** A card: something to act on. */
@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun CardView(app: RemoteApp, message: LoopyMessage, card: LoopyCard) {
    val loopy = app.loopy
    val task = if (card.kind == "delegate") loopy.task(card.task) else null
    val editable = card.waiting && card.editable
    val values = remember(message.id) { mutableStateMapOf<Int, String>() }
    var body by remember(message.id) { mutableStateOf(card.body) }
    var typed by remember(message.id) { mutableStateOf("") }
    val secrets = remember(message.id) { mutableStateMapOf<String, String>() }
    var keep by remember(message.id) { mutableStateOf(false) }
    var hint by remember(message.id) { mutableStateOf("") }
    fun answer(action: String) {
        val fields = if (editable && card.kind != "delegate") card.fields.mapIndexed { i, f -> LoopyField(f.label, values[i] ?: f.value) } else null
        loopy.post("answer", loopyAnswer(message, action, fields, body.takeIf { editable }, typed, secrets.toMap(), keep, hint)) { secrets.clear(); loopy.reload() }
    }
    Column(
        Modifier.fillMaxWidth().widthIn(max = 560.dp).clip(RoundedCornerShape(14.dp)).background(Palette.surface).border(1.dp, Palette.border, RoundedCornerShape(14.dp)).padding(13.dp),
        verticalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Icon(loopyIcon(card.kind), null, tint = Palette.muted, modifier = Modifier.size(18.dp))
            Column(Modifier.weight(1f).padding(horizontal = 9.dp)) {
                Text(card.title, style = Type.body.copy(fontWeight = FontWeight.SemiBold, fontSize = 14.sp))
                if (card.subtitle.isNotEmpty()) Text(card.subtitle, style = Type.small)
            }
            val (text, ink) = when {
                task != null && task.status != "proposed" -> when (task.status) { "running" -> "Working" to Palette.amber; "waiting" -> "Needs you" to Palette.danger; "done" -> "Done" to Palette.added; "failed" -> "Failed" to Palette.muted; else -> "Cancelled" to Palette.muted }
                card.answer.isNotEmpty() -> card.answer to if (card.answer in listOf("Deny", "Stopped", "Expired", "Discarded", "Cancelled", "Not now", "Drop it")) Palette.muted else Palette.added
                card.waiting && card.kind == "needs_you" -> "Needs you" to Palette.danger
                card.waiting -> "Waiting for you" to Palette.amber
                else -> "" to Palette.muted
            }
            if (text.isNotEmpty()) Box(Modifier.widthIn(max = 130.dp)) { Tag(text, ink, ink.copy(alpha = 0.16f)) }
        }
        if (card.warning.isNotEmpty()) Text("⚠ " + card.warning, style = Type.small.copy(color = Palette.danger))
        if (card.kind != "signin" && card.kind != "unlock") card.fields.forEachIndexed { i, field ->
            Column(verticalArrangement = Arrangement.spacedBy(3.dp)) {
                Text(field.label, style = Type.caption)
                if (editable && card.kind != "delegate") Field(values[i] ?: field.value, { values[i] = it }, field.label, KeyboardType.Text)
                else Text(field.value, style = Type.body.copy(fontSize = 14.sp))
            }
        }
        if (editable) TextBox(body, { body = it }, "") else if (card.body.isNotEmpty()) Text(card.body, style = Type.body.copy(fontSize = 14.sp))
        if (card.waiting && card.ask.isNotEmpty()) Field(typed, { typed = it }, card.ask, KeyboardType.Text, password = card.secret)
        if (card.waiting && (card.kind == "signin" || card.kind == "unlock")) {
            for (field in card.fields) Field(
                secrets[field.label].orEmpty(), { secrets[field.label] = it }, when (field.label) { "password" -> "Password"; "master" -> "Master password"; else -> "User name or address" },
                if (field.label == "user") KeyboardType.Email else KeyboardType.Password, password = field.label != "user",
            )
            if (card.keep) {
                Row(verticalAlignment = Alignment.CenterVertically) { Text("Keep in Loopy's vault", style = Type.small, modifier = Modifier.weight(1f)); Toggle(keep, true) { keep = it } }
                if (keep) Field(hint, { hint = it }, "When to use it (work account…), optional", KeyboardType.Text)
            }
            if (card.kind == "signin") Text("It goes straight into the page: Loopy never sees it.", style = Type.caption)
        }
        card.options.forEachIndexed { i, option -> OptionRow(card, option, i) { answer(option.label) } }
        if (card.result.isNotEmpty()) Text(card.result, style = Type.small)
        if (card.waiting && card.actions.isNotEmpty()) FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            card.actions.forEachIndexed { i, action -> Pill(action, primary = i == 0) { answer(action) } }
            if (card.kind == "autolink" && card.link.isNotEmpty()) Pill("See it", icon = Icons.terminal) { app.loopyPage = "link:${card.link}" }
            if (card.kind == "needs_you" && card.ask.isEmpty() && loopy.browser && !loopy.taken) Pill("Take over the browser", icon = Icons.globe) { loopy.change("browser.take") }
        }
        if (task != null && task.status != "proposed") TaskSteps(app, task)
    }
}

@Composable
private fun OptionRow(card: LoopyCard, option: LoopyOption, index: Int, onClick: () -> Unit) {
    val chosen = card.answer == option.label
    val open = card.kind != "summary" && card.answer.isEmpty()
    Row(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).background(if (chosen) Palette.selected else Color.Transparent)
            .then(if (open) Modifier.clickable(onClick = onClick) else Modifier).padding(horizontal = 4.dp, vertical = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (card.kind != "summary") Text(
            "${index + 1}", style = Type.caption.copy(color = if (chosen) Palette.ink else Palette.text), textAlign = TextAlign.Center,
            modifier = Modifier.padding(end = 10.dp).background(if (chosen) Palette.amber else Palette.raised, RoundedCornerShape(6.dp)).padding(horizontal = 7.dp, vertical = 3.dp),
        )
        Column(Modifier.weight(1f)) {
            Text(option.label, style = Type.body.copy(fontSize = 14.sp))
            if (option.detail.isNotEmpty()) Text(option.detail, style = Type.small)
        }
        if (option.aside.isNotEmpty()) Text(option.aside, style = Type.body.copy(fontSize = 14.sp), modifier = Modifier.padding(start = 8.dp))
        if (card.kind == "summary" && option.action.isNotEmpty()) Pill(option.action, onClick = onClick)
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun TaskSteps(app: RemoteApp, task: LoopyTask) {
    val context = LocalContext.current
    Column(verticalArrangement = Arrangement.spacedBy(5.dp)) {
        if (task.steps.size > 7) Text("${task.steps.size - 6} earlier steps", style = Type.caption)
        val steps = if (task.steps.size > 7) task.steps.takeLast(6) else task.steps
        steps.forEachIndexed { i, (at, text) ->
            val ink = when { i < steps.size - 1 -> Palette.added; task.status == "running" -> Palette.amber; task.status == "waiting" || task.status == "failed" -> Palette.danger; else -> Palette.added }
            Row(verticalAlignment = Alignment.CenterVertically) {
                Box(Modifier.size(8.dp).clip(CircleShape).background(ink))
                Text(text, style = Type.small.copy(color = Palette.textSoft), modifier = Modifier.weight(1f).padding(horizontal = 8.dp))
                Text(clock(at), style = Type.caption)
            }
        }
        FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            if (task.chat.isNotEmpty()) Pill("Open the chat") { app.openLoopyTask(task) }
            if (task.status == "running" || task.status == "waiting") Pill("Stop") { app.loopy.change("task.stop", task.id) }
            for (pr in task.prs) Pill(pr, icon = Icons.branch) {
                val (repo, number) = pr.split('#').let { it.first() to it.last() }
                runCatching { context.startActivity(Intent(Intent.ACTION_VIEW, android.net.Uri.parse("https://github.com/$repo/pull/$number"))) }
            }
        }
    }
}

/** A field of several lines. */
@Composable
internal fun TextBox(value: String, onChange: (String) -> Unit, placeholder: String, min: Int = 3) {
    BasicTextField(
        value, onChange, Modifier.fillMaxWidth(), textStyle = Type.body.copy(fontSize = 14.sp), cursorBrush = SolidColor(Palette.amber), minLines = min, maxLines = 14,
        decorationBox = { inner ->
            Box(Modifier.background(Palette.code, RoundedCornerShape(10.dp)).border(1.dp, Palette.border, RoundedCornerShape(10.dp)).padding(12.dp)) {
                if (value.isEmpty()) Text(placeholder, style = Type.body.copy(fontSize = 14.sp, color = Palette.faint))
                inner()
            }
        },
    )
}

@Composable
private fun LoopyComposer(app: RemoteApp) {
    val loopy = app.loopy
    val context = LocalContext.current
    val focus = LocalFocusManager.current
    var text by rememberSaveable { mutableStateOf("") }
    // What is shared to Loopy from another app joins the message being written.
    LaunchedEffect(app.loopyDraft) { app.loopyDraft?.let { text = (text + "\n" + it).trim(); app.loopyDraft = null } }
    val pick = rememberLauncherForActivityResult(ActivityResultContracts.OpenMultipleDocuments()) { uris -> app.loopyGive(uris) }
    val listen = rememberLauncherForActivityResult(ActivityResultContracts.StartActivityForResult()) { result ->
        val said = result.data?.getStringArrayListExtra(RecognizerIntent.EXTRA_RESULTS)?.firstOrNull().orEmpty()
        if (said.isNotBlank()) {
            app.loopySpeak = true // asked aloud, answered aloud
            app.loopySend(said)
        }
    }
    val ready = text.isNotBlank() || app.loopyFiles.isNotEmpty()
    fun send() {
        if (!ready || !app.connected) return
        app.loopySpeak = false
        app.loopySend(text)
        text = ""
        focus.clearFocus()
    }
    Column(Modifier.fillMaxWidth().navigationBarsPadding().imePadding().padding(horizontal = 12.dp).padding(bottom = 10.dp)) {
        loopy.problem?.let { Text(it, style = Type.small.copy(color = Palette.danger), modifier = Modifier.padding(bottom = 6.dp)) }
        if (app.loopyFiles.isNotEmpty()) Text("📎 " + app.loopyFiles.joinToString(", ") { it.substringAfterLast('/') }, style = Type.small, modifier = Modifier.padding(bottom = 6.dp).clickable(onClickLabel = "Remove the files") { app.loopyFiles.clear() })
        Row(
            Modifier.fillMaxWidth().background(Palette.surface, RoundedCornerShape(22.dp)).border(1.dp, Palette.overlay.copy(alpha = 0.11f), RoundedCornerShape(22.dp)).padding(5.dp),
            verticalAlignment = Alignment.Bottom,
        ) {
            RoundButton(Icons.plus, Palette.raised, Palette.muted, description = "Give Loopy a file", size = 36.dp) { pick.launch(arrayOf("*/*")) }
            BasicTextField(
                text, { text = it }, Modifier.weight(1f).padding(horizontal = 10.dp, vertical = 8.dp), textStyle = Type.body, cursorBrush = SolidColor(Palette.amber), maxLines = 6,
                decorationBox = { inner -> Box { if (text.isEmpty()) Text("Message Loopy…", style = Type.body.copy(color = Palette.faint)); inner() } },
            )
            if (loopy.running) RoundButton(Icons.close, Palette.text, Palette.background, description = "Stop Loopy", size = 36.dp, iconSize = 17.dp) { loopy.post("stop", JSONObject()) }
            else if (!ready) RoundButton(Icons.mic, Color.Transparent, Palette.muted, description = "Speak to Loopy", size = 36.dp) {
                try {
                    listen.launch(Intent(RecognizerIntent.ACTION_RECOGNIZE_SPEECH).putExtra(RecognizerIntent.EXTRA_LANGUAGE_MODEL, RecognizerIntent.LANGUAGE_MODEL_FREE_FORM).putExtra(RecognizerIntent.EXTRA_PROMPT, "Speak to Loopy"))
                } catch (_: ActivityNotFoundException) {
                    Toast.makeText(context, "This phone has no speech recognition.", Toast.LENGTH_LONG).show()
                }
            }
            if (ready) { Spacer(Modifier.width(6.dp)); RoundButton(Icons.send, Palette.amber, Palette.ink, description = "Send", size = 36.dp) { send() } }
        }
    }
}

/** Reads aloud what Loopy answers to what was said aloud. */
@Composable
private fun SpeakAnswers(app: RemoteApp) {
    if (!app.loopySpeak) return
    val context = LocalContext.current
    var ready by remember { mutableStateOf(false) }
    val voice = remember { TextToSpeech(context) { ready = it == TextToSpeech.SUCCESS } }
    DisposableEffect(Unit) { onDispose { voice.stop(); voice.shutdown() } }
    val start = remember { app.loopy.messages.lastOrNull()?.id }
    val said = remember { mutableSetOf<String>() }
    LaunchedEffect(app.loopy.running, app.loopy.messages.size, ready) {
        if (!ready || app.loopy.running) return@LaunchedEffect
        delay(150) // the answer's end arrives with its last words
        val from = app.loopy.messages.indexOfFirst { it.id == start }
        for (message in app.loopy.messages.drop(from + 1)) {
            if (message.mine || message.text.isBlank() || !said.add(message.id)) continue
            voice.language = Locale.getDefault()
            voice.speak(message.text.replace(Regex("[*_`#|]"), ""), TextToSpeech.QUEUE_ADD, null, message.id)
        }
    }
}
