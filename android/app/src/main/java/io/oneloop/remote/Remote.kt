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

/** A file picked for the next message: sending, then the engine's copy (ready) or a problem. */
data class Attachment(
    val key: String, val uri: android.net.Uri, val name: String, val size: Long, val image: Boolean,
    val folder: String, // the workspace it was sent to
    val ready: JSONObject? = null, val problem: String? = null,
) {
    val sending get() = ready == null && problem == null

    /** Whether a message to this workspace can carry it (the engine's resolved path counts too). */
    fun belongsTo(workspace: String) = workspace == folder || workspace == ready?.optString("workspace")
}

private const val MAX_ATTACHMENT = 64L shl 20

/** The versions of a message (its wordings) or of its reply (its retries); current is 1-based. */
data class MessageVersions(val anchor: String, val current: Int, val total: Int)

data class Item(
    val kind: String, val text: String, val images: Int,
    val tool: String, val verb: String, val target: String, val mono: Boolean,
    val meta: String, val tone: String, val state: String,
    val args: String, val diff: String, val millis: Long, val failed: String,
    val id: String = "", val versions: MessageVersions? = null,
) {
    companion object {
        fun from(o: JSONObject) = Item(
            o.optString("kind"), o.optString("text"), o.optInt("images"),
            o.optString("tool"), o.optString("verb"), o.optString("target"), o.optBoolean("mono"),
            o.optString("meta"), o.optString("tone"), o.optString("state", "done"),
            o.optString("args"), o.optString("diff"), o.optLong("millis"), o.optString("failed"),
            o.optString("id"), o.optJSONObject("versions")?.let { MessageVersions(it.optString("anchor"), it.optInt("current"), it.optInt("total")) },
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
    val todo: List<Task>, val tier: String = "", val contextStatus: String = "",
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
            o.optString("tier"), o.optString("contextStatus"),
        )
    }
}

private fun strings(array: JSONArray?) = (0 until (array?.length() ?: 0)).map { array!!.optString(it) }

sealed interface Screen {
    data object Launch : Screen
    data object Mode : Screen // where 1loop works: server, desktop or this phone
    data object Servers : Screen
    data object LocalSetup : Screen
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
    var target by mutableStateOf<ExecutionTarget?>(null)
    val local = AndroidLocalRuntime.get(context)
    var starting by mutableStateOf(false) // the local engine is starting
    private var localStartGeneration = -1L
    var messageVersions by mutableStateOf(false) // the computer can edit, retry and switch versions
    var attachmentsEnabled by mutableStateOf(false) // the computer takes files with a message
    val attachments = mutableStateListOf<Attachment>()
    private val uploads = mutableMapOf<String, Job>()
    var connectionsOpen by mutableStateOf(false) // this phone's connections (ConnectionsSheet)
    val servers = mutableStateListOf<Server>()
    val online = mutableStateListOf<String>() // keys of the servers that answered the last scan
    var scanning by mutableStateOf(false)
    var problem by mutableStateOf<String?>(null)

    var server by mutableStateOf<Server?>(null)
    var connected by mutableStateOf(false)
    val chats = mutableStateListOf<ChatSummary>()
    var projects by mutableStateOf(listOf<String>())
    var workspace by mutableStateOf("")
    var selectedProject by mutableStateOf<String?>(null)
    var projectManagement by mutableStateOf(false)
    var projectsOpen by mutableStateOf(false)
    var settingsOpen by mutableStateOf(false)
    var userPreferences by mutableStateOf("")
    var preferencesEditable by mutableStateOf(false)
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
    @Volatile private var connectionGeneration = 0L // Address equality cannot distinguish reconnects.

    private suspend fun <T> onConnection(target: Server, generation: Long, call: (Api) -> T): T {
        if (generation != connectionGeneration) throw CancellationException("Connection changed")
        val result = withContext(Dispatchers.IO) { call(Api(target)) }
        if (generation != connectionGeneration) throw CancellationException("Connection changed")
        return result
    }

    private fun actionFailed(generation: Long, failure: Throwable) {
        if (failure is CancellationException) throw failure
        if (generation == connectionGeneration) problem = failure.message
    }

