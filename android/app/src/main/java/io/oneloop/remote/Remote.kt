package io.oneloop.remote

import android.content.Context
import android.os.Build
import android.provider.Settings
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import org.json.JSONArray
import org.json.JSONObject

data class ChatSummary(val id: String, val title: String, val workspace: String, val running: Boolean)

data class Item(
    val kind: String, val text: String, val images: Int,
    val tool: String, val verb: String, val target: String, val mono: Boolean,
    val meta: String, val tone: String, val state: String,
    val args: String, val diff: String, val millis: Long, val failed: String,
) {
    companion object {
        fun from(o: JSONObject) = Item(
            o.optString("kind"), o.optString("text"), o.optInt("images"),
            o.optString("tool"), o.optString("verb"), o.optString("target"), o.optBoolean("mono"),
            o.optString("meta"), o.optString("tone"), o.optString("state", "done"),
            o.optString("args"), o.optString("diff"), o.optLong("millis"), o.optString("failed"),
        )
    }
}

data class Task(val id: String, val text: String, val status: String, val started: Long, val completed: Long)

/** A tab of the chat's side panel on the computer. */
data class PanelTab(val key: String, val kind: String, val title: String, val url: String, val index: Int)

data class Approval(val chat: String, val id: String, val title: String, val preview: String)

data class Effort(val value: String, val label: String)

data class ModelOption(val label: String, val name: String, val detail: String, val efforts: List<Effort>)

data class Meta(
    val chat: String, val title: String, val running: Boolean, val label: String, val mark: String,
    val queue: List<String>, val steer: List<String>, val approval: Approval?,
    val model: String, val effort: String, val used: Int, val window: Int,
    val todo: List<Task>,
) {
    companion object {
        fun from(o: JSONObject) = Meta(
            o.optString("chat"), o.optString("title"), o.optBoolean("running"),
            o.optString("label"), o.optString("mark"), strings(o.optJSONArray("queue")), strings(o.optJSONArray("steer")),
            o.optJSONObject("approval")?.let { Approval(it.optString("chat"), it.optString("id"), it.optString("title"), it.optString("preview")) },
            o.optString("model"), o.optString("effort"), o.optInt("used"), o.optInt("window"),
            o.optJSONArray("todo").let { array ->
                (0 until (array?.length() ?: 0)).map { i ->
                    array!!.getJSONObject(i).let { Task(it.optString("id"), it.optString("text"), it.optString("status"), it.optLong("started"), it.optLong("completed")) }
                }
            },
        )
    }
}

private fun strings(array: JSONArray?) = (0 until (array?.length() ?: 0)).map { array!!.optString(it) }

sealed interface Screen {
    data object Launch : Screen
    data object Servers : Screen
    data class Pair(val server: Server, val pairing: String) : Screen
    data object Chat : Screen
}

/**
 * Everything the app shows. Network calls run on IO; state changes happen on
 * the main thread, in the order the computer sent them.
 */
class RemoteApp(private val context: Context, private val scope: CoroutineScope) {
    private val prefs = context.getSharedPreferences("1loop", Context.MODE_PRIVATE)

    var screen by mutableStateOf<Screen>(Screen.Launch)
    val servers = mutableStateListOf<Server>()
    val online = mutableStateListOf<String>() // keys of the servers that answered the last scan
    var scanning by mutableStateOf(false)
    var problem by mutableStateOf<String?>(null)

    var server by mutableStateOf<Server?>(null)
    var connected by mutableStateOf(false)
    val chats = mutableStateListOf<ChatSummary>()
    var projects by mutableStateOf(listOf<String>())
    var workspace by mutableStateOf("")
    val models = mutableStateListOf<ModelOption>()
    var modelsLoading by mutableStateOf(false)
    var defaultModel by mutableStateOf("") // the model a new chat starts with
    var defaultEffort by mutableStateOf("")
    var defaultWindow by mutableStateOf(0)
    var approvalMode by mutableStateOf("approval")
    var autoMode by mutableStateOf(false) // Claude Code's Auto mode exists for the model in use
    var current by mutableStateOf<String?>(null) // null: a new chat
    val items = mutableStateListOf<Item>()
    var meta by mutableStateOf<Meta?>(null)
    val approvals = mutableStateMapOf<String, Approval>() // by chat
    var busy by mutableStateOf(false)

