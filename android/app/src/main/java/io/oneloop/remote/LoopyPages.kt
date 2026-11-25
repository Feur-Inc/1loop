// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import android.content.ActivityNotFoundException
import android.content.ClipData
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.ClipEntry
import androidx.compose.ui.platform.LocalClipboard
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.launch
import org.json.JSONArray
import org.json.JSONObject
import java.net.URLEncoder

// Loopy's pages on the phone, as on the desktop (internal/ui/loopy_pages.go,
// loopy_settings.go): what it remembers, its folder, a routine, its settings.

@Composable
fun LoopyPage(app: RemoteApp, page: String, onClose: () -> Unit) {
    val onBack = { if (page.startsWith("link:")) { app.loopyTab = "AutoLinks"; app.loopyPage = "settings" } else onClose() }
    val back = predictiveBack(onBack = onBack)
    Column(back.fillMaxSize().background(Palette.background).windowInsetsPadding(WindowInsets.safeDrawing)) {
        val title = when {
            page == "memory" -> "What Loopy remembers"
            page == "files" -> "Loopy's folder"
            page == "settings" -> "Loopy's settings"
            page.startsWith("link:") -> app.loopy.links.firstOrNull { "link:${it.id}" == page }?.name ?: "AutoLink"
            else -> app.loopy.routines.firstOrNull { "routine:${it.id}" == page }?.title ?: "Routine"
        }
        Row(Modifier.padding(12.dp), verticalAlignment = Alignment.CenterVertically) {
            RoundButton(Icons.back, Color.Transparent, Palette.muted, description = "Back to Loopy", onClick = onBack)
            Text(title, style = Type.title, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.padding(start = 12.dp))
        }
        app.loopy.problem?.let { Text(it, style = Type.small.copy(color = Palette.danger), modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp)) }
        Column(Modifier.fillMaxSize().imePadding().verticalScroll(rememberScrollState()).padding(horizontal = 16.dp).padding(bottom = 24.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            when {
                page == "memory" -> MemoryPage(app)
                page == "files" -> FilesPage(app)
                page == "settings" -> LoopySettingsPage(app)
                page.startsWith("link:") -> LinkPage(app, page.removePrefix("link:"), onBack)
                else -> RoutinePage(app, page.removePrefix("routine:"), onBack)
            }
        }
    }
}

@Composable
internal fun Group(title: String, content: @Composable ColumnScope.() -> Unit) {
    if (title.isNotEmpty()) Text(title, style = Type.body.copy(fontWeight = FontWeight.SemiBold), modifier = Modifier.padding(top = 10.dp))
    Column(Modifier.fillMaxWidth().clip(RoundedCornerShape(14.dp)).background(Palette.surface).padding(horizontal = 14.dp, vertical = 8.dp), content = content)
}

/** A line of a page: its title, what it says, and what acts on it. */
@Composable
internal fun Line(title: String, detail: String, control: (@Composable () -> Unit)? = null) {
    Row(Modifier.fillMaxWidth().padding(vertical = 7.dp), verticalAlignment = Alignment.CenterVertically) {
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Text(title, style = Type.body.copy(fontSize = 14.sp))
            if (detail.isNotEmpty()) Text(detail, style = Type.small)
        }
        if (control != null) Box(Modifier.padding(start = 10.dp)) { control() }
    }
}

@Composable
internal fun Link(text: String, onClick: () -> Unit) = Text(
    text, style = Type.small.copy(color = Palette.textSoft, fontWeight = FontWeight.Medium),
    modifier = Modifier.clip(RoundedCornerShape(8.dp)).clickable(onClick = onClick).padding(horizontal = 8.dp, vertical = 8.dp),
)

/** A dialog with fields; [onSave] says what is wrong through its argument, and closes it. */
@Composable
internal fun Form(title: String, save: String, onDismiss: () -> Unit, onSave: (problem: (String) -> Unit) -> Unit, content: @Composable ColumnScope.() -> Unit) {
    var problem by remember { mutableStateOf<String?>(null) }
    GlassBackdrop()
    AlertDialog(
        onDismissRequest = onDismiss, containerColor = GlassSurface, modifier = GlassDialogEdge,
        title = { Text(title, style = Type.title) },
        text = {
            Column(Modifier.verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                content()
                problem?.let { Text(it, style = Type.small.copy(color = Palette.danger)) }
            }
        },
        confirmButton = { Pill(save, primary = true) { problem = null; onSave { problem = it } } },
        dismissButton = { Pill("Cancel", onClick = onDismiss) },
    )
}