    init {
        servers.addAll(saved())
        // The local engine stopped (from its notification, or on its own): back to its setup.
        scope.launch {
            local.activeServer.collect { active ->
                if (active == null && server?.localPhone == true && screen == Screen.Chat) {
                    disconnect()
                    screen = Screen.LocalSetup
                }
            }
        }
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

    /** After the launch animation: back to the last place 1loop worked, or the choice. */
    fun started() {
        val last = prefs.getString("last", null)
        val known = servers.firstOrNull { it.key == last && it.paired }
        // Before the choice existed, the last computer told it.
        val saved = prefs.getString("target", null)?.let { name -> ExecutionTarget.entries.firstOrNull { it.name == name } }
            ?: known?.let { if (it.scheme == "http") ExecutionTarget.RemoteDesktop else ExecutionTarget.RemoteServer }
        if (saved == null) {
            screen = Screen.Mode
            return
        }
        target = saved
        if (saved == ExecutionTarget.LocalPhone) {
            local.activeServer.value?.let { connect(it) } ?: run { screen = Screen.Mode }
            return
        }
        if (known != null) connect(known) else {
            screen = Screen.Servers
            scan()
        }
    }

    // -- where 1loop works -----------------------------------------------------

    fun chooseTarget(chosen: ExecutionTarget) {
        connectionGeneration++
        target = chosen
        problem = null
        prefs.edit { putString("target", chosen.name) }
        when (chosen) {
            ExecutionTarget.LocalPhone -> openLocal()
            ExecutionTarget.RemoteDesktop -> { screen = Screen.Servers; scan() }
            ExecutionTarget.RemoteServer -> screen = Screen.Servers
        }
    }

    /** Back to the choice; a local engine keeps running until stopped from its notification. */
    fun switchTarget() {
        disconnect()
        problem = null
        screen = Screen.Mode
    }

    /** The running engine, or a start when nothing is missing, or the setup. */
    private fun openLocal() {
        local.activeServer.value?.let { return connect(it) }
        screen = Screen.LocalSetup
        val status = local.status.value
        if (status.ready) startLocal()
    }

    fun startLocal() {
        val generation = connectionGeneration
        if (starting && localStartGeneration == generation) return
        localStartGeneration = generation
        starting = true
        problem = null
        scope.launch {
            try {
                val active = local.start()
                if (generation == connectionGeneration) connect(active)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                actionFailed(generation, e)
            } finally {
                if (localStartGeneration == generation) starting = false
            }
        }
    }

    /** Ends a pending Claude sign-in; called when its editor is closed on purpose. */
    fun cancelClaudeLogin() {
        val target = server?.takeIf { it.localPhone } ?: return
        scope.launch { runCatching { withContext(Dispatchers.IO) { Api(target).post("/api/local/claude/login", JSONObject().put("cancel", true)) } } }
    }

    fun prepareLocal(step: String) {
        problem = null
        // A failure shows on its step (LocalStatus); nothing else to report here.
        scope.launch { try { local.prepare(step) } catch (e: CancellationException) { throw e } catch (_: Exception) {} }
    }

    // Local ADB: LocalAdbState carries the problem of a failed action.
    fun setAdbAllowed(allowed: Boolean) {
        scope.launch { try { local.setAdbAllowed(allowed) } catch (e: CancellationException) { throw e } catch (_: Exception) {} }
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
        val generation = connectionGeneration
        scope.launch {
            if (generation != connectionGeneration) return@launch
            problem = null
            try {
                val text = address.trim().trimEnd('/').let { if (it.contains("://")) it else "https://$it" }
                val url = java.net.URL(text)
                require(url.protocol in listOf("http", "https") && url.userInfo == null && url.path.isEmpty() && url.query == null && url.ref == null) { "Enter a server address without a path." }
                val pin = fingerprint.trim().replace(":", "").lowercase()
                require(pin.isEmpty() || (url.protocol == "https" && pin.matches(Regex("[0-9a-f]{64}")))) { "Enter the complete SHA-256 fingerprint from the server console." }
                val port = if (url.port > 0) url.port else if (address.trim().contains("://")) url.defaultPort else DEFAULT_PORT
                val probe = Server(url.host, url.host, port, scheme = url.protocol, fingerprint = pin)
                val info = onConnection(probe, generation) { it.get("/api/info") }
                val target = probe.copy(name = info.optString("name", url.host))
                upsert(target)
                online.add(target.key)
                choose(servers.first { it.key == target.key })
            } catch (e: Exception) { actionFailed(generation, e) }
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
        val generation = connectionGeneration
        scope.launch {
            if (generation != connectionGeneration) return@launch
            busy = true
            try {
                val reply = onConnection(server, generation) { it.post("/api/pair", JSONObject().put("device", deviceName())) }
                screen = Screen.Pair(server, reply.getString("pairing"), reply.optString("code").takeIf { reply.optBoolean("console") }.orEmpty())
            } catch (e: Exception) {
                actionFailed(generation, e)
            } finally {
                if (generation == connectionGeneration) busy = false
            }
        }
    }

    fun confirm(pair: Screen.Pair, code: String) {
        val generation = connectionGeneration
        scope.launch {
            if (generation != connectionGeneration) return@launch
            busy = true
            problem = null
            try {
                val reply = onConnection(pair.server, generation) {
                    it.post("/api/pair/confirm", JSONObject().put("pairing", pair.pairing).put("code", code))
                }
                val server = pair.server.copy(token = reply.getString("token"))
                upsert(server)
                connect(server)
            } catch (e: Exception) {
                actionFailed(generation, e)
            } finally {
                if (generation == connectionGeneration) busy = false
            }
        }
    }

    // -- connection ----------------------------------------------------------

    fun connect(target: Server) {
        clearAttachments() // picked for the previous target
        connectionsOpen = false
        settingsOpen = false
        projectsOpen = false
        events?.cancel()
        val generation = ++connectionGeneration
        server = target
        connected = false
        busy = false
        approvalMode = "approval"
        autoMode = false
        loadingEarlier = false
        scrolls.clear()
        projects = emptyList()
        workspace = ""
        models.clear()
        accounts.clear()
        limitsLoading = false
        limits.clear()
        runCatching { JSONArray(prefs.getString("limits:${target.key}", "[]")) }.getOrNull()?.let { restoreLimits(it) }
        selectedProject = prefs.getString("project:${target.key}", null)
        projectManagement = false
        preferencesEditable = false
        userPreferences = ""
        running.clear()
        defaultModel = ""
        defaultEffort = ""
        defaultTier = ""
        defaultWindow = 0
        messageVersions = false
        attachmentsEnabled = false
        chats.clear()
        items.clear()
        meta = null
        current = null
        approvals.clear()
        questions.clear()
        prefs.edit { putString("last", target.key) }
        screen = Screen.Chat
        events = scope.launch {
            val eventScope = this
            var wait = 500L
            while (true) {
                try {
                    Api(target).events(
                        onOpen = {
                            wait = 500L
                            eventScope.launch(Dispatchers.Main) {
                                if (generation == connectionGeneration) { connected = true; refresh(target, generation) }
                            }
                        },
                        onEvent = { name, data -> eventScope.launch(Dispatchers.Main) {
                            if (generation == connectionGeneration) handle(name, data)
                        } },
                    )
                } catch (e: CancellationException) {
                    throw e
                } catch (e: ApiError) {
                    if (generation != connectionGeneration) return@launch
                    if (e.status == 401 && target.localPhone) { // the local engine restarted with a new token
                        withContext(Dispatchers.Main) {
                            disconnect()
                            openLocal()
                        }
                        return@launch
                    }
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
                if (generation != connectionGeneration) return@launch
                connected = false
                delay(wait)
                wait = (wait * 2).coerceAtMost(8000)
            }
        }
    }

    fun leave() {
        disconnect()
        screen = Screen.Servers
        scan()
    }

    private fun disconnect() {
        connectionGeneration++
        server = null
        busy = false
        scrolls.clear()
        clearAttachments()
        connectionsOpen = false // this target's sheet; also when the local engine stops
        events?.cancel()
        events = null
        connected = false
        prefs.edit { remove("last") }
    }

    private suspend fun refresh(target: Server, generation: Long) {
        try {
            val state = onConnection(target, generation) { it.get("/api/state") }
            chats.clear()
            chats.addAll(parseChats(state.optJSONArray("chats")))
            projects = strings(state.optJSONArray("projects"))
            workspace = state.optString("workspace")
            projectManagement = state.optBoolean("projectManagement")
            if (selectedProject !in projects) selectedProject = workspace
            state.optJSONObject("settings")?.let { applySettings(it) }
            if (current == null) current = state.optString("current").ifEmpty { null } ?: chats.firstOrNull { it.workspace == selectedProject }?.id
            chats.firstOrNull { it.id == current }?.workspace?.let { selectedProject = it }
            current?.let { load(it, target, generation) }
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (_: Exception) { }
    }

    private fun parseChats(array: JSONArray?) = (0 until (array?.length() ?: 0)).map { i ->
        array!!.getJSONObject(i).let { ChatSummary(it.getString("id"), it.optString("title"), it.optString("workspace"), it.optBoolean("running")) }
    }

    private suspend fun load(id: String, target: Server, generation: Long) {
        val chat = try { onConnection(target, generation) { it.get("/api/chats/$id?limit=$PAGE") } }
        catch (cancelled: CancellationException) { throw cancelled }
        catch (_: Exception) { return }
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
        val generation = connectionGeneration
        val id = current ?: return 0
        if (firstItem <= 0 || loadingEarlier) return 0
        loadingEarlier = true
        try {
            val chat = onConnection(target, generation) { it.get("/api/chats/$id?limit=$PAGE&before=$firstItem") }
            if (current != id) return 0
            val array = chat.optJSONArray("items") ?: return 0
            val earlier = (0 until array.length()).map { Item.from(array.getJSONObject(it)) }
            items.addAll(0, earlier)
            firstItem = chat.optInt("first", 0)
            return earlier.size
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (_: Exception) {
            return 0
        } finally {
            if (generation == connectionGeneration) loadingEarlier = false
        }
    }

    fun open(id: String?) {
        current = id
        chats.firstOrNull { it.id == id }?.workspace?.let {
            selectedProject = it
            server?.let { target -> prefs.edit { putString("project:${target.key}", it) } }
        }
        items.clear()
        firstItem = 0
        meta = null
        val target = server ?: return
        val generation = connectionGeneration
        if (id != null) scope.launch { load(id, target, generation) }
    }

    private fun applySettings(settings: JSONObject) {
        userPreferences = settings.optString("userPreferences")
        preferencesEditable = settings.optBoolean("preferencesEditable")
        messageVersions = settings.optBoolean("messageVersions")
        attachmentsEnabled = settings.optBoolean("attachments")
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
        // On this phone, LocalRuntimeService notifies: it runs even without the app.
        val notify = !foreground && server?.localPhone != true
        if (notify && wasRunning == true && !m.running) notifier.show(m.chat, m.title, "Done.")
        if (notify) {
            if (m.approval != null && approvals[m.chat]?.id != m.approval.id) notifier.show(m.chat, m.title, "Waiting for your approval: " + m.approval.title)
            if (m.question != null && questions[m.chat]?.id != m.question.id) notifier.show(m.chat, m.title, m.question.questions.firstOrNull()?.text ?: "1loop has a question")
        }
        if (m.approval != null) approvals[m.chat] = m.approval else approvals.remove(m.chat)
        if (m.question != null) questions[m.chat] = m.question else questions.remove(m.chat)
        if (m.chat == current) meta = m
    }

    private fun handle(name: String, data: JSONObject) {
        val target = server ?: return
        val generation = connectionGeneration
        when (name) {
            "projects" -> { projects = strings(data.optJSONArray("projects")) }
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
                    else -> current?.let { scope.launch { load(it, target, generation) } } // missed some: reload
                }
            }
            "meta" -> applyMeta(Meta.from(data))
            "replace" -> if (data.optString("chat") == current) {
                current?.let { scope.launch { load(it, target, generation) } }
            }
            "settings" -> applySettings(data)
        }
    }

    // -- actions -------------------------------------------------------------

    fun send(text: String, mode: String = "send", project: String? = null) {
        val generation = connectionGeneration
        val target = server ?: return
        // What this message carries, as it is now: files added meanwhile stay for the next one.
        val sent = attachments.filter { it.ready != null }
        val body = JSONObject().put("chat", current.orEmpty()).put("text", text).put("mode", mode)
            .put("workspace", project ?: chats.firstOrNull { it.id == current }?.workspace ?: selectedProject ?: workspace)
        if (sent.isNotEmpty()) body.put("attachments", JSONArray(sent.map { it.ready }))
        problem = null
        scope.launch {
            try {
                val reply = onConnection(target, generation) { it.post("/api/send", body) }
                if (generation != connectionGeneration) return@launch // another target since: nothing of this one to touch
                val keys = sent.map { it.key }.toSet()
                attachments.removeAll { it.key in keys }
                val id = reply.optString("chat")
                if (current == null && id.isNotEmpty()) open(id)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                actionFailed(generation, e)
            }
        }
    }

    fun setModel(label: String? = null, effort: String? = null, tier: String? = null) {
        val generation = connectionGeneration
        val target = server ?: return
        val chat = current.orEmpty()
        // Shown at once; the computer confirms through its events.
        if (current == null) {
            label?.let { defaultModel = it }
            effort?.let { defaultEffort = it }
            tier?.let { defaultTier = it }
        } else meta = meta?.let { it.copy(model = label ?: it.model, effort = effort ?: it.effort, tier = tier ?: it.tier) }
        scope.launch {
            runCatching {
                val body = JSONObject().put("chat", chat).put("label", label.orEmpty()).put("effort", effort.orEmpty())
                tier?.let { body.put("tier", it) }
                onConnection(target, generation) { it.post("/api/model", body) }
            }.onFailure { actionFailed(generation, it) }
        }
    }

    private var limitsLoading = false

    private fun restoreLimits(array: JSONArray) {
        for (i in 0 until array.length()) {
            val account = array.getJSONObject(i)
            val windows = account.optJSONArray("windows")
            val next = AccountLimits(account.optString("note"), (0 until (windows?.length() ?: 0)).map {
                windows!!.getJSONObject(it).let { w -> LimitWindow(w.optString("label"), w.optDouble("left").toFloat(), w.optLong("reset")) }
            })
            val id = account.optString("account")
            if (next.windows.isNotEmpty() || limits[id]?.windows.isNullOrEmpty()) limits[id] = next
        }
    }

    fun loadLimits() {
        val generation = connectionGeneration
        val target = server ?: return
        if (limitsLoading) return
        limitsLoading = true
        scope.launch {
            try {
                val reply = onConnection(target, generation) { it.get("/api/limits") }
                val array = reply.optJSONArray("accounts") ?: return@launch
                restoreLimits(array)
                val cached = JSONArray()
                for ((id, value) in limits) cached.put(JSONObject().put("account", id).put("note", value.note).put("windows", JSONArray(value.windows.map {
                    JSONObject().put("label", it.label).put("left", it.left).put("reset", it.reset)
                })))
                prefs.edit { putString("limits:${target.key}", cached.toString()) }
            } catch (cancelled: CancellationException) { throw cancelled }
            catch (_: Exception) { /* Keep the last known usage during an outage. */ }
            finally { if (generation == connectionGeneration) limitsLoading = false }
        }
    }

    fun selectProject(path: String) {
        if (path !in projects) return
        selectedProject = path
        server?.let { target -> prefs.edit { putString("project:${target.key}", path) } }
        open(null)
    }

    suspend fun projectFolders(path: String): JSONObject {
        val target = server ?: error("Connect first.")
        val generation = connectionGeneration
        return onConnection(target, generation) { it.get("/api/projects?path=" + java.net.URLEncoder.encode(path, "UTF-8")) }
    }

    suspend fun addProject(path: String, create: Boolean) {
        val target = server ?: error("Connect first.")
        val generation = connectionGeneration
        val reply = onConnection(target, generation) { it.post("/api/projects", JSONObject().put("path", path).put("create", create)) }
        projects = strings(reply.optJSONArray("projects"))
        selectProject(reply.getString("workspace"))
    }

    suspend fun savePreferences(text: String) {
        val target = server ?: error("Connect first.")
        val generation = connectionGeneration
        val reply = onConnection(target, generation) { it.post("/api/preferences", JSONObject().put("userPreferences", text)) }
        userPreferences = reply.optString("userPreferences")
    }

    fun chooseApprovalMode(mode: String) {
        val generation = connectionGeneration
        val target = server ?: return
        approvalMode = mode
        scope.launch {
            runCatching { onConnection(target, generation) { it.post("/api/approval-mode", JSONObject().put("mode", mode)) } }
                .onFailure { actionFailed(generation, it) }
        }
    }

    // Versions of messages. An edit's index is taken when its sheet opens (firstItem + shown
    // index): a page loaded above meanwhile must not move it to another message.
    fun editMessage(absoluteIndex: Int, text: String) =
        chatAction("/message", JSONObject().put("index", absoluteIndex).put("action", "edit").put("text", text))

    fun retryMessage(index: Int) = chatAction("/message", JSONObject().put("index", firstItem + index).put("action", "retry"))

    fun switchVersion(anchor: String, delta: Int) = chatAction("/version", JSONObject().put("anchor", anchor).put("delta", delta))

    /** The chat reloads through the "replace" event; a refusal (a reply under way) shows. */
    private fun chatAction(path: String, body: JSONObject) {
        val generation = connectionGeneration
        val target = server ?: return
        val id = current ?: return
        problem = null
        scope.launch {
            try {
                onConnection(target, generation) { it.post("/api/chats/$id$path", body) }
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                actionFailed(generation, e) // not on a target chosen since
            }
        }
    }

    // -- attachments: sent to the workspace as soon as picked ------------------

    fun attach(uris: List<android.net.Uri>, project: String?) {
        val generation = connectionGeneration
        val target = server ?: return
        // The open chat's project, or the one a new chat starts in.
        val folder = project ?: chats.firstOrNull { it.id == current }?.workspace ?: selectedProject ?: workspace
        for (uri in uris) {
            var name = "file"
            var size = -1L
            runCatching {
                context.contentResolver.query(uri, null, null, null, null)?.use { cursor ->
                    if (cursor.moveToFirst()) {
                        cursor.getColumnIndex(android.provider.OpenableColumns.DISPLAY_NAME).takeIf { it >= 0 }?.let { name = cursor.getString(it) ?: name }
                        cursor.getColumnIndex(android.provider.OpenableColumns.SIZE).takeIf { it >= 0 && !cursor.isNull(it) }?.let { size = cursor.getLong(it) }
                    }
                }
            }
            val key = java.util.UUID.randomUUID().toString()
            val image = context.contentResolver.getType(uri)?.startsWith("image/") == true
            if (size > MAX_ATTACHMENT) {
                attachments.add(Attachment(key, uri, name, size, image, folder, problem = "Larger than 64 MiB"))
                continue
            }
            attachments.add(Attachment(key, uri, name, size, image, folder))
            uploads[key] = scope.launch {
                var reply: JSONObject? = null
                var failure: String? = null
                try {
                    val path = "/api/attachments?workspace=" + java.net.URLEncoder.encode(folder, "UTF-8") + "&name=" + java.net.URLEncoder.encode(name, "UTF-8")
                    reply = onConnection(target, generation) { api ->
                        context.contentResolver.openInputStream(uri)?.use { api.upload(path, it, size) } ?: throw ApiError("Cannot read this file.")
                    }
                } catch (e: CancellationException) {
                    throw e
                } catch (e: Exception) {
                    failure = e.message ?: "Not sent"
                }
                uploads.remove(key)
                val index = attachments.indexOfFirst { it.key == key }
                if (index >= 0) attachments[index] = attachments[index].copy(ready = reply, problem = failure)
            }
        }
    }

    /** Takes it out of the next message; the copy already in the workspace stays. */
    fun removeAttachment(key: String) {
        uploads.remove(key)?.cancel()
        attachments.removeAll { it.key == key }
    }

    /** Leaves out the files sent to another workspace than the message's: it would refuse them. */
    fun retainAttachments(workspace: String) {
        attachments.filter { !it.belongsTo(workspace) }.forEach { removeAttachment(it.key) }
    }

    fun clearAttachments() {
        uploads.values.forEach { it.cancel() }
        uploads.clear()
        attachments.clear()
    }

    fun stop() {
        val generation = connectionGeneration
        val target = server ?: return
        val id = current ?: return
        scope.launch { runCatching { onConnection(target, generation) { it.post("/api/stop", JSONObject().put("chat", id)) } } }
    }

    // -- the chat's side panel ------------------------------------------------

    /** The panel's tabs on the computer; null when it cannot be reached. */
    suspend fun panelTabs(chat: String): List<PanelTab>? {
        val generation = connectionGeneration
        val target = server ?: return null
        return runCatching {
            val reply = onConnection(target, generation) { it.get("/api/chats/$chat/panel") }
            val array = reply.optJSONArray("tabs")
            (0 until (array?.length() ?: 0)).map { i ->
                array!!.getJSONObject(i).let { PanelTab(it.optString("key"), it.optString("kind"), it.optString("title"), it.optString("url"), it.optInt("index")) }
            }
        }.onFailure { if (it is CancellationException) throw it }.getOrNull()
    }

    suspend fun panelTab(chat: String, key: String): JSONObject? {
        val generation = connectionGeneration
        val target = server ?: return null
        return runCatching { onConnection(target, generation) { it.get("/api/chats/$chat/panel/$key") } }.onFailure { if (it is CancellationException) throw it }.getOrNull()
    }

    suspend fun panelScreen(chat: String, key: String): ByteArray? {
        val generation = connectionGeneration
        val target = server ?: return null
        return runCatching { onConnection(target, generation) { it.bytes("/api/chats/$chat/panel/$key/screen") } }.onFailure { if (it is CancellationException) throw it }.getOrNull()
    }

    /** Input for a browser or a terminal tab; see remote_panel.go. */
    fun panelInput(chat: String, key: String, action: String, text: String = "", x: Float = 0f, y: Float = 0f, dy: Float = 0f) {
        val generation = connectionGeneration
        val target = server ?: return
        scope.launch {
            inputs.withLock { sendInput(target, generation, chat, key, action, text, x, y, dy) }
        }
    }

    /** Follows a browser tab: its frames and its state, until cancelled. */
    suspend fun panelStream(chat: String, key: String, onMessage: suspend (Char, ByteArray) -> Unit) {
        val generation = connectionGeneration
        val target = server ?: return
        Api(target).messages("/api/chats/$chat/panel/$key/stream") { kind, bytes ->
            if (generation != connectionGeneration) throw CancellationException("Connection changed")
            onMessage(kind, bytes)
        }
    }

    // Scrolling sends a wheel per few pixels of drag: while one waits for
    // its turn, the next ones add to it instead of queuing behind it.
    private val scrolls = mutableMapOf<String, FloatArray>()

    /** A panel input that answers (browser logs, JavaScript, a resize); throws when refused. */
    suspend fun panelCall(chat: String, key: String, body: JSONObject): JSONObject {
        val generation = connectionGeneration
        val target = server ?: throw ApiError("Not connected.")
        return onConnection(target, generation) { it.post("/api/chats/$chat/panel/$key/input", body) }
    }

    fun panelScroll(chat: String, key: String, x: Float, y: Float, dy: Float) {
        val generation = connectionGeneration
        val target = server ?: return
        val id = "$chat/$key"
        scrolls[id]?.let { it[2] += dy; return }
        scrolls[id] = floatArrayOf(x, y, dy)
        scope.launch {
            inputs.withLock {
                if (generation != connectionGeneration) return@withLock
                val wheel = scrolls.remove(id) ?: return@withLock
                sendInput(target, generation, chat, key, "scroll", "", wheel[0], wheel[1], wheel[2])
            }
        }
    }

    // One at a time, in order: a tap then typing must reach the page so.
    private val inputs = Mutex()

    private suspend fun sendInput(target: Server, generation: Long, chat: String, key: String, action: String, text: String, x: Float, y: Float, dy: Float) {
        runCatching {
            val body = JSONObject().put("action", action).put("text", text).put("x", x.toDouble()).put("y", y.toDouble()).put("dy", dy.toDouble())
            onConnection(target, generation) { it.post("/api/chats/$chat/panel/$key/input", body) }
        }.onFailure { actionFailed(generation, it) }
    }

    fun answer(request: Questions, answers: Map<String, String>) {
        val generation = connectionGeneration
        val target = server ?: return
        questions.remove(request.chat)
        scope.launch {
            runCatching {
                val body = JSONObject().put("id", request.id).put("answers", JSONObject(answers))
                onConnection(target, generation) { it.post("/api/answer", body) }
            }.onFailure { actionFailed(generation, it) }
        }
    }

    fun answer(approval: Approval, allow: Boolean) {
        val generation = connectionGeneration
        val target = server ?: return
        approvals.remove(approval.chat)
        scope.launch {
            runCatching {
                onConnection(target, generation) { it.post("/api/approve", JSONObject().put("id", approval.id).put("allow", allow)) }
            }.onFailure { actionFailed(generation, it) }
        }
    }
}