    private var events: Job? = null

    init {
        servers.addAll(saved())
    }

    // -- known computers --------------------------------------------------

    private fun saved(): List<Server> = runCatching {
        val array = JSONArray(prefs.getString("servers", "[]"))
        (0 until array.length()).map { i ->
            array.getJSONObject(i).let { Server(it.getString("name"), it.getString("host"), it.getInt("port"), it.optString("token")) }
        }
    }.getOrDefault(emptyList())

    private fun store() {
        val array = JSONArray()
        servers.filter { it.paired }.forEach {
            array.put(JSONObject().put("name", it.name).put("host", it.host).put("port", it.port).put("token", it.token))
        }
        prefs.edit().putString("servers", array.toString()).apply()
    }

    private fun upsert(server: Server) {
        val serverIndex = servers.indexOfFirst { it.key == server.key || (it.name == server.name && it.port == server.port) }
        if (serverIndex >= 0) servers[serverIndex] = server.copy(token = server.token.ifEmpty { servers[serverIndex].token }) else servers.add(server)
        store()
    }

    fun forget(server: Server) {
        servers.removeAll { it.key == server.key }
        store()
    }

    /** After the launch animation: back to the last computer, or the list. */
    fun started() {
        val last = prefs.getString("last", null)
        val known = servers.firstOrNull { it.key == last && it.paired }
        if (known != null) connect(known) else {
            screen = Screen.Servers
            scan()
        }
    }

    fun scan() {
        if (scanning) return
        scanning = true
        scope.launch {
            val found = runCatching { discover(context) }.getOrDefault(emptyList())
            online.clear()
            for (s in found) {
                // A known computer keeps its token when its address changed.
                val known = servers.firstOrNull { it.key == s.key || (it.name == s.name && it.port == s.port) }
                upsert(s.copy(token = known?.token.orEmpty()))
                online.add(s.key)
            }
            scanning = false
        }
    }

    fun addManually(address: String) {
        val text = address.trim().removePrefix("http://").trimEnd('/')
        if (text.isEmpty()) return
        val host = text.substringBefore(':')
        val port = text.substringAfter(':', "").toIntOrNull() ?: DEFAULT_PORT
        scope.launch {
            problem = null
            val probe = Server(host, host, port)
            val info = runCatching { withContext(Dispatchers.IO) { Api(probe).get("/api/info") } }.getOrNull()
            if (info == null) {
                problem = "No 1loop answered at $host:$port."
                return@launch
            }
            val server = probe.copy(name = info.optString("name", host))
            upsert(server)
            online.add(server.key)
            choose(server)
        }
    }

    // -- pairing -------------------------------------------------------------

    fun choose(server: Server) {
        problem = null
        if (server.paired) connect(server) else pair(server)
    }

    private fun deviceName(): String =
        Settings.Global.getString(context.contentResolver, "device_name") ?: "${Build.MANUFACTURER} ${Build.MODEL}"

    fun pair(server: Server) {
        scope.launch {
            busy = true
            try {
                val reply = withContext(Dispatchers.IO) { Api(server).post("/api/pair", JSONObject().put("device", deviceName())) }
                screen = Screen.Pair(server, reply.getString("pairing"))
            } catch (e: Exception) {
                problem = e.message
            } finally {
                busy = false
            }
        }
    }

    fun confirm(pair: Screen.Pair, code: String) {
        scope.launch {
            busy = true
            problem = null
            try {
                val reply = withContext(Dispatchers.IO) {
                    Api(pair.server).post("/api/pair/confirm", JSONObject().put("pairing", pair.pairing).put("code", code))
                }
                val server = pair.server.copy(token = reply.getString("token"))
                upsert(server)
                connect(server)
            } catch (e: Exception) {
                problem = e.message
            } finally {
                busy = false
            }
        }
    }

    // -- connection ----------------------------------------------------------

