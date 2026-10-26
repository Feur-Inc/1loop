package io.oneloop.remote

import android.content.Intent
import android.net.Uri
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.layout.windowInsetsPadding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.SideEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONArray
import org.json.JSONObject

// Local phone's connections, as the desktop's Settings → Connections: the
// accounts and APIs this phone's agent uses. Each target keeps its own; a
// key is sent once and never read back.

data class LocalLogin(val state: String, val url: String, val problem: String)

/** A connection as the engine stores it (json), with what can be shown of its access. */
data class LocalConnection(
    val json: JSONObject, val hasKey: Boolean, val signedIn: Boolean, val email: String, val problem: String, val login: LocalLogin?,
) {
    val id: String get() = json.optString("id")
    val name: String get() = json.optString("name")
    val provider: String get() = json.optString("provider")
}

private val providers = listOf(
    "openai" to "ChatGPT account", "claudecode" to "Claude Code", "openrouter" to "OpenRouter",
    "vllm" to "Local vLLM", "compatible" to "OpenAI-compatible API", "chatgptweb" to "ChatGPT (Web)",
)

private fun providerName(id: String) = providers.firstOrNull { it.first == id }?.second ?: id

private const val OPENROUTER = "https://openrouter.ai/api/v1"

private suspend fun RemoteApp.localCall(path: String, body: JSONObject? = null): JSONObject {
    val target = server ?: throw ApiError("The local agent is not running.")
    return withContext(Dispatchers.IO) { if (body == null) Api(target).get(path) else Api(target).post(path, body) }
}

private suspend fun RemoteApp.localConnections(): Pair<List<LocalConnection>, Boolean> {
    val reply = localCall("/api/local/connections")
    val rows = reply.optJSONArray("connections") ?: JSONArray()
    val list = (0 until rows.length()).map { i ->
        val row = rows.getJSONObject(i)
        LocalConnection(
            row.optJSONObject("connection") ?: JSONObject(), row.optBoolean("hasKey"), row.optBoolean("signedIn"),
            row.optString("email"), row.optString("problem"),
            row.optJSONObject("login")?.let { LocalLogin(it.optString("state"), it.optString("url"), it.optString("problem")) },
        )
    }
    return list to reply.optBoolean("claudeInstalled")
}

/** A connection being edited: saved as a whole, by Save connection only. */
private class ConnectionDraft(val original: LocalConnection?) {
    private val base = original?.json ?: JSONObject().put("enabled", true).put("allModels", true)
    var name by mutableStateOf(base.optString("name"))
    var provider by mutableStateOf(base.optString("provider").ifEmpty { "compatible" })
    var baseURL by mutableStateOf(base.optString("baseURL"))
    var enabled by mutableStateOf(base.optBoolean("enabled", true))
    var allModels by mutableStateOf(base.optBoolean("allModels", true))
    val models = mutableStateListOf<String>().apply {
        base.optJSONArray("models")?.let { array -> for (i in 0 until array.length()) add(array.optString(i)) }
    }
    var apiKey by mutableStateOf("") // empty: the saved key stays (ChatGPT (Web): its tunnel key)
    private val web = base.optJSONObject("webSettings") ?: JSONObject()
    var tunnelId by mutableStateOf(web.optString("tunnelId"))
    var plugin by mutableStateOf(web.optString("plugin"))

    private fun savedModels() = base.optJSONArray("models")?.let { array -> (0 until array.length()).map { array.optString(it) } }.orEmpty()

    /** Its service, address or key differ from what is saved. */
    val accessChanged get() = original == null || provider != original.provider || baseURL.trim() != base.optString("baseURL") || apiKey.isNotEmpty() ||
        tunnelId.trim() != web.optString("tunnelId") || plugin.trim() != web.optString("plugin")

    val changed get() = if (original == null) name.isNotBlank() || baseURL.isNotBlank() || apiKey.isNotEmpty() else accessChanged || name != base.optString("name") || enabled != base.optBoolean("enabled", true) ||
        allModels != base.optBoolean("allModels", true) || models.toSet() != savedModels().toSet()

