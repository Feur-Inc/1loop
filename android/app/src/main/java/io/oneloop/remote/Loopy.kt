// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import org.json.JSONArray
import org.json.JSONObject
import java.time.Instant
import java.time.OffsetDateTime
import java.time.ZoneId
import java.time.format.DateTimeFormatter

// Loopy (#352), the personal agent, as the phone shows it: the one of the
// target it lives on, this phone's engine, a desktop or a server
// (internal/loopy, internal/loopy/engine). It works there; this only shows it
// and answers its cards, over the target's /api/loopy and its event stream.

data class LoopyField(val label: String, val value: String)

data class LoopyOption(val label: String, val detail: String, val aside: String, val action: String)

/** The kinds of card the user types in. */
private val TYPED = listOf("signin", "unlock")

data class LoopyCard(
    val kind: String, val title: String, val subtitle: String, val fields: List<LoopyField>, val body: String,
    val options: List<LoopyOption>, val warning: String, val actions: List<String>, val editable: Boolean,
    val ask: String, val secret: Boolean, val keep: Boolean, val answer: String, val result: String, val task: String, val link: String = "",
) {
    /** The card still waits for the user (loopy.Card.Waiting). */
    val waiting get() = answer.isEmpty() && kind != "summary" && (actions.isNotEmpty() || options.isNotEmpty() || ask.isNotEmpty())

    /** Its buttons need nothing typed: a notification can carry them. */
    val plainActions get() = if (!waiting || ask.isNotEmpty() || kind in TYPED || kind == "autolink") emptyList() else actions.ifEmpty { options.map { it.label } }

    companion object {
        fun from(o: JSONObject) = LoopyCard(
            o.optString("kind"), o.optString("title"), o.optString("subtitle"),
            o.objects("fields") { LoopyField(it.optString("label"), it.optString("value")) }, o.optString("body"),
            o.objects("options") { LoopyOption(it.optString("label"), it.optString("detail"), it.optString("aside"), it.optString("action")) },
            o.optString("warning"), o.strings("actions"), o.optBoolean("editable"), o.optString("ask"), o.optBoolean("secret"),
            o.optBoolean("keep"), o.optString("answer"), o.optString("result"), o.optString("task"), o.optString("link"),
        )
    }
}

data class LoopyMessage(
    val id: String, val time: Instant, val mine: Boolean, val text: String, val reaction: String, val card: LoopyCard?,
    val memory: String, val memoryId: String, val note: String, val files: List<String>, val pending: Boolean,
) {
    companion object {
        fun from(o: JSONObject) = LoopyMessage(
            o.optString("id"), instant(o.optString("t")), o.optString("from") == "me", o.optString("text"), o.optString("reaction"),
            o.optJSONObject("card")?.let { LoopyCard.from(it) }, o.optString("memory"), o.optString("memoryId"),
            o.optString("note"), o.strings("files"), o.optBoolean("pending"),
        )
    }
}

data class LoopyTask(val id: String, val title: String, val chat: String, val status: String, val steps: List<Pair<Instant, String>>, val prs: List<String>) {
    companion object {
        fun from(o: JSONObject) = LoopyTask(
            o.optString("id"), o.optString("title"), o.optString("chat"), o.optString("status"),
            o.objects("steps") { instant(it.optString("t")) to it.optString("text") }, o.strings("prs"),
        )
    }
}

data class LoopyRoutine(val json: JSONObject) {
    val id: String get() = json.optString("id")
    val title: String get() = json.optString("title")
    val task: String get() = json.optString("task")
    val on get() = json.optBoolean("on")
    val watch get() = json.optBoolean("watch")
    val whenText: String get() = json.optString("when")
    val trigger: String get() = json.optString("trigger")
    val filter: String get() = json.optString("filter")
    val token: String get() = json.optString("token")
    val runs get() = json.objects("history") { Triple(instant(it.optString("t")), it.optString("why"), it.optString("outcome")) }

    /** When it starts, in words. */
    fun starts(): String = when (trigger) {
        "github" -> "When there is a new $filter on GitHub"
        "mail" -> if (filter.isEmpty()) "When a mail comes" else "When a mail comes about $filter"
        "webhook" -> "When its address is called"
        else -> describeWhen(whenText)
    }
}

data class LoopyFact(val id: String, val text: String, val kind: String, val since: Instant, val until: Instant?, val said: Boolean, val times: Int, val pinned: Boolean, val quote: String) {
    /** What is known of where it comes from. */
    fun detail(): String = listOfNotNull(
        "Pinned".takeIf { pinned },
        if (said) "You said so" else if (times >= 2) "Noticed $times times" else "Noticed once, only a guess yet",
        "Since ${day(since)}", "“$quote”".takeIf { quote.isNotEmpty() },
    ).joinToString(" · ")

