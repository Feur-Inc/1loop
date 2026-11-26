// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import android.content.ClipData
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.ClipEntry
import androidx.compose.ui.platform.LocalClipboard
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import org.json.JSONArray
import org.json.JSONObject
import java.net.URLEncoder

// The AutoLinks and Vault tabs of Loopy's settings, as on the desktop
// (internal/ui/loopy_links.go).

/** A script of Loopy's own (loopy.AutoLink). */
data class LoopyLink(val json: JSONObject) {
    val id: String get() = json.optString("id")
    val name: String get() = json.optString("name")
    val about: String get() = json.optString("about")
    val input: String get() = json.optString("input")
    val sites get() = json.strings("sites")
    val secrets get() = json.strings("secrets")
    val rights get() = json.strings("rights")
    val libraries get() = json.strings("libraries")
    val whenText: String get() = json.optString("when")
    val does: String get() = json.optString("do")
    val ask get() = json.optBoolean("ask")
    val allowed get() = json.optBoolean("allowed")
    val on get() = json.optBoolean("on")
    val running get() = json.optBoolean("running")
    val waits: String get() = json.optString("waits")
    val failed get() = json.optInt("failed")
    val next get() = json.optJSONObject("next")?.let { LoopyLink(it) }
    val runs get() = json.objects("runs") { LoopyLinkRun(instant(it.optString("t")), it.optString("why"), it.optLong("ms"), it.optString("error"), it.optString("outcome"), it.strings("files")) }

    /** Its state in a word. */
    fun state(): String = when {
        running -> "Running"
        !allowed -> "Not allowed yet"
        next != null -> "A change to allow"
        waits.isNotEmpty() -> "Waits for $waits"
        failed > 0 -> "Failing"
        whenText.isNotEmpty() && !on -> "Off"
        else -> "Ready"
    }

    /** What it is, in a line. */
    fun says(): String = listOfNotNull(
        about, describeWhen(whenText).takeIf { it.isNotEmpty() },
        runs.lastOrNull()?.let { (if (it.error.isEmpty()) "ran " else "failed ") + "${day(it.time)}, ${clock(it.time)}" },
    ).joinToString(" · ")
}

data class LoopyLinkRun(val time: java.time.Instant, val why: String, val took: Long, val error: String, val outcome: String, val files: List<String>)

/** loopy.LinkRights. */
val LOOPY_RIGHTS = listOf(
    "github" to "Read GitHub with your connector", "github_write" to "Write on GitHub in your name",
    "agent" to "Start coding agents in your projects", "act" to "Change things on its sites (send, book, buy…)",
)

private fun stateInk(link: LoopyLink): Color = when (link.state()) {
    "Ready" -> Palette.added
    "Off" -> Palette.muted
    "Failing" -> Palette.danger
    else -> Palette.amber
}

@Composable
internal fun LoopyTabs(tab: String, onPick: (String) -> Unit) = Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
    for (name in listOf("General", "AutoLinks", "Vault")) Pill(name, primary = tab == name) { onPick(name) }
}