    fun chooseProvider(id: String) {
        if (id == provider) return
        provider = id
        baseURL = if (id == "openrouter") OPENROUTER else if (base.optString("provider") == id) base.optString("baseURL") else ""
        allModels = true // another service: another catalog
        models.clear()
    }

    /** The connection to send: the stored one with these fields, its other settings kept. */
    fun json(): JSONObject = JSONObject(base.toString()).apply {
        put("name", name.trim())
        put("provider", provider)
        put("baseURL", if (provider in listOf("openai", "claudecode", "chatgptweb")) "" else baseURL.trim())
        if (provider == "chatgptweb") put("webSettings", JSONObject(web.toString()).put("tunnelId", tunnelId.trim()).put("plugin", plugin.trim()))
        put("enabled", enabled)
        put("allModels", allModels)
        put("models", JSONArray(models.toList()))
        if (provider != base.optString("provider")) remove("modelOptions")
        if (original == null) put("id", "")
    }
}

@Composable
fun ConnectionsSheet(app: RemoteApp, onDone: () -> Unit) {
    val scope = rememberCoroutineScope()
    var list by remember { mutableStateOf<List<LocalConnection>?>(null) }
    var claudeInstalled by remember { mutableStateOf(false) }
    var problem by remember { mutableStateOf<String?>(null) }
    var editing by remember { mutableStateOf<ConnectionDraft?>(null) }
    suspend fun reload() {
        try {
            val (rows, claude) = app.localConnections()
            list = rows
            claudeInstalled = claude
            problem = null
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            problem = e.message
        }
    }
    LaunchedEffect(Unit) { reload() }
    Dialog(onDismissRequest = {}, properties = DialogProperties(usePlatformDefaultWidth = false, dismissOnClickOutside = false, dismissOnBackPress = false)) {
        Box(Modifier.fillMaxSize().background(Palette.background).windowInsetsPadding(WindowInsets.safeDrawing).imePadding()) {
            val draft = editing
            if (draft == null) {
                BackHandler(onBack = onDone)
                ConnectionList(list, problem, onBack = onDone, onEdit = { editing = ConnectionDraft(it) }, onAdd = { editing = ConnectionDraft(null) })
            } else {
                val current = list?.firstOrNull { it.id == draft.original?.id }
                ConnectionEditor(app, draft, current, claudeInstalled, reload = { reload() }, onClose = {
                    editing = null
                    scope.launch { reload() }
                }, onSaved = { id ->
                    scope.launch {
                        reload()
                        // Stay on it, as saved, so a sign-in or the catalog can follow.
                        list?.firstOrNull { it.id == id }?.let { editing = ConnectionDraft(it) }
                    }
                })
            }
        }
    }
}

@Composable
private fun ConnectionList(list: List<LocalConnection>?, problem: String?, onBack: () -> Unit, onEdit: (LocalConnection) -> Unit, onAdd: () -> Unit) {
    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(horizontal = 20.dp)) {
        Header("Connections", onBack)
        Text("The accounts and APIs this phone's agent uses. A computer or a server keeps its own.", style = Type.small)
        Spacer(Modifier.height(18.dp))
        when {
            list == null && problem == null -> Row(verticalAlignment = Alignment.CenterVertically) {
                WorkRing("wait", 22.dp)
                Spacer(Modifier.width(10.dp))
                Text("Loading…", style = Type.small)
            }
            list.isNullOrEmpty() -> if (problem == null) Text("No connection yet. Add one to get started.", style = Type.small)
            else -> Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                for (connection in list) {
                    Card(Modifier.clickable { onEdit(connection) }) {
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            Column(Modifier.weight(1f)) {
                                Text(connection.name, style = Type.body.copy(fontWeight = FontWeight.Medium), maxLines = 1, overflow = TextOverflow.Ellipsis)
                                Text(
                                    listOf(providerName(connection.provider), accessText(connection)).filter { it.isNotEmpty() }.joinToString(" · ") +
                                        if (connection.json.optBoolean("enabled", true)) "" else " · Off",
                                    style = Type.caption.copy(color = Palette.muted), maxLines = 1, overflow = TextOverflow.Ellipsis,
                                )
                                if (connection.problem.isNotEmpty()) Text(connection.problem, style = Type.caption.copy(color = Palette.danger), maxLines = 2)
                            }
                            Icon(Icons.chevronRight, null, tint = Palette.faint, modifier = Modifier.size(18.dp))
                        }
                    }
                }
            }
        }
        problem?.let { Text(it, style = Type.small.copy(color = Palette.danger), modifier = Modifier.padding(top = 12.dp)) }
        Spacer(Modifier.height(20.dp))
        Pill("Add connection", primary = true, icon = Icons.plus, onClick = onAdd)
        Spacer(Modifier.height(32.dp))
    }
}