/** A value picked in a menu. */
@Composable
internal fun Choose(value: String, values: List<Pair<String, String>>, onPick: (String) -> Unit) {
    var open by remember { mutableStateOf(false) }
    Box {
        Row(
            Modifier.clip(RoundedCornerShape(10.dp)).border(1.dp, Palette.border, RoundedCornerShape(10.dp)).clickable { open = true }.padding(horizontal = 10.dp, vertical = 8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(values.firstOrNull { it.first == value }?.second ?: value, style = Type.small.copy(color = Palette.text), maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.widthIn(max = 130.dp))
            Icon(Icons.chevron, null, tint = Palette.muted, modifier = Modifier.padding(start = 6.dp).size(14.dp))
        }
        DropdownMenu(open, { open = false }, containerColor = Palette.popover) {
            for ((id, name) in values) DropdownMenuItem(
                text = { Text(name, style = Type.body) },
                trailingIcon = { if (id == value) Icon(Icons.check, "Selected", tint = Palette.amber, modifier = Modifier.size(18.dp)) },
                onClick = { open = false; onPick(id) },
            )
        }
    }
}

// -- what it remembers -------------------------------------------------------

@Composable
private fun MemoryPage(app: RemoteApp) {
    val loopy = app.loopy
    var view by remember { mutableStateOf<JSONObject?>(null) }
    var editing by remember { mutableStateOf<LoopyFact?>(null) }
    var adding by remember { mutableStateOf(false) }
    var forgetting by remember { mutableStateOf<LoopyFact?>(null) }
    LaunchedEffect(loopy.revision, app.connected) { if (app.connected) loopy.act { view = app.loopyCall { it.get("/api/loopy/memory") } } }
    Text("Every exchange is kept as it was said, and searched when it matters. Facts are drawn from your own words: what you said, and what you pin, is always in its mind; what it only noticed once comes back when it has to do with your message, as a guess. Passwords and card numbers never go here.", style = Type.small)
    val memory = view ?: return Text("Loading…", style = Type.small)
    val waiting = memory.optInt("waiting")
    Text("${memory.optInt("records")} exchanges kept" + if (waiting > 0) " · $waiting not read for facts yet" else "", style = Type.caption)
    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        Pill("Add something", icon = Icons.plus) { adding = true }
        if (waiting > 0) Pill("Read them now") { loopy.change("learn") }
    }
    val habits = memory.strings("habits")
    if (habits.isNotEmpty()) Group("How you write, as Loopy sees it") { for (habit in habits) Line(habit, "") }
    val facts = memory.objects("facts") { LoopyFact.from(it) }
    for ((kind, name) in LOOPY_KINDS) {
        val shown = facts.filter { it.kind == kind && it.until == null }.reversed()
        if (shown.isNotEmpty()) Group(name) {
            for (fact in shown) Column(Modifier.padding(vertical = 4.dp)) {
                Line(fact.text, fact.detail())
                Row {
                    Link(if (fact.pinned) "Unpin" else "Pin") { loopy.change(JSONObject().put("do", "fact.change").put("id", fact.id).put("on", !fact.pinned)) }
                    Link("Edit") { editing = fact }
                    Link("Forget") { forgetting = fact }
                }
            }
        }
    }
    if (facts.none { it.until == null }) Group("") { Line("Nothing yet", "Tell Loopy “remember that…”, correct it when it does something wrong, or let it notice as you talk.") }
    val ended = facts.filter { it.until != null }.reversed()
    if (ended.isNotEmpty()) Group("No longer true") { for (fact in ended.take(30)) Line(fact.text, "${day(fact.since)} to ${day(fact.until!!)}") { Link("Forget") { forgetting = fact } } }
    val journal = memory.objects("journal") { instant(it.optString("t")) to it.optString("text") }.reversed()
    if (journal.isNotEmpty()) Group("Journal") { for ((at, text) in journal) Line(text, "${day(at)}, ${clock(at)}") }
    if (adding || editing != null) FactForm(app, editing) { adding = false; editing = null }
    forgetting?.let { fact ->
        var also by remember { mutableStateOf(false) }
        Form("Forget this?", "Forget", { forgetting = null }, { loopy.change(JSONObject().put("do", "fact.forget").put("id", fact.id).put("on", also), it) { forgetting = null } }) {
            Text(fact.text, style = Type.body)
            Row(verticalAlignment = Alignment.CenterVertically) { Text("Also erase the exchange it comes from", style = Type.small, modifier = Modifier.weight(1f)); Toggle(also, true) { also = it } }
        }
    }
}