@Composable
internal fun AutoLinksTab(app: RemoteApp) {
    val loopy = app.loopy
    Text("Scripts Loopy makes for itself, to reach a service or do a job in one step instead of driving the browser each time. You allow each one, with what it may reach and use. Ask Loopy for one: “make an AutoLink that reads my grades”.", style = Type.small)
    if (loopy.links.isEmpty()) Group("") { Line("No AutoLink yet.", "") }
    else Group("") {
        for (link in loopy.links) Column(Modifier.padding(vertical = 4.dp)) {
            Line(link.name, link.says()) {
                if (link.allowed && link.whenText.isNotEmpty()) Toggle(link.on, app.connected) { loopy.change(if (it) "link.on" else "link.off", link.id) }
            }
            Row(verticalAlignment = Alignment.CenterVertically) {
                val ink = stateInk(link)
                Tag(link.state(), ink, ink.copy(alpha = 0.16f))
                Spacer(Modifier.weight(1f))
                Link("Open") { app.loopyPage = "link:${link.id}" }
            }
        }
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
internal fun LinkPage(app: RemoteApp, id: String, onBack: () -> Unit) {
    val loopy = app.loopy
    val link = loopy.links.firstOrNull { it.id == id } ?: return Text("This AutoLink is gone.", style = Type.small)
    var script by remember { mutableStateOf(false) }
    var editing by remember { mutableStateOf(false) }
    var removing by remember { mutableStateOf(false) }
    var asking by remember { mutableStateOf(false) }
    val ink = stateInk(link)
    Row { Tag(link.state(), ink, ink.copy(alpha = 0.16f)) }
    Text(link.about, style = Type.body.copy(fontSize = 14.sp))
    val shown = link.next ?: link
    fun act(what: String, on: Boolean) = loopy.change(JSONObject().put("do", what).put("id", id).put("on", on))
    if (!link.allowed || link.next != null) {
        Group(if (link.next != null) "Allow this change?" else "Let it run?") {
            Line("Read what it may do below, and its script", "Nothing of it runs before your yes.")
            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Pill("Run by itself", primary = true) { act("link.allow", false) }
                Pill("Ask me each time") { act("link.allow", true) }
            }
            Spacer(Modifier.height(6.dp))
        }
    } else Group("Running") {
        Line("Ask me each time", "On: each run waits for your yes. Off: it runs by itself.") { Toggle(link.ask, app.connected) { act("link.ask", it) } }
        if (link.whenText.isNotEmpty()) Line("On", describeWhen(link.whenText)) { Toggle(link.on, app.connected) { act(if (it) "link.on" else "link.off", it) } }
    }
    Group("What it may do") {
        Line("Reaches", if ("*" in shown.sites) "Any site" else shown.sites.joinToString(", ").ifEmpty { "No site" })
        Line("Uses from the vault", shown.secrets.joinToString(", ").ifEmpty { "Nothing" })
        Line("May", LOOPY_RIGHTS.filter { it.first in shown.rights }.joinToString(", ") { it.second }.ifEmpty { "Nothing more" })
        if (shown.libraries.isNotEmpty()) Line("Libraries", shown.libraries.joinToString(", "))
        if (shown.input.isNotEmpty()) Line("Takes", shown.input)
        if (shown.does.isNotEmpty()) Line("When it finds something, Loopy does", shown.does)
    }
    FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        if (link.allowed) {
            if (link.running) Pill("Stop") { act("link.stop", false) }
            else Pill("Run now") { if (link.input.isEmpty()) loopy.change("link.run", id) else asking = true }
        }
        Pill("Script", icon = Icons.terminal) { script = true }
        Pill("Edit", icon = Icons.pencil) { editing = true }
        Pill("Remove") { removing = true }
    }
    Group("History") {
        val runs = link.runs.reversed()
        if (runs.isEmpty()) Line("It has not run yet.", "")
        for (run in runs.take(15)) {
            val by = when (run.why) { "schedule" -> "by itself"; "you" -> "by you"; "test" -> "a test"; else -> "by ${run.why}" }
            Line("${day(run.time)}, ${clock(run.time)}, $by, %.1f s".format(run.took / 1000.0), run.error.ifEmpty { run.outcome.ifEmpty { "Done" } })
        }
    }
    if (removing) ConfirmDialog("Remove “${link.name}”?", "Its script and what it keeps are deleted. This can't be undone.", "Remove", { removing = false }) { removing = false; onBack(); loopy.change("link.remove", id) }
    if (asking) {
        var text by remember { mutableStateOf("") }
        Form("Run “${link.name}”", "Run", { asking = false }, { problem -> loopy.change(JSONObject().put("do", "link.run").put("id", id).put("text", text), problem) { asking = false } }) {
            Text("It takes: ${link.input}", style = Type.small)
            TextBox(text, { text = it }, "{ }")
        }
    }
    if (script) LinkScript(app, link) { script = false }
    if (editing) LinkForm(app, link) { editing = false }
}