private fun accessText(connection: LocalConnection) = when (connection.provider) {
    "openai" -> if (connection.signedIn) connection.email.ifEmpty { "Signed in" } else "Not signed in"
    "claudecode" -> ""
    "chatgptweb" -> if (connection.hasKey) "Tunnel key saved" else ""
    else -> if (connection.hasKey) "Key saved" else ""
}

@Composable
private fun ConnectionEditor(
    app: RemoteApp, draft: ConnectionDraft, current: LocalConnection?, claudeInstalled: Boolean,
    reload: suspend () -> Unit, onClose: () -> Unit, onSaved: (String) -> Unit,
) {
    val scope = rememberCoroutineScope()
    val context = LocalContext.current
    var busy by remember { mutableStateOf(false) }
    var message by remember { mutableStateOf<String?>(null) }
    var failed by remember { mutableStateOf(false) }
    var leaving by remember { mutableStateOf(false) }
    var removing by remember { mutableStateOf(false) }
    var catalog by remember { mutableStateOf<List<Pair<String, String>>?>(null) }
    var search by remember { mutableStateOf("") }
    var catalogGeneration by remember { mutableStateOf(0) } // a catalog asked before a change of type is dropped
    // Closing the editor on purpose ends a pending Claude sign-in; leaving for
    // the browser or the background does not, since the editor stays.
    var claudePending by remember { mutableStateOf(false) }
    val leave = {
        if (claudePending) app.cancelClaudeLogin()
        onClose()
    }
    fun say(text: String?, error: Boolean = false) { message = text; failed = error }
    fun save(closeAfter: Boolean = false) {
        busy = true
        say(null)
        scope.launch {
            try {
                val body = JSONObject().put("connection", draft.json())
                if (draft.apiKey.isNotEmpty()) body.put("apiKey", draft.apiKey)
                val saved = app.localCall("/api/local/connections", body).optJSONObject("connection")
                draft.apiKey = ""
                if (closeAfter) leave() else onSaved(saved?.optString("id").orEmpty())
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                say(e.message ?: "Unable to save the connection.", error = true)
            } finally {
                busy = false
            }
        }
    }
    val close = { if (draft.changed) leaving = true else leave() }
    BackHandler(onBack = close)
    // A sign-in finishes in the browser: follow it until it ends.
    val pending = current?.login?.state == "pending"
    LaunchedEffect(pending) {
        while (pending) {
            delay(2000)
            reload()
        }
    }
    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(horizontal = 20.dp)) {
        Header(if (draft.original == null) "New connection" else draft.original.name, close)
        Caption("Name")
        Field(draft.name, { draft.name = it }, "As it appears in the model menu", KeyboardType.Text)
        Spacer(Modifier.height(16.dp))
        Caption("Type")
        Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            for ((id, label) in providers) Chip(label, draft.provider == id) {
                if (id != draft.provider) catalogGeneration++
                draft.chooseProvider(id)
                catalog = null
            }
        }
        Spacer(Modifier.height(16.dp))
        Card {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text("Enabled", style = Type.body)
                    Text("Its models are offered in the model menu.", style = Type.caption)
                }
                Toggle(draft.enabled, enabled = true) { draft.enabled = it }
            }
        }
        Spacer(Modifier.height(22.dp))
        Caption("Access")
        when (draft.provider) {
            "openai" -> ChatGPTAccess(draft, current, busy) { action ->
                busy = true
                say(null)
                scope.launch {
                    try {
                        when (action) {
                            "login" -> {
                                val url = app.localCall("/api/local/connections/${draft.original!!.id}/login", JSONObject()).optString("url")
                                if (url.isNotEmpty()) openLink(context, url)
                            }
                            "open" -> current?.login?.url?.takeIf { it.isNotEmpty() }?.let { openLink(context, it) }
                            "disconnect" -> app.localCall("/api/local/connections/${draft.original!!.id}/disconnect", JSONObject())
                                .optString("warning").takeIf { it.isNotEmpty() }?.let { say(it) }
                        }
                        reload()
                    } catch (e: CancellationException) {
                        throw e
                    } catch (e: Exception) {
                        say(e.message, error = true)
                    } finally {
                        busy = false
                    }
                }
            }
            "claudecode" -> ClaudeAccess(app, claudeInstalled, current?.signedIn == true, reload) { claudePending = it }
            "chatgptweb" -> WebAccess(app, draft, current)
            else -> Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                if (draft.provider == "openrouter") {
                    Text("Address: $OPENROUTER", style = Type.small)
                } else {
                    Field(draft.baseURL, { draft.baseURL = it }, if (draft.provider == "vllm") "http://192.168.1.20:8000/v1" else "API base URL (…/v1)", KeyboardType.Uri)
                }
                Field(
                    draft.apiKey, { draft.apiKey = it },
                    when {
                        current?.hasKey != true -> "API key"
                        draft.provider == current.provider -> "Saved key kept · type to replace it"
                        else -> "API key · the other service's key is not reused"
                    },
                    KeyboardType.Password, password = true,
                )
            }
        }
        Spacer(Modifier.height(22.dp))
        Caption("Models")
        Card {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text("Show every model", style = Type.body)
                    Text("Turn off to choose them one by one.", style = Type.caption)
                }
                Toggle(draft.allModels, enabled = true) { draft.allModels = it }
            }
        }
        Spacer(Modifier.height(10.dp))
        // The draft's catalog, as on the desktop, before any Save; a ChatGPT
        // account needs its sign-in first, so it must be saved.
        if (draft.provider in listOf("openai", "chatgptweb") && (draft.original == null || draft.original.provider != draft.provider)) {
            Text("Save the connection and sign in to load its models.", style = Type.caption)
        } else {
            Pill("Load models", enabled = !busy) {
                busy = true
                say("Loading the model catalog…")
                val generation = ++catalogGeneration
                val body = JSONObject().put("connection", draft.json())
                if (draft.apiKey.isNotEmpty()) body.put("apiKey", draft.apiKey)
                scope.launch {
                    try {
                        val array = app.localCall("/api/local/connections/models", body).optJSONArray("models") ?: JSONArray()
                        if (generation != catalogGeneration) return@launch // the type changed meanwhile
                        catalog = (0 until array.length()).map { array.getJSONObject(it).let { m -> m.optString("id") to m.optString("name").ifEmpty { m.optString("id") } } }
                        say("${catalog!!.size} models loaded.")
                    } catch (e: CancellationException) {
                        throw e
                    } catch (e: Exception) {
                        say(e.message, error = true)
                    } finally {
                        busy = false
                    }
                }
            }
        }
        catalog?.let { models ->
            Spacer(Modifier.height(10.dp))
            Field(search, { search = it }, "Search the models…", KeyboardType.Text)
            Spacer(Modifier.height(6.dp))
            val query = search.trim().lowercase()
            val matching = models.filter { (id, name) -> query.isEmpty() || "$name $id".lowercase().contains(query) }
            for ((id, name) in matching.take(150)) {
                val included = draft.allModels || id in draft.models
                Row(
                    Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).clickable {
                        if (draft.allModels) { // choosing one model ends "every model"
                            draft.models.clear()
                            draft.models.addAll(models.map { it.first })
                            draft.allModels = false
                        }
                        if (id in draft.models) draft.models.remove(id) else draft.models.add(id)
                    }.padding(horizontal = 12.dp, vertical = 10.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Text(name, style = Type.body.copy(fontSize = 14.5.sp, color = if (included) Palette.text else Palette.muted), maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
                    if (included) Icon(Icons.check, null, tint = Palette.amber, modifier = Modifier.size(18.dp))
                }
            }
            when {
                models.isEmpty() -> Text("The service lists no model.", style = Type.caption)
                matching.isEmpty() -> Text("No model matches.", style = Type.caption)
                matching.size > 150 -> Text("${matching.size - 150} more. Search to narrow the list.", style = Type.caption)
            }
        }
        message?.let { Text(it, style = Type.small.copy(color = if (failed) Palette.danger else Palette.muted), modifier = Modifier.padding(top = 14.dp)) }
        current?.problem?.takeIf { it.isNotEmpty() }?.let { Text(it, style = Type.small.copy(color = Palette.danger), modifier = Modifier.padding(top = 10.dp)) }
        Spacer(Modifier.height(22.dp))
        Row(horizontalArrangement = Arrangement.spacedBy(10.dp), verticalAlignment = Alignment.CenterVertically) {
            Pill("Save connection", primary = true, enabled = !busy && draft.changed && draft.name.isNotBlank()) { save() }
            if (draft.original != null) Pill("Remove", enabled = !busy) { removing = true }
            if (busy) WorkRing("wait", 24.dp)
        }
        Spacer(Modifier.height(32.dp))
    }
    if (leaving) {
        AlertDialog(
            onDismissRequest = { leaving = false }, containerColor = Palette.popover,
            title = { Text("Unsaved changes", style = Type.title) },
            text = { Text("Save this connection before leaving?", style = Type.small) },
            confirmButton = {
                TextButton(enabled = draft.name.isNotBlank(), onClick = { leaving = false; save(closeAfter = true) }) { Text("Save", color = Palette.amber) }
            },
            dismissButton = {
                Row {
                    TextButton(onClick = { leaving = false }) { Text("Stay", color = Palette.muted) }
                    TextButton(onClick = { leaving = false; leave() }) { Text("Discard", color = Palette.danger) }
                }
            },
        )
    }
    if (removing) {
        AlertDialog(
            onDismissRequest = { removing = false }, containerColor = Palette.popover,
            title = { Text("Remove connection?", style = Type.title) },
            text = { Text("Remove “${draft.original?.name}” from this phone? Existing chats are kept.", style = Type.small) },
            confirmButton = {
                TextButton(onClick = {
                    removing = false
                    busy = true
                    scope.launch {
                        try {
                            app.localCall("/api/local/connections", JSONObject().put("connection", draft.original!!.json).put("remove", true))
                            onClose()
                        } catch (e: CancellationException) {
                            throw e
                        } catch (e: Exception) {
                            say(e.message, error = true)
                        } finally {
                            busy = false
                        }
                    }
                }) { Text("Remove", color = Palette.danger) }
            },
            dismissButton = { TextButton(onClick = { removing = false }) { Text("Cancel", color = Palette.muted) } },
        )
    }
}