    companion object {
        fun from(o: JSONObject) = LoopyFact(
            o.optString("id"), o.optString("text"), o.optString("kind"), instant(o.optString("since")),
            o.optString("until").takeIf { it.isNotEmpty() }?.let { instant(it) }, o.optBoolean("said"), o.optInt("times"),
            o.optBoolean("pinned"), o.optString("quote"),
        )
    }
}

data class LoopyFile(val path: String, val dir: Boolean, val size: Long)

data class LoopyModel(val connection: String, val id: String, val label: String, val efforts: List<String>)

/** The memory page's titles, in its order (loopy.KindNames). */
val LOOPY_KINDS = listOf(
    "instruction" to "Rules", "style" to "How you like answers", "preference" to "Preferences", "habit" to "Habits",
    "person" to "People", "place" to "Places", "work" to "Work", "goal" to "Goals", "other" to "Other",
)

/** loopy.Reactions. */
val LOOPY_REACTIONS = listOf(
    "thumbs_up" to "👍", "heart" to "❤️", "done" to "✅", "laugh" to "😄", "wow" to "😮",
    "eyes" to "👀", "moon" to "🌙", "party" to "🎉", "sad" to "😢", "pray" to "🙏",
)

fun loopyEmoji(name: String) = LOOPY_REACTIONS.firstOrNull { it.first == name }?.second ?: name

internal fun <T> JSONObject.objects(name: String, read: (JSONObject) -> T): List<T> =
    optJSONArray(name).let { a -> (0 until (a?.length() ?: 0)).mapNotNull { a!!.optJSONObject(it)?.let(read) } }

internal fun JSONObject.strings(name: String): List<String> =
    optJSONArray(name).let { a -> (0 until (a?.length() ?: 0)).map { a!!.optString(it) } }

internal fun instant(text: String): Instant = runCatching { OffsetDateTime.parse(text).toInstant() }.getOrDefault(Instant.EPOCH)

internal fun day(at: Instant): String = DateTimeFormatter.ofPattern("d MMM yyyy").format(at.atZone(ZoneId.systemDefault()))
internal fun clock(at: Instant): String = DateTimeFormatter.ofPattern("HH:mm").format(at.atZone(ZoneId.systemDefault()))

/** loopy.Describe: a schedule in words. */
fun describeWhen(text: String): String {
    val parts = text.trim().split(Regex("\\s+"))
    val at = parts.drop(1).joinToString(" ")
    return when (parts.firstOrNull()) {
        "daily" -> "Every day at $at"
        "weekdays" -> "Monday to Friday at $at"
        "monthly" -> "Each month on day ${at.replace(" ", " at ")}"
        "once" -> "Once, on ${at.replace(" ", " at ")}"
        "every" -> "Every $at"
        null, "" -> ""
        else -> parts.first().split(",").joinToString(", ") { it.replaceFirstChar(Char::uppercase) } + (if (at.isEmpty()) "" else " at $at")
    }
}

/** The answer to a card, as POST /api/loopy/answer takes it. */
fun loopyAnswer(message: LoopyMessage, action: String, fields: List<LoopyField>? = null, body: String? = null, text: String = "", values: Map<String, String> = emptyMap(), keep: Boolean = false, hint: String = ""): JSONObject {
    val out = JSONObject().put("message", message.id).put("answer", action)
    fields?.let { list -> out.put("fields", JSONArray(list.map { JSONObject().put("label", it.label).put("value", it.value) })) }
    body?.let { out.put("body", it) }
    if (text.isNotEmpty()) out.put("text", text)
    if (values.isNotEmpty()) out.put("values", JSONObject(values)).put("keep", keep).put("hint", hint)
    return out
}

/** What the phone knows of the open target's Loopy. Its state changes on the main thread. */
class LoopyStore(private val app: RemoteApp, private val scope: CoroutineScope) {
    val messages = mutableStateListOf<LoopyMessage>()
    var loaded by mutableStateOf(false)
    var more by mutableStateOf(false)
    var host by mutableStateOf("")
    var state by mutableStateOf("idle")
    var label by mutableStateOf("")
    var running by mutableStateOf(false)
    var waiting by mutableStateOf(listOf<String>())
    var tasks by mutableStateOf(listOf<LoopyTask>())
    var routines by mutableStateOf(listOf<LoopyRoutine>())
    var rules by mutableStateOf(listOf<JSONObject>())
    var vault by mutableStateOf(listOf<JSONObject>())
    var vaultState by mutableStateOf("")
    var vaultStays by mutableStateOf(false)
    var links by mutableStateOf(listOf<LoopyLink>())
    var accounts by mutableStateOf(listOf<JSONObject>())
    var settings by mutableStateOf(JSONObject())
    var model by mutableStateOf("")
    var models by mutableStateOf(listOf<LoopyModel>())
    var facts by mutableStateOf(0)
    var browser by mutableStateOf(false)
    var taken by mutableStateOf(false)
    var problem by mutableStateOf<String?>(null)
    var supported by mutableStateOf(true) // false: that 1loop is older than Loopy
    var revision by mutableStateOf(0) // a page reads again what it shows
    private var version = 0L
    private var reading: Job? = null