@Composable
private fun FactForm(app: RemoteApp, fact: LoopyFact?, onDone: () -> Unit) {
    var text by remember { mutableStateOf(fact?.text.orEmpty()) }
    var kind by remember { mutableStateOf(fact?.kind ?: "instruction") }
    var pinned by remember { mutableStateOf(fact?.pinned ?: false) }
    Form(if (fact == null) "Something to remember" else "Edit what Loopy remembers", "Save", onDone, { problem ->
        if (text.isBlank()) problem("Write it first")
        else app.loopy.change(JSONObject().put("do", if (fact == null) "fact.add" else "fact.change").put("id", fact?.id.orEmpty()).put("text", text.trim()).put("kind", kind).put("on", pinned), problem, onDone)
    }) {
        TextBox(text, { text = it }, "One sentence: “Never book before 10 am”")
        Row(verticalAlignment = Alignment.CenterVertically) { Text("Kind", style = Type.small, modifier = Modifier.weight(1f)); Choose(kind, LOOPY_KINDS) { kind = it } }
        Row(verticalAlignment = Alignment.CenterVertically) { Text("Pin it", style = Type.small, modifier = Modifier.weight(1f)); Toggle(pinned, true) { pinned = it } }
    }
}

// -- its folder ----------------------------------------------------------------

@Composable
private fun FilesPage(app: RemoteApp) {
    val loopy = app.loopy
    var dir by remember { mutableStateOf("/") }
    var files by remember { mutableStateOf<List<LoopyFile>?>(null) }
    var shown by remember { mutableStateOf<Pair<LoopyFile, ByteArray>?>(null) }
    var removing by remember { mutableStateOf<LoopyFile?>(null) }
    val context = LocalContext.current
    fun encode(path: String) = URLEncoder.encode(path, "UTF-8")
    LaunchedEffect(dir, loopy.revision, app.connected) {
        if (app.connected) loopy.act {
            val text = app.loopyCall { String(it.bytes("/api/loopy/files?path=${encode(dir)}")) }
            val array = JSONArray(text)
            files = List(array.length()) { i -> array.getJSONObject(i).let { LoopyFile(it.optString("path"), it.optBoolean("dir"), it.optLong("size")) } }
        }
    }
    val add = rememberLauncherForActivityResult(ActivityResultContracts.OpenMultipleDocuments()) { uris -> app.loopyUpload(uris, dir) { loopy.reload() } }
    val save = rememberLauncherForActivityResult(ActivityResultContracts.CreateDocument("*/*")) { uri ->
        val data = shown?.second
        if (uri != null && data != null) runCatching { context.contentResolver.openOutputStream(uri)?.use { it.write(data) } }
    }
    Text("What Loopy keeps for as long as it lives: its notes, what you give it, what its scripts make. It is the only place its scripts see.", style = Type.small)
    Row(verticalAlignment = Alignment.CenterVertically) {
        if (dir != "/") Link("‹ ${dir.trimEnd('/').substringBeforeLast('/').ifEmpty { "/" }}") { files = null; dir = dir.trimEnd('/').substringBeforeLast('/').ifEmpty { "/" } }
        Text(dir, style = Type.mono, modifier = Modifier.weight(1f).padding(horizontal = 6.dp))
        Pill("Add files", icon = Icons.plus) { try { add.launch(arrayOf("*/*")) } catch (_: ActivityNotFoundException) { } }
    }
    val list = files ?: return Text("Loading…", style = Type.small)
    if (list.isEmpty()) Text("Empty", style = Type.small)
    else Group("") {
        for (file in list) Row(
            Modifier.fillMaxWidth().clickable {
                if (file.dir) { files = null; dir = file.path.trimEnd('/') }
                else loopy.act { shown = file to app.loopyCall { it.bytes("/api/loopy/file?path=${encode(file.path)}") } }
            }.padding(vertical = 10.dp), verticalAlignment = Alignment.CenterVertically,
        ) {
            Icon(if (file.dir) Icons.folder else Icons.document, null, tint = Palette.muted, modifier = Modifier.size(18.dp))
            Text(file.path.trimEnd('/').substringAfterLast('/'), style = Type.body.copy(fontSize = 14.sp), maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f).padding(horizontal = 10.dp))
            if (!file.dir) { Text(sizeText(file.size), style = Type.caption); Link("Remove") { removing = file } }
        }
    }
    shown?.let { (file, data) ->
        val text = data.takeIf { it.size <= 200_000 && it.none { b -> b == 0.toByte() } }?.let { String(it) }
        Form(file.path.substringAfterLast('/'), "Save to the phone", { shown = null }, { runCatching { save.launch(file.path.substringAfterLast('/')) } }) {
            Text(text ?: "${sizeText(file.size)}: save it to open it.", style = if (text != null) Type.mono else Type.body)
        }
    }
    removing?.let { file -> ConfirmDialog("Remove ${file.path.substringAfterLast('/')}?", "It is removed from Loopy's folder. This can't be undone.", "Remove", { removing = null }) { removing = null; loopy.change(JSONObject().put("do", "file.remove").put("text", file.path)) } }
}

