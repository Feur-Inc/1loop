package io.oneloop.remote

import android.content.Context
import android.content.Intent
import android.provider.Settings
import androidx.core.content.edit
import java.lang.ref.WeakReference
import kotlinx.coroutines.flow.MutableStateFlow

/** What a running model is doing, for the status 1loop shows outside the app. */
data class RunStatus(
    val chat: String, val title: String, val label: String, val mark: String, val todo: List<Task>,
    // phone_use or adb_shell drives the phone: the badge must not hide or catch what it touches.
    val phoneUse: Boolean,
    val question: Questions? = null, // the agent waits for answers: the badge takes them
) {
    val done get() = todo.count { it.status == "completed" }
}

/** How the status shows while a model runs and 1loop is not on screen. */
enum class StatusStyle(val key: String, val title: String) {
    Badge("badge", "Floating badge"),
    LiveUpdate("live", "Status bar (Live Update)"),
    Off("off", "Off"),
}

/**
 * The runs in progress, from the chats' metadata (RemoteApp.applyMeta), and
 * AgentStatusService, which shows the latest one while any runs: a Live
 * Update notification, and the floating badge when chosen and allowed.
 *
 * The service starts with the run, while 1loop is usually on screen:
 * Android refuses to start a foreground service from the background (unless
 * 1loop may draw over other apps), and keeping it from the start also keeps
 * the connection alive once the user leaves the app.
 */
object AgentStatus {
    private const val PREFERENCES = "agent_status"
    private const val STYLE = "style"

    val status = MutableStateFlow<RunStatus?>(null)
    val foreground = MutableStateFlow(true)
    private val runs = LinkedHashMap<String, RunStatus>()
    private var app = WeakReference<RemoteApp>(null)

    fun style(context: Context): StatusStyle {
        val key = context.getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE).getString(STYLE, null)
        return StatusStyle.entries.firstOrNull { it.key == key } ?: StatusStyle.Badge
    }

    fun setStyle(context: Context, style: StatusStyle) {
        context.getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE).edit { putString(STYLE, style.key) }
        refresh(context)
    }

    /** The badge needs "Display over other apps"; without it, the Live Update shows instead. */
    fun canDrawOverlays(context: Context) = Settings.canDrawOverlays(context)

    fun attach(remote: RemoteApp) { app = WeakReference(remote) }

    fun update(context: Context, meta: Meta) {
        if (meta.running) {
            val phoneUse = meta.label.contains("phone_use") || meta.label.contains("adb_shell")
            runs.remove(meta.chat)
            runs[meta.chat] = RunStatus(meta.chat, meta.title, meta.label, meta.mark, meta.todo, phoneUse, meta.question)
        } else if (runs.remove(meta.chat) == null) return
        refresh(context)
    }

    /** The connection changed: its runs are no longer known. */
    fun clear(context: Context) {
        runs.clear()
        refresh(context)
    }

    fun setForeground(context: Context, shown: Boolean) {
        foreground.value = shown
        refresh(context)
    }

    fun stop(chat: String) { app.get()?.stop(chat) }

    fun answer(request: Questions, answers: Map<String, String>) { app.get()?.answer(request, answers) }

    private fun refresh(context: Context) {
        status.value = runs.values.lastOrNull()
        val wanted = status.value != null && style(context) != StatusStyle.Off
        val intent = Intent(context, AgentStatusService::class.java)
        if (wanted && !AgentStatusService.active) {
            // From the background, only allowed with the overlay permission: otherwise the
            // next run started while 1loop is on screen shows it.
            runCatching { context.startForegroundService(intent) }
        } else if (!wanted && AgentStatusService.active) context.stopService(intent)
    }
}