    fun connect(target: Server) {
        events?.cancel()
        server = target
        connected = false
        chats.clear()
        items.clear()
        meta = null
        current = null
        approvals.clear()
        prefs.edit().putString("last", target.key).apply()
        screen = Screen.Chat
        events = scope.launch {
            var wait = 500L
            while (true) {
                try {
                    Api(target).events(
                        onOpen = {
                            wait = 500L
                            scope.launch { connected = true; refresh() }
                        },
                        onEvent = { name, data -> scope.launch { handle(name, data) } },
                    )
                } catch (e: CancellationException) {
                    throw e
                } catch (e: ApiError) {
                    if (e.status == 401) { // the computer removed this phone
                        withContext(Dispatchers.Main) {
                            forget(target)
                            upsert(target.copy(token = ""))
                            problem = "${target.name} no longer knows this phone. Pair it again."
                            leave()
                        }
                        return@launch
                    }
                } catch (_: Exception) {
                }
                connected = false
                delay(wait)
                wait = (wait * 2).coerceAtMost(8000)
            }
        }
    }

    fun leave() {
        events?.cancel()
        events = null
        connected = false
        prefs.edit().remove("last").apply()
        screen = Screen.Servers
        scan()
    }

    private suspend fun refresh() {
        val target = server ?: return
        runCatching {
            val state = withContext(Dispatchers.IO) { Api(target).get("/api/state") }
            chats.clear()
            chats.addAll(parseChats(state.optJSONArray("chats")))
            projects = strings(state.optJSONArray("projects"))
            workspace = state.optString("workspace")
            state.optJSONObject("settings")?.let { applySettings(it) }
            if (current == null) current = state.optString("current").ifEmpty { null } ?: chats.firstOrNull()?.id
            current?.let { load(it) }
        }
    }

    private fun parseChats(array: JSONArray?) = (0 until (array?.length() ?: 0)).map { i ->
        array!!.getJSONObject(i).let { ChatSummary(it.getString("id"), it.optString("title"), it.optString("workspace"), it.optBoolean("running")) }
    }

    private suspend fun load(id: String) {
        val target = server ?: return
        val chat = runCatching { withContext(Dispatchers.IO) { Api(target).get("/api/chats/$id") } }.getOrNull() ?: return
        if (current != id) return
        val array = chat.optJSONArray("items")
        items.clear()
        items.addAll((0 until (array?.length() ?: 0)).map { Item.from(array!!.getJSONObject(it)) })
        chat.optJSONObject("meta")?.let { applyMeta(Meta.from(it)) }
    }

    fun open(id: String?) {
        current = id
        items.clear()
        meta = null
        if (id != null) scope.launch { load(id) }
    }

    private fun applySettings(settings: JSONObject) {
        val array = settings.optJSONArray("models")
        models.clear()
        for (modelIndex in 0 until (array?.length() ?: 0)) {
            val model = array!!.getJSONObject(modelIndex)
            val efforts = model.optJSONArray("efforts")
            models.add(ModelOption(
                model.optString("label"), model.optString("name"), model.optString("detail"),
                (0 until (efforts?.length() ?: 0)).map { efforts!!.getJSONObject(it).let { effort -> Effort(effort.optString("value"), effort.optString("label")) } },
            ))
        }
        defaultModel = settings.optString("model")
        defaultEffort = settings.optString("effort")
        defaultWindow = settings.optInt("window")
        approvalMode = settings.optString("approvalMode", "approval")
        autoMode = settings.optBoolean("autoMode")
        modelsLoading = settings.optBoolean("loading")
    }

    /** The model of the chat on screen (or of a new chat), with its effort. */
    val chatModel: ModelOption? get() = models.firstOrNull { it.label == (meta?.model?.takeIf { current != null } ?: defaultModel) }
    val chatEffort: String get() = meta?.effort?.takeIf { current != null } ?: defaultEffort

    private fun applyMeta(m: Meta) {
        if (m.approval != null) approvals[m.chat] = m.approval else approvals.remove(m.chat)
        if (m.chat == current) meta = m
    }

    private fun handle(name: String, data: JSONObject) {
        when (name) {
            "chats" -> {
                val list = parseChats(data.optJSONArray("chats"))
                chats.clear()
                chats.addAll(list)
            }
            "item" -> {
                if (data.optString("chat") != current) return
                val index = data.optInt("index")
                val item = Item.from(data.getJSONObject("item"))
                when {
                    index < items.size -> items[index] = item
                    index == items.size -> items.add(item)
                    else -> current?.let { scope.launch { load(it) } } // missed some: reload
                }
            }
            "meta" -> applyMeta(Meta.from(data))
            "replace" -> if (data.optString("chat") == current) {
                current?.let { scope.launch { load(it) } }
            }
            "settings" -> applySettings(data)
        }
    }