@Composable
private fun ChatGPTAccess(draft: ConnectionDraft, current: LocalConnection?, busy: Boolean, act: (String) -> Unit) {
    Card {
        Text("Uses your ChatGPT subscription, with a sign-in in your browser.", style = Type.small)
        Spacer(Modifier.height(10.dp))
        val login = current?.login
        when {
            draft.original == null || draft.provider != draft.original.provider -> Text("Save the connection, then sign in.", style = Type.caption)
            login?.state == "pending" -> {
                Text("Waiting for the sign-in in your browser…", style = Type.small.copy(color = Palette.amber))
                Spacer(Modifier.height(10.dp))
                Pill("Open the sign-in page", enabled = !busy) { act("open") }
            }
            current?.signedIn == true -> {
                Text("Signed in" + current.email.takeIf { it.isNotEmpty() }?.let { " as $it" }.orEmpty(), style = Type.small.copy(color = Palette.added))
                Spacer(Modifier.height(10.dp))
                Pill("Disconnect", enabled = !busy) { act("disconnect") }
            }
            else -> {
                if (login?.state == "failed" && login.problem.isNotEmpty()) {
                    Text(login.problem, style = Type.small.copy(color = Palette.danger))
                    Spacer(Modifier.height(10.dp))
                }
                Pill("Continue with ChatGPT", enabled = !busy) { act("login") }
            }
        }
    }
}

