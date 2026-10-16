package io.oneloop.remote

import androidx.core.content.edit
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

data class QuestionOption(val label: String, val description: String)

data class Question(val id: String, val header: String, val text: String, val options: List<QuestionOption>)

/** The model's questions (request_user_input); answered with POST /api/answer. */
data class Questions(val chat: String, val id: String, val questions: List<Question>) {
    companion object {
        /** Prefixes an answer written rather than picked, as the computer expects. */
        const val OTHER = "Other: "

        fun from(o: JSONObject) = Questions(o.optString("chat"), o.optString("id"), o.optJSONArray("questions").let { array ->
            (0 until (array?.length() ?: 0)).map { i ->
                array!!.getJSONObject(i).let { q ->
                    val options = q.optJSONArray("options")
                    Question(q.optString("id"), q.optString("header"), q.optString("question"), (0 until (options?.length() ?: 0)).map { j ->
                        options!!.getJSONObject(j).let { QuestionOption(it.optString("label"), it.optString("description")) }
                    })
                }
            }
        })
    }
}

data class Effort(val value: String, val label: String, val about: String)

/** A speed the ChatGPT plan offers for a model (Fast, Ultrafast…). */
data class ServiceTier(val id: String, val name: String, val description: String)

/**
 * account: the ID of its connection (empty from a computer older than this app).
 * tiers: the plan's speeds for it, none on most plans; defaultTier is used unless told.
 */
data class ModelOption(
    val label: String, val name: String, val detail: String, val account: String, val efforts: List<Effort>,
    val tiers: List<ServiceTier> = emptyList(), val defaultTier: String = "",
)

/** A connection of the model picker; problem says why its models are missing. */
data class Account(val id: String, val name: String, val provider: String, val kind: String, val problem: String)

/** How much of a usage window is left, in percent; reset in Unix milliseconds (0: unknown). */
data class LimitWindow(val label: String, val left: Float, val reset: Long)

data class AccountLimits(val note: String, val windows: List<LimitWindow>)

data class Meta(
    val chat: String, val title: String, val running: Boolean, val label: String, val mark: String,
    val queue: List<String>, val steer: List<String>, val approval: Approval?, val question: Questions?,
    val model: String, val effort: String, val used: Int, val window: Int,
    val todo: List<Task>, val tier: String = "",
) {
    companion object {
        fun from(o: JSONObject) = Meta(
            o.optString("chat"), o.optString("title"), o.optBoolean("running"),
            o.optString("label"), o.optString("mark"), strings(o.optJSONArray("queue")), strings(o.optJSONArray("steer")),
            o.optJSONObject("approval")?.let { Approval(it.optString("chat"), it.optString("id"), it.optString("title"), it.optString("preview")) },
            o.optJSONObject("question")?.let { Questions.from(it) },
            o.optString("model"), o.optString("effort"), o.optInt("used"), o.optInt("window"),
            o.optJSONArray("todo").let { array ->
                (0 until (array?.length() ?: 0)).map { i ->
                    array!!.getJSONObject(i).let { Task(it.optString("id"), it.optString("text"), it.optString("status"), it.optLong("started"), it.optLong("completed")) }
                }
            },
            o.optString("tier"),
        )
    }
}

private fun strings(array: JSONArray?) = (0 until (array?.length() ?: 0)).map { array!!.optString(it) }

sealed interface Screen {
    data object Launch : Screen
    data object Servers : Screen
    data class Pair(val server: Server, val pairing: String, val consoleCode: String = "") : Screen
    data object Chat : Screen
}

/**
 * Everything the app shows. Network calls run on IO; state changes happen on
 * the main thread, in the order the computer sent them.
 */
private const val PAGE = 80 // items a chat opens with, and each earlier page

class RemoteApp(private val context: Context, private val scope: CoroutineScope) {
    private val prefs = context.getSharedPreferences("1loop", Context.MODE_PRIVATE)
    private val notifier = Notifier(context)
    private val running = mutableMapOf<String, Boolean>() // by chat, to tell when a run ends

    /** The app is on screen; notifications are only for when it is not. */
    var foreground = true

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
    val accounts = mutableStateListOf<Account>()
    val limits = mutableStateMapOf<String, AccountLimits>() // by account
    var modelsLoading by mutableStateOf(false)
    var defaultModel by mutableStateOf("") // the model a new chat starts with
    var defaultEffort by mutableStateOf("")
    var defaultTier by mutableStateOf("")
    var defaultWindow by mutableStateOf(0)
    var approvalMode by mutableStateOf("approval")
    var autoMode by mutableStateOf(false) // the computer offers Auto mode
    var current by mutableStateOf<String?>(null) // null: a new chat
    val items = mutableStateListOf<Item>()
    /** The index of items[0] in the chat: a long chat opens on its last page. */
    var firstItem by mutableStateOf(0)
    private var loadingEarlier = false
    var meta by mutableStateOf<Meta?>(null)
    val approvals = mutableStateMapOf<String, Approval>() // by chat
    val questions = mutableStateMapOf<String, Questions>() // by chat
    var busy by mutableStateOf(false)