internal fun sizeText(n: Long) = when { n < 1024 -> "$n B"; n < 1 shl 20 -> "${n / 1024} KB"; else -> "%.1f MB".format(n / 1048576.0) }

// -- a routine -----------------------------------------------------------------

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun RoutinePage(app: RemoteApp, id: String, onBack: () -> Unit) {
    val loopy = app.loopy
    val routine = loopy.routines.firstOrNull { it.id == id } ?: return
    val clipboard = LocalClipboard.current
    val scope = rememberCoroutineScope()
    var editing by remember { mutableStateOf(false) }
    var removing by remember { mutableStateOf(false) }
    Group("") {
        Line("On", "It runs by itself, and asks you before anything that matters.") { Toggle(routine.on, app.connected) { loopy.change(if (it) "routine.on" else "routine.off", id) } }
        Line("When", routine.starts())
        Line("Does", routine.task)
        if (routine.watch) Line("Watch", "At night too; it speaks only if something breaks.")
        if (routine.trigger == "webhook") Line("Its address", "POST /api/loopy/hook/${routine.token} on ${loopy.host}. Whoever has it can start this routine.") {
            Link("Copy") { scope.launch { clipboard.setClipEntry(ClipEntry(ClipData.newPlainText("1loop", "/api/loopy/hook/${routine.token}"))) } }
        }
    }
    FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Pill("Run now") { onBack(); loopy.change("routine.run", id) }
        Pill("Edit", icon = Icons.pencil) { editing = true }
        Pill("Remove") { removing = true }
    }
    Group("History") {
        if (routine.runs.isEmpty()) Line("It has not run yet.", "")
        for ((at, why, outcome) in routine.runs.reversed()) Line("${day(at)}, ${clock(at)}", "$why · ${outcome.ifEmpty { "running, or cut short" }}")
    }
    if (removing) ConfirmDialog("Remove this routine?", "Loopy no longer runs it. Its history goes with it.", "Remove", { removing = false }) { removing = false; onBack(); loopy.change("routine.remove", id) }
    if (editing) RoutineForm(app, routine) { editing = false }
}