/**
 * ChatGPT (Web), as on the desktop: the ChatGPT website in this phone's own
 * browser for the account, its tools through a Secure MCP Tunnel and the
 * plugin added in ChatGPT.
 */
@Composable
private fun WebAccess(app: RemoteApp, draft: ConnectionDraft, current: LocalConnection?) {
    val scope = rememberCoroutineScope()
    val context = LocalContext.current
    val saved = draft.original != null && draft.original.provider == "chatgptweb"
    val profile = "chatgpt-" + current?.json?.optString("authProfile").orEmpty()
    var tunnel by remember { mutableStateOf<String?>(null) }
    var starting by remember { mutableStateOf(false) }
    val path = "/api/local/connections/${draft.original?.id}/web"
    LaunchedEffect(saved) {
        while (saved) { // the tunnel's state, while it is set up
            tunnel = runCatching { app.localCall(path).optString("tunnel") }.getOrNull()
            delay(3000)
        }
    }
    Card {
        Text(
            "Uses the ChatGPT website with your subscription, in a browser of its own on this phone (Browser Use keeps its cookies). " +
                "This is unofficial and can break when ChatGPT changes. ChatGPT's usage limits apply.",
            style = Type.small,
        )
        Spacer(Modifier.height(12.dp))
        if (!saved) {
            Text("Enter the tunnel below, save the connection, then sign in.", style = Type.caption)
            Spacer(Modifier.height(10.dp))
        }
        Text("1. Sign in to ChatGPT.", style = Type.small)
        Spacer(Modifier.height(8.dp))
        Pill("Open ChatGPT sign-in", enabled = saved) { ChatGPTWebActivity.open(context, profile) }
        Spacer(Modifier.height(14.dp))
        Text("2. Create a Secure MCP Tunnel in your OpenAI organization, for this ChatGPT workspace. Use one for this phone only: one 1loop answers per tunnel.", style = Type.small)
        Spacer(Modifier.height(8.dp))
        Pill("Open tunnel settings") { openLink(context, "https://platform.openai.com/settings/organization/tunnels") }
        Spacer(Modifier.height(8.dp))
        Field(draft.tunnelId, { draft.tunnelId = it }, "Tunnel ID (tunnel_…)", KeyboardType.Ascii)
        Spacer(Modifier.height(14.dp))
        Text("3. Create a restricted key with Tunnels Read + Use and paste it here. It stays in a private file on this phone, never in chats or sent to the model.", style = Type.small)
        Spacer(Modifier.height(8.dp))
        Pill("Open API key settings") { openLink(context, "https://platform.openai.com/settings/organization/api-keys") }
        Spacer(Modifier.height(8.dp))
        Field(
            draft.apiKey, { draft.apiKey = it },
            if (current?.hasKey == true && current.provider == "chatgptweb") "Saved key kept · paste to replace it" else "Tunnel key",
            KeyboardType.Password, password = true,
        )
        Spacer(Modifier.height(14.dp))
        Text(
            "4. Save, start the tunnel, then open ChatGPT Plugins: Add → Add custom MCP server. Name: ${draft.plugin.trim().ifEmpty { "1loop" }}. " +
                "Connection: Tunnel, with the ID above. Authentication: No authentication. Tick \"I understand and want to continue\", choose Create as a plugin, then Connect.",
            style = Type.small,
        )
        Spacer(Modifier.height(8.dp))
        Field(draft.plugin, { draft.plugin = it }, "Plugin name · 1loop", KeyboardType.Text)
        Spacer(Modifier.height(8.dp))
        Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            Pill("Start tunnel", enabled = saved && current?.hasKey == true && !starting) {
                starting = true
                scope.launch {
                    tunnel = try {
                        app.localCall(path, JSONObject()).optString("tunnel")
                    } catch (e: CancellationException) {
                        throw e
                    } catch (e: Exception) {
                        e.message
                    } finally {
                        starting = false
                    }
                }
            }
            Pill("Open ChatGPT Plugins", enabled = saved) { ChatGPTWebActivity.open(context, profile, "https://chatgpt.com/plugins") }
        }
        tunnel?.let { Text(it, style = Type.caption, modifier = Modifier.padding(top = 8.dp)) }
        Spacer(Modifier.height(14.dp))
        Text("5. Every chat 1loop opens goes in the ChatGPT project named 1loop, found or created on the first message. The models come from your account.", style = Type.small)
    }
}