    // -- actions -------------------------------------------------------------

    fun send(text: String, mode: String = "send", project: String? = null) {
        val target = server ?: return
        scope.launch {
            problem = null
            try {
                val body = JSONObject().put("chat", current.orEmpty()).put("text", text).put("mode", mode)
                    .put("workspace", project ?: workspace)
                val reply = withContext(Dispatchers.IO) { Api(target).post("/api/send", body) }
                val id = reply.optString("chat")
                if (current == null && id.isNotEmpty()) open(id)
            } catch (e: Exception) {
                problem = e.message
            }
        }
    }

    fun setModel(label: String? = null, effort: String? = null) {
        val target = server ?: return
        // Shown at once; the computer confirms through its events.
        if (current == null) {
            label?.let { defaultModel = it }
            effort?.let { defaultEffort = it }
        } else meta = meta?.let { it.copy(model = label ?: it.model, effort = effort ?: it.effort) }
        scope.launch {
            runCatching {
                val body = JSONObject().put("chat", current.orEmpty()).put("label", label.orEmpty()).put("effort", effort.orEmpty())
                withContext(Dispatchers.IO) { Api(target).post("/api/model", body) }
            }.onFailure { problem = it.message }
        }
    }

    fun chooseApprovalMode(mode: String) {
        val target = server ?: return
        approvalMode = mode
        scope.launch {
            runCatching { withContext(Dispatchers.IO) { Api(target).post("/api/approval-mode", JSONObject().put("mode", mode)) } }
                .onFailure { problem = it.message }
        }
    }

    fun stop() {
        val target = server ?: return
        val id = current ?: return
        scope.launch { runCatching { withContext(Dispatchers.IO) { Api(target).post("/api/stop", JSONObject().put("chat", id)) } } }
    }

    // -- the chat's side panel ------------------------------------------------

    /** The panel's tabs on the computer; null when it cannot be reached. */
    suspend fun panelTabs(chat: String): List<PanelTab>? {
        val target = server ?: return null
        return runCatching {
            val reply = withContext(Dispatchers.IO) { Api(target).get("/api/chats/$chat/panel") }
            val array = reply.optJSONArray("tabs")
            (0 until (array?.length() ?: 0)).map { i ->
                array!!.getJSONObject(i).let { PanelTab(it.optString("key"), it.optString("kind"), it.optString("title"), it.optString("url"), it.optInt("index")) }
            }
        }.getOrNull()
    }

    suspend fun panelTab(chat: String, key: String): JSONObject? {
        val target = server ?: return null
        return runCatching { withContext(Dispatchers.IO) { Api(target).get("/api/chats/$chat/panel/$key") } }.getOrNull()
    }

    suspend fun panelScreen(chat: String, key: String): ByteArray? {
        val target = server ?: return null
        return runCatching { withContext(Dispatchers.IO) { Api(target).bytes("/api/chats/$chat/panel/$key/screen") } }.getOrNull()
    }

    /** Input for a browser or a terminal tab; see remote_panel.go. */
    fun panelInput(chat: String, key: String, action: String, text: String = "", x: Float = 0f, y: Float = 0f, dy: Float = 0f) {
        val target = server ?: return
        scope.launch {
            inputs.withLock { sendInput(target, chat, key, action, text, x, y, dy) }
        }
    }

    // One at a time, in order: a tap then typing must reach the page so.
    private val inputs = Mutex()

    private suspend fun sendInput(target: Server, chat: String, key: String, action: String, text: String, x: Float, y: Float, dy: Float) {
        runCatching {
            val body = JSONObject().put("action", action).put("text", text).put("x", x.toDouble()).put("y", y.toDouble()).put("dy", dy.toDouble())
            withContext(Dispatchers.IO) { Api(target).post("/api/chats/$chat/panel/$key/input", body) }
        }.onFailure { problem = it.message }
    }

    fun answer(approval: Approval, allow: Boolean) {
        val target = server ?: return
        approvals.remove(approval.chat)
        scope.launch {
            runCatching {
                withContext(Dispatchers.IO) { Api(target).post("/api/approve", JSONObject().put("id", approval.id).put("allow", allow)) }
            }.onFailure { problem = it.message }
        }
    }
}