@Composable
private fun LinkScript(app: RemoteApp, link: LoopyLink, onDone: () -> Unit) {
    var code by remember { mutableStateOf<JSONObject?>(null) }
    var text by remember { mutableStateOf("") }
    LaunchedEffect(link.id) {
        app.loopy.act {
            val read = app.loopyCall { it.get("/api/loopy/link?id=${URLEncoder.encode(link.id, "UTF-8")}") }
            text = read.optString("next").ifEmpty { read.optString("code") }
            code = read
        }
    }
    val waits = code?.optString("next").orEmpty().isNotEmpty()
    Form("Script of “${link.name}”", if (waits) "Close" else "Save", onDone, { problem ->
        if (waits || code == null) onDone()
        else app.loopy.change(JSONObject().put("do", "link.edit").put("id", link.id).put("link", link.json).put("text", text), problem, onDone)
    }) {
        Text(if (waits) "The change Loopy asks you to allow." else "Python. Yours to change: your change is allowed as it is.", style = Type.small)
        if (code == null) Text("Loading…", style = Type.small)
        else if (waits) Text(text, style = Type.mono)
        else TextBox(text, { text = it }, "", 10)
    }
}

@Composable
private fun LinkForm(app: RemoteApp, link: LoopyLink, onDone: () -> Unit) {
    var name by remember { mutableStateOf(link.name) }
    var about by remember { mutableStateOf(link.about) }
    var sites by remember { mutableStateOf(link.sites.joinToString(", ")) }
    var secrets by remember { mutableStateOf(link.secrets.joinToString(", ")) }
    var libraries by remember { mutableStateOf(link.libraries.joinToString(", ")) }
    var whenText by remember { mutableStateOf(link.whenText) }
    var does by remember { mutableStateOf(link.does) }
    val rights = remember { mutableStateListOf<String>().apply { addAll(link.rights) } }
    fun list(text: String) = JSONArray(text.split(",").map { it.trim() }.filter { it.isNotEmpty() })
    Form("AutoLink", "Save", onDone, { problem ->
        val changed = JSONObject(link.json.toString()).put("name", name).put("about", about).put("sites", list(sites)).put("secrets", list(secrets))
            .put("libraries", list(libraries)).put("when", whenText.trim()).put("do", does).put("rights", JSONArray(rights.toList()))
        app.loopy.change(JSONObject().put("do", "link.edit").put("id", link.id).put("link", changed), problem, onDone)
    }) {
        Field(name, { name = it }, "Name", KeyboardType.Text)
        TextBox(about, { about = it }, "What it does")
        Text("Reaches", style = Type.caption); Field(sites, { sites = it }, "api.example.com, or * for any site", KeyboardType.Uri)
        Text("From the vault", style = Type.caption); Field(secrets, { secrets = it }, "Names of vault entries", KeyboardType.Text)
        Text("Libraries", style = Type.caption); Field(libraries, { libraries = it }, "requests", KeyboardType.Text)
        Text("Runs", style = Type.caption); Field(whenText, { whenText = it }, "every 5m, daily 08:00… or empty", KeyboardType.Text)
        Text("When it finds something, Loopy does", style = Type.caption); TextBox(does, { does = it }, "")
        for ((right, says) in LOOPY_RIGHTS) Row(verticalAlignment = Alignment.CenterVertically) {
            Text(says, style = Type.small, modifier = Modifier.weight(1f))
            Toggle(right in rights, true) { if (it) rights.add(right) else rights.remove(right) }
        }
    }
}

// -- the vault -----------------------------------------------------------------