@Composable
private fun ClaudeAccess(app: RemoteApp, installed: Boolean, signedIn: Boolean, reload: suspend () -> Unit, onPending: (Boolean) -> Unit) {
    val status by app.local.status.collectAsState()
    val step = status.steps.firstOrNull { it.id == "claudecode" }
    // An install that just ended: read again whether Claude Code is there.
    LaunchedEffect(step?.state) { if (step == null && !installed) reload() }
    Card {
        Text(
            "Uses Claude Code, installed in this phone's Debian, with its own sign-in (your Claude subscription).",
            style = Type.small,
        )
        Spacer(Modifier.height(10.dp))
        when {
            installed -> ClaudeSignIn(app, signedIn, reload, onPending)
            step?.state == StepState.Running -> Row(verticalAlignment = Alignment.CenterVertically) {
                WorkRing("wait", 22.dp)
                Spacer(Modifier.width(10.dp))
                Text(step.detail.ifEmpty { "Installing Claude Code…" }, style = Type.small, maxLines = 2, overflow = TextOverflow.Ellipsis)
            }
            else -> {
                Text("Optional. Claude Code is downloaded only if you install it.", style = Type.caption)
                if (step?.state == StepState.Failed) Text(step.detail, style = Type.small.copy(color = Palette.danger), modifier = Modifier.padding(top = 6.dp))
                Spacer(Modifier.height(10.dp))
                Pill(if (step?.state == StepState.Failed) "Try again" else "Install Claude Code") {
                    runCatching { app.local.requestComponent("claudecode") }.onSuccess { app.prepareLocal("claudecode") }
                }
            }
        }
    }
}