@Composable
private fun RoutineForm(app: RemoteApp, routine: LoopyRoutine, onDone: () -> Unit) {
    var title by remember { mutableStateOf(routine.title) }
    var task by remember { mutableStateOf(routine.task) }
    var trigger by remember { mutableStateOf(routine.trigger) }
    var schedule by remember { mutableStateOf(routine.whenText) }
    var filter by remember { mutableStateOf(routine.filter) }
    var watch by remember { mutableStateOf(routine.watch) }
    Form("Edit the routine", "Save", onDone, { problem ->
        val changed = JSONObject(routine.json.toString()).put("title", title).put("task", task).put("trigger", trigger).put("when", schedule).put("filter", filter).put("watch", watch)
        app.loopy.change(JSONObject().put("do", "routine.edit").put("routine", changed), problem, onDone)
    }) {
        Field(title, { title = it }, "Title", KeyboardType.Text)
        TextBox(task, { task = it }, "What to do each time")
        Row(verticalAlignment = Alignment.CenterVertically) { Text("Starts", style = Type.small, modifier = Modifier.weight(1f)); Choose(trigger, listOf("" to "On a schedule", "github" to "GitHub", "mail" to "A mail", "webhook" to "A webhook")) { trigger = it } }
        when (trigger) {
            "" -> Field(schedule, { schedule = it }, "daily 08:00 · weekdays 09:30 · every 2h", KeyboardType.Text)
            "github" -> Field(filter, { filter = it }, "owner/name issue · pr · ci", KeyboardType.Text)
            "mail" -> Field(filter, { filter = it }, "Words of the sender or subject, or empty", KeyboardType.Text)
        }
        Row(verticalAlignment = Alignment.CenterVertically) { Text("Watch: at night too, speaks only if something breaks", style = Type.small, modifier = Modifier.weight(1f)); Toggle(watch, true) { watch = it } }
    }
}

// -- its settings --------------------------------------------------------------

@Composable
private fun LoopySettingsPage(app: RemoteApp) {
    val loopy = app.loopy
    LoopyTabs(app.loopyTab) { app.loopyTab = it }
    when (app.loopyTab) {
        "AutoLinks" -> return AutoLinksTab(app)
        "Vault" -> return VaultTab(app)
    }
    var account by remember { mutableStateOf<String?>(null) }
    var removing by remember { mutableStateOf<Pair<String, JSONObject>?>(null) } // what (an action), the entry
    val settings = loopy.settings
    Group("Home") {
        Line("Where Loopy lives", "On this phone, its routines run while the agent here runs. On a server they always go on. Each place has a Loopy of its own: its conversation, memory and folder do not move.")
        Choose(app.loopyHome ?: app.server?.key.orEmpty(), app.linked.map { it.key to it.name }) { app.chooseLoopyHome(it) }
        Spacer(Modifier.height(8.dp))
    }
    val models = loopy.models.map { "${it.connection}\u0000${it.id}" to it.label }
    fun of(connection: String, id: String) = if (settings.optString(id).isEmpty()) "" else "${settings.optString(connection)}\u0000${settings.optString(id)}"
    fun pick(connection: String, id: String, value: String) = loopy.set { it.put(connection, value.substringBefore('\u0000')).put(id, value.substringAfter('\u0000', "")); if (id == "model") it.put("effort", "") }
    Group("Model") {
        Line("Loopy's model", "Its own model, from the connections of where it lives, or the one chosen for your projects.") { Choose(of("connection", "model"), listOf("" to "As in 1loop") + models) { pick("connection", "model", it) } }
        val efforts = loopy.models.firstOrNull { it.id == settings.optString("model") && it.connection == settings.optString("connection") }?.efforts.orEmpty()
        if (efforts.isNotEmpty()) Line("Reasoning effort", "") { Choose(settings.optString("effort"), listOf("" to "The model's default") + efforts.map { it to it.replaceFirstChar(Char::uppercase) }) { value -> loopy.set { it.put("effort", value) } } }
        Line("Memory's model", "It reads what you wrote, from time to time, for facts worth keeping. A small model does.") { Choose(of("learnerConnection", "learner"), listOf("" to "The same as Loopy's") + models) { pick("learnerConnection", "learner", it) } }
    }
    val night = if (settings.optString("quietFrom").isEmpty() && settings.optString("quietTo").isEmpty()) "23:00-07:00"
    else if (settings.optString("quietFrom") == settings.optString("quietTo")) "-" else "${settings.optString("quietFrom")}-${settings.optString("quietTo")}"
    Group("Every day") {
        Line("Morning summary", "What needs you and what is in progress, in a card, once a day.") {
            Choose(settings.optString("summary").ifEmpty { "08:00" }, listOf("06:30", "07:00", "07:30", "08:00", "08:30", "09:00", "10:00").map { it to "At $it" } + ("off" to "Never")) { value -> loopy.set { it.put("summary", value) } }
        }
        Line("Night", "Only a watch that breaks, or a card you must answer, notifies you then.") {
            Choose(night, listOf("22:00-07:00", "23:00-07:00", "23:00-08:00", "00:00-09:00").map { it to it.replace("-", " to ") } + ("-" to "No night")) { value ->
                loopy.set { if (value == "-") it.put("quietFrom", "00:00").put("quietTo", "00:00") else it.put("quietFrom", value.substringBefore('-')).put("quietTo", value.substringAfter('-')) }
            }
        }
        var wants by remember(settings.optString("wants")) { mutableStateOf(settings.optString("wants")) }
        Text("What you want to hear about", style = Type.body.copy(fontSize = 14.sp), modifier = Modifier.padding(vertical = 6.dp))
        TextBox(wants, { wants = it }, "Only what needs me today, and CI on dev…", 2)
        if (wants != settings.optString("wants")) Link("Save") { loopy.set { it.put("wants", wants.trim()) } }
    }
    Group("Memory") {
        Line("What Loopy remembers", "Rules, how you like answers, facts about you, and its journal.") { Link("Open") { app.loopyPage = "memory" } }
        Line("Learn on its own", "On: Loopy also keeps what your words show of your ways. Off: only what you ask it to remember.") { Toggle(!settings.optBoolean("askToRemember"), app.connected) { on -> loopy.set { it.put("askToRemember", !on) } } }
        Line("Journal", "Loopy notes what it did, dated, and searches it later.") { Toggle(!settings.optBoolean("journalOff"), app.connected) { on -> loopy.set { it.put("journalOff", !on) } } }
    }
    Group("Accounts") {
        for (entry in loopy.accounts) Line(entry.optString("address"), if (entry.optString("kind") == "calendar") "Calendar: what it adds is added only from a card you press." else "Mail: what it writes is sent only from a card you press.") { Link("Remove") { removing = "account.remove" to entry } }
        Line("Connect an account", "Mail (IMAP and SMTP) or a calendar (CalDAV), with an app password.")
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) { Pill("Mail", icon = Icons.plus) { account = "mail" }; Pill("Calendar", icon = Icons.plus) { account = "calendar" } }
        Spacer(Modifier.height(6.dp))
    }
    if (loopy.rules.isNotEmpty()) Group("Always allowed") {
        for (rule in loopy.rules) {
            val kind = rule.optString("kind")
            val what = if (kind.startsWith("browser:")) kind.removePrefix("browser:").replaceFirstChar(Char::uppercase) + " on " + rule.optString("site") else if (kind == "script") "Scripts that act on ${rule.optString("site")}" else "Sign in to ${rule.optString("site")} with the vault"
            Line(what, "Allowed without asking since ${day(instant(rule.optString("t")))}") { Link("Ask again") { loopy.change("rule.remove", rule.optString("id")) } }
        }
    }
    account?.let { AccountForm(app, it) { account = null } }
    removing?.let { (what, entry) ->
        val name = entry.optString("name").ifEmpty { entry.optString("address") }
        ConfirmDialog("Remove $name?", "Loopy no longer reaches this account. Its password is forgotten.", "Remove", { removing = null }) { removing = null; loopy.change(what, entry.optString("id")) }
    }
}