    private var events: Job? = null

    init {
        servers.addAll(saved())
    }

    // -- known computers --------------------------------------------------

    private fun saved(): List<Server> = runCatching {
        val array = JSONArray(prefs.getString("servers", "[]"))
        (0 until array.length()).map { i ->
            array.getJSONObject(i).let { Server(it.getString("name"), it.getString("host"), it.getInt("port"), it.optString("token"), it.optString("scheme", "http"), it.optString("fingerprint")) }
        }
    }.getOrDefault(emptyList())

    private fun store() {
        val array = JSONArray()
        servers.filter { it.paired }.forEach {
            array.put(JSONObject().put("name", it.name).put("host", it.host).put("port", it.port).put("token", it.token).put("scheme", it.scheme).put("fingerprint", it.fingerprint))
        }
        prefs.edit { putString("servers", array.toString()) }
    }

    private fun upsert(server: Server) {
        val serverIndex = servers.indexOfFirst { it.key == server.key }
        if (serverIndex >= 0) servers[serverIndex] = server.copy(token = server.token.ifEmpty { if (server.fingerprint == servers[serverIndex].fingerprint) servers[serverIndex].token else "" }) else servers.add(server)
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
                val known = servers.firstOrNull { it.key == s.key || (it.scheme == "http" && it.name == s.name && it.port == s.port) }
                upsert(s.copy(token = known?.token.orEmpty()))
                online.add(s.key)
            }
            scanning = false
        }
    }

    fun addManually(address: String, fingerprint: String = "") {
        scope.launch {
            problem = null
            try {
                val text = address.trim().trimEnd('/').let { if (it.contains("://")) it else "https://$it" }
                val url = java.net.URL(text)
                require(url.protocol in listOf("http", "https") && url.userInfo == null && url.path.isEmpty() && url.query == null && url.ref == null) { "Enter a server address without a path." }
                val pin = fingerprint.trim().replace(":", "").lowercase()
                require(pin.isEmpty() || (url.protocol == "https" && pin.matches(Regex("[0-9a-f]{64}")))) { "Enter the complete SHA-256 fingerprint from the server console." }
                val port = if (url.port > 0) url.port else if (address.trim().contains("://")) url.defaultPort else DEFAULT_PORT
                val probe = Server(url.host, url.host, port, scheme = url.protocol, fingerprint = pin)
                val info = withContext(Dispatchers.IO) { Api(probe).get("/api/info") }
                val target = probe.copy(name = info.optString("name", url.host))
                upsert(target)
                online.add(target.key)
                choose(servers.first { it.key == target.key })
            } catch (e: Exception) { problem = e.message }
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
                screen = Screen.Pair(server, reply.getString("pairing"), reply.optString("code").takeIf { reply.optBoolean("console") }.orEmpty())
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
        questions.clear()
        prefs.edit { putString("last", target.key) }
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
        prefs.edit { remove("last") }
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
        val chat = runCatching { withContext(Dispatchers.IO) { Api(target).get("/api/chats/$id?limit=$PAGE") } }.getOrNull() ?: return
        if (current != id) return
        val array = chat.optJSONArray("items")
        items.clear()
        items.addAll((0 until (array?.length() ?: 0)).map { Item.from(array!!.getJSONObject(it)) })
        firstItem = chat.optInt("first", 0)
        chat.optJSONObject("meta")?.let { applyMeta(Meta.from(it)) }
    }

    /** Loads the page before the first item shown; returns how many came. */
    suspend fun loadEarlier(): Int {
        val target = server ?: return 0
        val id = current ?: return 0
        if (firstItem <= 0 || loadingEarlier) return 0
        loadingEarlier = true
        try {
            val chat = runCatching { withContext(Dispatchers.IO) { Api(target).get("/api/chats/$id?limit=$PAGE&before=$firstItem") } }.getOrNull() ?: return 0
            if (current != id) return 0
            val array = chat.optJSONArray("items") ?: return 0
            val earlier = (0 until array.length()).map { Item.from(array.getJSONObject(it)) }
            items.addAll(0, earlier)
            firstItem = chat.optInt("first", 0)
            return earlier.size
        } finally {
            loadingEarlier = false
        }
    }

    fun open(id: String?) {
        current = id
        items.clear()
        firstItem = 0
        meta = null
        if (id != null) scope.launch { load(id) }
    }

    private fun applySettings(settings: JSONObject) {
        val array = settings.optJSONArray("models")
        models.clear()
        for (modelIndex in 0 until (array?.length() ?: 0)) {
            val model = array!!.getJSONObject(modelIndex)
            val efforts = model.optJSONArray("efforts")
            val tiers = model.optJSONArray("serviceTiers")
            models.add(ModelOption(
                model.optString("label"), model.optString("name"), model.optString("detail"), model.optString("account"),
                (0 until (efforts?.length() ?: 0)).map {
                    efforts!!.getJSONObject(it).let { effort -> Effort(effort.optString("value"), effort.optString("label"), effort.optString("about")) }
                },
                (0 until (tiers?.length() ?: 0)).map {
                    tiers!!.getJSONObject(it).let { tier -> ServiceTier(tier.optString("id"), tier.optString("name"), tier.optString("description")) }
                },
                model.optString("defaultServiceTier"),
            ))
        }
        val accountArray = settings.optJSONArray("accounts")
        accounts.clear()
        accounts.addAll((0 until (accountArray?.length() ?: 0)).map { i ->
            accountArray!!.getJSONObject(i).let { Account(it.optString("id"), it.optString("name"), it.optString("provider"), it.optString("kind"), it.optString("problem")) }
        })
        defaultModel = settings.optString("model")
        defaultEffort = settings.optString("effort")
        defaultTier = settings.optString("tier")
        defaultWindow = settings.optInt("window")
        approvalMode = settings.optString("approvalMode", "approval")
        autoMode = settings.optBoolean("autoMode")
        modelsLoading = settings.optBoolean("loading")
    }

    /** The model of the chat on screen (or of a new chat), with its effort. */
    val chatModel: ModelOption? get() = models.firstOrNull { it.label == (meta?.model?.takeIf { current != null } ?: defaultModel) }
    val chatEffort: String get() = meta?.effort?.takeIf { current != null } ?: defaultEffort
    val chatTier: String get() = meta?.tier?.takeIf { current != null } ?: defaultTier

    private fun applyMeta(m: Meta) {
        val wasRunning = running.put(m.chat, m.running)
        if (!foreground && wasRunning == true && !m.running) notifier.show(m.chat, m.title, "Done.")
        if (!foreground) {
            if (m.approval != null && approvals[m.chat]?.id != m.approval.id) notifier.show(m.chat, m.title, "Waiting for your approval: " + m.approval.title)
            if (m.question != null && questions[m.chat]?.id != m.question.id) notifier.show(m.chat, m.title, m.question.questions.firstOrNull()?.text ?: "1loop has a question")
        }
        if (m.approval != null) approvals[m.chat] = m.approval else approvals.remove(m.chat)
        if (m.question != null) questions[m.chat] = m.question else questions.remove(m.chat)
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
                val index = data.optInt("index") - firstItem
                val item = Item.from(data.getJSONObject("item"))
                when {
                    index < 0 -> {} // an item above the loaded page
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

    fun setModel(label: String? = null, effort: String? = null, tier: String? = null) {
        val target = server ?: return
        // Shown at once; the computer confirms through its events.
        if (current == null) {
            label?.let { defaultModel = it }
            effort?.let { defaultEffort = it }
            tier?.let { defaultTier = it }
        } else meta = meta?.let { it.copy(model = label ?: it.model, effort = effort ?: it.effort, tier = tier ?: it.tier) }
        scope.launch {
            runCatching {
                val body = JSONObject().put("chat", current.orEmpty()).put("label", label.orEmpty()).put("effort", effort.orEmpty())
                tier?.let { body.put("tier", it) }
                withContext(Dispatchers.IO) { Api(target).post("/api/model", body) }
            }.onFailure { problem = it.message }
        }
    }

    /** Reads every account's usage windows; a computer older than this app has none to give. */
    fun loadLimits() {
        val target = server ?: return
        scope.launch {
            val reply = runCatching { withContext(Dispatchers.IO) { Api(target).get("/api/limits") } }.getOrNull() ?: return@launch
            val array = reply.optJSONArray("accounts") ?: return@launch
            for (i in 0 until array.length()) {
                val account = array.getJSONObject(i)
                val windows = account.optJSONArray("windows")
                limits[account.optString("account")] = AccountLimits(account.optString("note"), (0 until (windows?.length() ?: 0)).map {
                    windows!!.getJSONObject(it).let { w -> LimitWindow(w.optString("label"), w.optDouble("left").toFloat(), w.optLong("reset")) }
                })
            }
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

    /** Follows a browser tab: its frames and its state, until cancelled. */
    suspend fun panelStream(chat: String, key: String, onMessage: suspend (Char, ByteArray) -> Unit) {
        val target = server ?: return
        Api(target).messages("/api/chats/$chat/panel/$key/stream", onMessage)
    }

    // Scrolling sends a wheel per few pixels of drag: while one waits for
    // its turn, the next ones add to it instead of queuing behind it.
    private val scrolls = mutableMapOf<String, FloatArray>()

    fun panelScroll(chat: String, key: String, x: Float, y: Float, dy: Float) {
        val target = server ?: return
        val id = "$chat/$key"
        scrolls[id]?.let { it[2] += dy; return }
        scrolls[id] = floatArrayOf(x, y, dy)
        scope.launch {
            inputs.withLock {
                val wheel = scrolls.remove(id) ?: return@withLock
                sendInput(target, chat, key, "scroll", "", wheel[0], wheel[1], wheel[2])
            }
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

    fun answer(request: Questions, answers: Map<String, String>) {
        val target = server ?: return
        questions.remove(request.chat)
        scope.launch {
            runCatching {
                val body = JSONObject().put("id", request.id).put("answers", JSONObject(answers))
                withContext(Dispatchers.IO) { Api(target).post("/api/answer", body) }
            }.onFailure { problem = it.message }
        }
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