@Composable
internal fun VaultTab(app: RemoteApp) {
    val loopy = app.loopy
    var form by remember { mutableStateOf("") }
    var secret by remember { mutableStateOf<JSONObject?>(null) }
    var showing by remember { mutableStateOf<JSONObject?>(null) }
    var removing by remember { mutableStateOf<JSONObject?>(null) }
    var find by remember { mutableStateOf("") }
    Text("A password manager of your own, where Loopy lives. Loopy knows an entry is there, never its value: 1loop types it in the page, or hands it to an AutoLink you allowed.", style = Type.small)
    when (loopy.vaultState) {
        "none" -> Group("") {
            Line("No vault yet", "Choose a master password to make one.")
            Pill("Make it", primary = true) { form = "create" }
            Spacer(Modifier.height(6.dp))
        }
        "locked" -> Group("") {
            Line("Locked", "Its entries show once it is open.")
            Pill("Unlock", primary = true) { form = "unlock" }
            Spacer(Modifier.height(6.dp))
        }
        "unlocked" -> {
            Group("") { Line("Open", "") { Link("Lock now") { loopy.change("vault.lock") } } }
            Field(find, { find = it }, "Search", KeyboardType.Text)
            val shown = loopy.vault.filter { (it.optString("name") + " " + it.optString("site") + " " + it.optString("hint")).contains(find.trim(), ignoreCase = true) }
            Group("") {
                if (shown.isEmpty()) Line(if (find.isBlank()) "Nothing in it yet." else "Nothing matches.", "")
                for (entry in shown) Column(Modifier.padding(vertical = 4.dp)) {
                    val detail = listOf(
                        when (entry.optString("kind")) { "signin" -> "Sign-in of ${entry.optString("site")}"; "key" -> "Key"; else -> "Contact details" },
                        if ("totp" in entry.strings("fields")) "two-step codes" else "", entry.optString("hint"),
                    ).filter { it.isNotEmpty() }.joinToString(" · ")
                    Line(entry.optString("name"), detail)
                    Row { Link("Show") { showing = entry }; Link("Edit") { secret = entry }; Link("Remove") { removing = entry } }
                }
            }
            Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                for ((kind, name) in listOf("signin" to "Sign-in", "key" to "Key", "contact" to "Contact details")) Pill(name, icon = Icons.plus) { secret = JSONObject().put("kind", kind) }
            }
            Group("Safety") {
                Line("Lock it", "An AutoLink that needs it then waits for you to open it.") {
                    Choose(loopy.settings.optInt("vaultLock").toString(), listOf("0" to "When 1loop closes", "5" to "After 5 minutes", "15" to "After 15 minutes", "60" to "After an hour")) { value -> loopy.set { it.put("vaultLock", value.toInt()) } }
                }
                Line("Open by itself there", "After a restart too, without your master password: for AutoLinks that run while you are away. Less safe.") {
                    Toggle(loopy.vaultStays, app.connected) { if (it) form = "stay" else loopy.change(JSONObject().put("do", "vault.stay").put("on", false)) }
                }
                Line("Master password", "") { Link("Change") { form = "master" } }
            }
        }
        else -> Group("") { Line("Not available", "The vault could not be read.") }
    }
    if (form.isNotEmpty()) MasterForm(app, form) { form = "" }
    secret?.let { VaultForm(app, it) { secret = null } }
    showing?.let { VaultShow(app, it) { showing = null } }
    removing?.let { entry -> ConfirmDialog("Remove ${entry.optString("name")}?", "The vault forgets it. This can't be undone.", "Remove", { removing = null }) { removing = null; loopy.change("vault.remove", entry.optString("id")) } }
}

@Composable
private fun MasterForm(app: RemoteApp, what: String, onDone: () -> Unit) {
    var old by remember { mutableStateOf("") }
    var one by remember { mutableStateOf("") }
    var two by remember { mutableStateOf("") }
    val twice = what == "create" || what == "master"
    val (title, save) = when (what) {
        "create" -> "Make your vault" to "Make it"
        "unlock" -> "Open your vault" to "Unlock"
        "stay" -> "Open by itself there" to "Allow"
        else -> "Change the master password" to "Change it"
    }
    Form(title, save, onDone, { problem ->
        if (twice && one != two) problem("The two are not the same.")
        else app.loopy.change(JSONObject().put("do", "vault.$what").put("on", what == "stay").put("values", JSONObject().put("old", old).put("master", one)), problem, onDone)
    }) {
        if (what == "create") Text("One password opens it, kept nowhere: if you forget it, what the vault holds is lost. Everything stays where Loopy lives.", style = Type.small)
        if (what == "stay") Text("Where Loopy lives opens the vault without you, so that AutoLinks sign in at night after a restart. Whoever takes over your session there could open it too.", style = Type.small)
        if (what == "master") Field(old, { old = it }, "The one you have", KeyboardType.Password, password = true)
        Field(one, { one = it }, if (what == "create") "Master password (10 characters or more)" else if (what == "master") "The new one" else "Master password", KeyboardType.Password, password = true)
        if (twice) Field(two, { two = it }, "Again", KeyboardType.Password, password = true)
    }
}

private val VAULT_NAMES = mapOf(
    "user" to "User name", "password" to "Password", "totp" to "Two-step secret", "code" to "Code now", "key" to "Key",
    "note" to "Note", "name" to "Name", "email" to "E-mail", "phone" to "Phone", "address" to "Address",
)