@Composable
private fun AccountForm(app: RemoteApp, kind: String, onDone: () -> Unit) {
    var address by remember { mutableStateOf("") }
    var password by remember { mutableStateOf("") }
    var imap by remember { mutableStateOf("") }
    var smtp by remember { mutableStateOf("") }
    var caldav by remember { mutableStateOf("") }
    Form(if (kind == "mail") "Connect a mail account" else "Connect a calendar", "Connect", onDone, { problem ->
        val account = JSONObject().put("kind", kind).put("address", address.trim()).put("password", password).put("imap", imap.trim()).put("smtp", smtp.trim()).put("caldav", caldav.trim())
        app.loopy.change(JSONObject().put("do", "account.save").put("account", account), problem, onDone)
    }) {
        Field(address, { address = it }, "you@example.com", KeyboardType.Email)
        Field(password, { password = it }, "App password", KeyboardType.Password, password = true)
        if (kind == "mail") {
            Field(imap, { imap = it }, "IMAP server (known for the common providers)", KeyboardType.Uri)
            Field(smtp, { smtp = it }, "SMTP server (known for the common providers)", KeyboardType.Uri)
        } else Field(caldav, { caldav = it }, "https://… (known for the common providers)", KeyboardType.Uri)
        Text("Most providers want a password made for apps, in their security settings. It is kept encrypted in Loopy's vault.", style = Type.small)
    }
}