/** Claude's own sign-in (claude auth login --claudeai), followed through the engine. */
@Composable
private fun ClaudeSignIn(app: RemoteApp, signedIn: Boolean, reload: suspend () -> Unit, onPending: (Boolean) -> Unit) {
    val scope = rememberCoroutineScope()
    val context = LocalContext.current
    var state by remember { mutableStateOf("idle") }
    var url by remember { mutableStateOf("") }
    var problem by remember { mutableStateOf<String?>(null) }
    var code by remember { mutableStateOf("") } // in memory only, cleared once sent
    var busy by remember { mutableStateOf(false) }
    var opened by remember { mutableStateOf("") } // the sign-in page already opened
    fun apply(reply: JSONObject) {
        state = reply.optString("state", "idle")
        url = reply.optString("url")
        problem = reply.optString("problem").ifEmpty { null }
    }
    fun send(body: JSONObject) {
        busy = true
        problem = null
        scope.launch {
            try {
                apply(app.localCall("/api/local/claude/login", body))
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                problem = e.message
            } finally {
                busy = false
            }
        }
    }
    SideEffect { onPending(state == "pending") }
    // The state, then while pending its link and its end.
    LaunchedEffect(Unit) { runCatching { apply(app.localCall("/api/local/claude/login")) } }
    LaunchedEffect(state) {
        while (state == "pending") {
            if (url.isNotEmpty() && url != opened) {
                opened = url
                openLink(context, url)
            }
            delay(2000)
            try {
                apply(app.localCall("/api/local/claude/login"))
            } catch (e: CancellationException) {
                throw e
            } catch (_: Exception) {
            }
        }
        if (state == "connected") reload()
    }
    when {
        state == "pending" -> {
            Text("Waiting for the sign-in in your browser…", style = Type.small.copy(color = Palette.amber))
            Spacer(Modifier.height(10.dp))
            if (url.isNotEmpty()) {
                Text("If the page shows a code, paste it here.", style = Type.caption)
                Spacer(Modifier.height(8.dp))
                Field(code, { code = it.trim() }, "Code from the sign-in page", KeyboardType.Ascii)
                Spacer(Modifier.height(10.dp))
            }
            Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                if (code.isNotEmpty()) Pill("Submit code", primary = true, enabled = !busy) {
                    send(JSONObject().put("code", code))
                    code = ""
                }
                if (url.isNotEmpty()) Pill("Open the sign-in page", enabled = !busy) { openLink(context, url) }
                Pill("Cancel", enabled = !busy) { send(JSONObject().put("cancel", true)) }
            }
        }
        signedIn || state == "connected" -> Text("Signed in to Claude.", style = Type.small.copy(color = Palette.added))
        else -> Pill("Sign in with Claude", enabled = !busy) { send(JSONObject()) }
    }
    problem?.let { Text(it, style = Type.small.copy(color = Palette.danger), modifier = Modifier.padding(top = 10.dp)) }
}