    /** Another target: nothing of the last one's stays. */
    fun reset() {
        reading?.cancel()
        messages.clear()
        loaded = false; more = false; host = ""; state = "idle"; label = ""; running = false
        waiting = emptyList(); tasks = emptyList(); routines = emptyList(); rules = emptyList(); vault = emptyList(); accounts = emptyList(); links = emptyList(); vaultState = ""; vaultStays = false
        settings = JSONObject(); model = ""; models = emptyList(); facts = 0; browser = false; taken = false
        problem = null; supported = true; version = 0
    }

    fun reload() {
        reading?.cancel()
        reading = scope.launch {
            delay(30) // several changes at once are read once
            try {
                show(app.loopyCall { it.get("/api/loopy") })
                supported = true
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (failure: ApiError) {
                if (failure.status == 404) supported = false else problem = failure.message
            } catch (failure: Exception) {
                problem = failure.message
            }
        }
    }

    fun show(view: JSONObject) {
        host = view.optString("host")
        val read = view.objects("messages") { LoopyMessage.from(it) }
        val from = read.firstOrNull()?.id
        val earlier = if (from == null) emptyList() else messages.takeWhile { it.id != from }.takeIf { it.size < messages.size }.orEmpty()
        messages.clear()
        messages.addAll(earlier + read)
        if (earlier.isEmpty()) more = view.optBoolean("more")
        version = view.optLong("version")
        state = view.optString("state", "idle"); label = view.optString("label"); running = view.optBoolean("running")
        waiting = view.strings("waiting")
        tasks = view.objects("tasks") { LoopyTask.from(it) }
        routines = view.objects("routines") { LoopyRoutine(it) }
        rules = view.objects("rules") { it }; vault = view.objects("vault") { it }; vaultState = view.optString("vaultState"); vaultStays = view.optBoolean("vaultStays")
        links = view.objects("links") { LoopyLink(it) }; accounts = view.objects("accounts") { it }
        settings = view.optJSONObject("settings") ?: JSONObject()
        model = view.optString("model")
        models = view.objects("models") { LoopyModel(it.optString("connection"), it.optString("id"), it.optString("label"), it.strings("efforts")) }
        facts = view.optInt("facts"); browser = view.optBoolean("browser"); taken = view.optBoolean("taken")
        loaded = true
        revision++
    }

    /** A "loopy" event: what changed (loopy.Change). */
    fun apply(change: JSONObject) {
        state = change.optString("state", "idle"); label = change.optString("label"); running = change.optBoolean("running")
        if (!loaded) return
        for (id in change.strings("gone")) messages.removeAll { it.id == id }
        for (message in change.objects("messages") { LoopyMessage.from(it) }) {
            val at = messages.indexOfFirst { it.id == message.id }
            if (at >= 0) messages[at] = message else messages.add(message)
        }
        waiting = messages.filter { it.card?.waiting == true }.map { it.id }.reversed()
        if (change.optBoolean("all")) reload()
    }

    fun earlier() {
        val first = messages.firstOrNull() ?: return
        act {
            val page = app.loopyCall { it.get("/api/loopy/messages?before=${java.net.URLEncoder.encode(first.id, "UTF-8")}") }
            val known = messages.map { it.id }.toSet()
            messages.addAll(0, page.objects("messages") { LoopyMessage.from(it) }.filter { it.id !in known })
            more = page.optBoolean("more")
        }
    }

    /** Runs a request; its failure shows over the composer. */
    fun act(done: () -> Unit = {}, request: suspend () -> Unit) {
        problem = null
        scope.launch {
            try {
                request()
                done()
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (failure: Exception) {
                problem = failure.message ?: "That did not work."
            }
        }
    }

    fun post(path: String, body: JSONObject, done: () -> Unit = {}) = act(done) { app.loopyCall { it.post("/api/loopy/$path", body) } }

    /** A change asked for from a page (loopy.Action); its failure goes to [failed] when given. */
    fun change(action: JSONObject, failed: ((String) -> Unit)? = null, done: () -> Unit = {}) {
        problem = null
        scope.launch {
            try {
                app.loopyCall { it.post("/api/loopy/do", action, readTimeout = 120_000) }
                done()
                reload()
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (failure: Exception) {
                val text = failure.message ?: "That did not work."
                if (failed != null) failed(text) else problem = text
            }
        }
    }

    fun change(what: String, id: String = "") = change(JSONObject().put("do", what).put("id", id))

    fun set(edit: (JSONObject) -> Unit) {
        val changed = JSONObject(settings.toString())
        edit(changed)
        post("settings", changed) { reload() }
    }

    fun message(id: String) = messages.firstOrNull { it.id == id }
    fun task(id: String) = tasks.firstOrNull { it.id == id }
}