@Composable
private fun VaultForm(app: RemoteApp, entry: JSONObject, onDone: () -> Unit) {
    val known = entry.optString("id").isNotEmpty()
    val kind = entry.optString("kind")
    var site by remember { mutableStateOf(entry.optString("site")) }
    var name by remember { mutableStateOf(entry.optString("name")) }
    var hint by remember { mutableStateOf(entry.optString("hint")) }
    val values = remember { mutableStateMapOf<String, String>() }
    var clear by remember { mutableStateOf(false) } // a password just made is shown
    val scope = rememberCoroutineScope()
    val same = if (known) " (empty: unchanged)" else ""
    Form(when (kind) { "signin" -> "A sign-in"; "key" -> "A key"; else -> "Contact details" }, "Save", onDone, { problem ->
        val changed = JSONObject(entry.toString()).put("site", site.trim()).put("name", name.trim().ifEmpty { site.trim() }).put("hint", hint.trim())
        app.loopy.change(JSONObject().put("do", "vault.save").put("entry", changed).put("values", JSONObject(values.toMap())), problem, onDone)
    }) {
        Text("It stays where Loopy lives, behind your master password. Loopy never sees the values.", style = Type.small)
        if (kind == "signin") Field(site, { site = it }, "Site, as example.com", KeyboardType.Uri)
        Field(name, { name = it }, if (kind == "signin") "Name (the site's by default)" else "Name", KeyboardType.Text)
        @Composable fun value(field: String, label: String, secret: Boolean = false, type: KeyboardType = KeyboardType.Text) =
            Field(values[field].orEmpty(), { values[field] = it }, label, if (secret) KeyboardType.Password else type, password = secret)
        when (kind) {
            "signin" -> {
                value("user", "User name or address$same", type = KeyboardType.Email)
                value("password", "Password$same", !clear)
                if (!known) Link("Make a password") { scope.launch { runCatching { app.loopyCall { it.post("/api/loopy/reveal", JSONObject().put("id", "")) } }.onSuccess { values["password"] = it.optString("password"); clear = true } } }
                value("totp", "Two-step secret or otpauth:// address, optional", true)
            }
            "key" -> value("key", "Key$same", true)
            else -> for (field in listOf("name", "email", "phone", "address")) value(field, VAULT_NAMES.getValue(field) + same)
        }
        value("note", "Note, optional")
        Field(hint, { hint = it }, "When to use it (work account…), optional", KeyboardType.Text)
    }
}

/** An entry's values for the user's eyes, for a minute. */
@Composable
private fun VaultShow(app: RemoteApp, entry: JSONObject, onDone: () -> Unit) {
    var values by remember { mutableStateOf<JSONObject?>(null) }
    val seen = remember { mutableStateListOf<String>() }
    val clipboard = LocalClipboard.current
    val scope = rememberCoroutineScope()
    LaunchedEffect(entry) {
        app.loopy.act { values = app.loopyCall { it.post("/api/loopy/reveal", JSONObject().put("id", entry.optString("id"))) } }
        delay(60_000)
        onDone()
    }
    Form(entry.optString("name"), "Close", onDone, { onDone() }) {
        val shown = values ?: return@Form Text("Loading…", style = Type.small)
        for (field in shown.keys().asSequence().filter { it != "totp" }.sorted()) {
            val value = shown.optString(field)
            val hidden = field in listOf("password", "key") && field !in seen
            Text(VAULT_NAMES[field] ?: field, style = Type.caption)
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(if (hidden) "••••••••••••" else value, style = Type.body.copy(fontFamily = FontFamily.Monospace, fontWeight = FontWeight.Normal), modifier = Modifier.weight(1f))
                if (hidden) Link("Show") { seen.add(field) }
                Link("Copy") {
                    scope.launch {
                        val clip = ClipData.newPlainText("1loop", value)
                        if (android.os.Build.VERSION.SDK_INT >= 33) clip.description.extras = android.os.PersistableBundle().apply { putBoolean(android.content.ClipDescription.EXTRA_IS_SENSITIVE, true) }
                        clipboard.setClipEntry(ClipEntry(clip))
                    }
                }
            }
        }
    }
}