private fun openLink(context: android.content.Context, url: String) {
    runCatching { context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url)).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) }
}

@Composable
private fun Header(title: String, onBack: () -> Unit) {
    Row(Modifier.fillMaxWidth().padding(top = 8.dp, bottom = 10.dp), verticalAlignment = Alignment.CenterVertically) {
        RoundButton(Icons.back, Color.Transparent, Palette.muted, description = "Back", onClick = onBack)
        Spacer(Modifier.width(6.dp))
        Text(title, style = Type.title, maxLines = 1, overflow = TextOverflow.Ellipsis)
    }
}

@Composable
private fun Caption(text: String) {
    Text(text, style = Type.small.copy(color = Palette.faint), modifier = Modifier.padding(start = 4.dp, bottom = 6.dp))
}

@Composable
private fun Chip(text: String, selected: Boolean, onClick: () -> Unit) {
    Text(
        text, style = Type.small.copy(color = if (selected) Palette.ink else Palette.text),
        modifier = Modifier.clip(RoundedCornerShape(50)).background(if (selected) Palette.amber else Palette.raised).clickable(onClick = onClick)
            .padding(horizontal = 14.dp, vertical = 8.dp),
    )
}

@Composable
private fun Card(modifier: Modifier = Modifier, content: @Composable () -> Unit) {
    Column(
        Modifier.widthIn(max = 640.dp).fillMaxWidth().clip(RoundedCornerShape(14.dp)).background(Palette.surface)
            .border(1.dp, Palette.borderSoft, RoundedCornerShape(14.dp)).then(modifier).padding(horizontal = 16.dp, vertical = 14.dp),
    ) { content() }
}
