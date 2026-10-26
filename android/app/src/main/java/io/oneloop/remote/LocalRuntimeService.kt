package io.oneloop.remote

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.IBinder
import android.os.SystemClock
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/**
 * Keeps Local phone's work alive while 1loop is in the background: the
 * engine the user started, installs included. It never starts the engine
 * itself; it shows that it runs, offers Stop, and tells when a chat finished
 * or waits for an approval or an answer while the app is not on screen.
 */
class LocalRuntimeService : Service() {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main)
    private lateinit var runtime: AndroidLocalRuntime
    private lateinit var notifier: Notifier

    // What each chat last showed, to notify on changes only (as RemoteApp).
    private val running = mutableMapOf<String, Boolean>()
    private val approvals = mutableMapOf<String, String>()
    private val questions = mutableMapOf<String, String>()

    // Set once this instance's end was decided by Stop or by an idle release.
    private var released = false
    private var seen = 0L // the last ensureRunning this instance has answered

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onCreate() {
        super.onCreate()
        seen = requests.get()
        alive = true
        runtime = AndroidLocalRuntime.get(applicationContext)
        notifier = Notifier(applicationContext)
        getSystemService(NotificationManager::class.java).createNotificationChannel(
            NotificationChannel(CHANNEL, "Local phone", NotificationManager.IMPORTANCE_LOW).apply {
                description = "While 1loop works on this phone"
            },
        )
        // Android requires the notification within seconds of the start.
        ServiceCompat.startForeground(this, NOTIFICATION, notification("Local phone"), ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)
        scope.launch {
            combine(runtime.status, runtime.activeServer) { status, server ->
                when {
                    status.steps.any { it.state == StepState.Running } -> "Preparing Local phone…"
                    server != null -> "Local phone is running"
                    else -> "Local phone"
                }
            }.collectLatest { text ->
                getSystemService(NotificationManager::class.java).notify(NOTIFICATION, notification(text))
            }
        }
        scope.launch {
            runtime.activeServer.collectLatest { server ->
                running.clear(); approvals.clear(); questions.clear()
                if (server != null) follow(server)
            }
        }
        // In the background with nothing to do for IDLE_MS (no turn, waiting
        // message, background command or open terminal), the engine and its
        // Debian stop, to save battery and memory; the app starts them again,
        // on the same chat, when it is shown.
        scope.launch {
            var idleSince = SystemClock.elapsedRealtime()
            while (true) {
                delay(IDLE_CHECK_MS)
                val server = runtime.activeServer.value
                val busy = server == null || runtime.appForeground || runtime.status.value.steps.any { it.state == StepState.Running } ||
                    try {
                        withContext(Dispatchers.IO) { Api(server).get("/api/local/busy") }.optBoolean("busy", true)
                    } catch (e: CancellationException) {
                        throw e
                    } catch (_: Exception) {
                        true // unknown: kept
                    }
                if (busy) {
                    idleSince = SystemClock.elapsedRealtime()
                } else if (SystemClock.elapsedRealtime() - idleSince >= IDLE_MS) {
                    runtime.stoppedWhenIdle = true
                    released = true
                    runtime.stop()
                    stopSelf()
                    return@launch
                }
            }
        }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        seen = requests.get()
        when (intent?.action) {
            ACTION_STOP -> {
                released = true // the user's Stop: onDestroy must not stop a newer start again
                runtime.stop()
                stopSelf()
            }
            // Nothing left to keep alive. stopSelfResult refuses when a newer start
            // (ensureRunning for a new setup or engine) came after this signal.
            ACTION_RELEASE_IDLE -> if (!runtime.hasWork() && stopSelfResult(startId)) released = true
        }
        return START_NOT_STICKY
    }

    override fun onDestroy() {
        // A start asked after this instance's last command is for the next instance.
        if (requests.get() == seen) alive = false
        scope.cancel()
        if (!released) runtime.stop() // Android ended the service with work still kept alive
        super.onDestroy()
    }

    /** The engine's events, as the app reads them, until the server changes. */
    private suspend fun follow(server: Server) {
        var wait = 500L
        while (true) {
            try {
                Api(server).events(onOpen = { wait = 500L }) { name, data ->
                    if (name == "meta") scope.launch { changed(Meta.from(data)) }
                }
            } catch (e: CancellationException) {
                throw e
            } catch (e: ApiError) {
                if (e.status == 401) return // the token changed: a new server comes through activeServer
            } catch (_: Exception) {
            }
            delay(wait)
            wait = (wait * 2).coerceAtMost(8000L)
        }
    }

    private fun changed(m: Meta) {
        val wasRunning = running.put(m.chat, m.running)
        val approval = m.approval?.id
        val question = m.question?.id
        if (!runtime.appForeground) {
            if (wasRunning == true && !m.running) notifier.show(m.chat, m.title, "Done.")
            if (m.approval != null && approvals[m.chat] != approval) notifier.show(m.chat, m.title, "Waiting for your approval: " + m.approval.title)
            if (m.question != null && questions[m.chat] != question) notifier.show(m.chat, m.title, m.question.questions.firstOrNull()?.text ?: "1loop has a question")
        }
        if (approval != null) approvals[m.chat] = approval else approvals.remove(m.chat)
        if (question != null) questions[m.chat] = question else questions.remove(m.chat)
    }

    private fun notification(text: String) = NotificationCompat.Builder(this, CHANNEL)
        .setSmallIcon(R.drawable.ic_launcher_foreground)
        .setContentTitle("1loop")
        .setContentText(text)
        .setOngoing(true)
        .setSilent(true)
        .setContentIntent(
            PendingIntent.getActivity(
                this, 0, Intent(this, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP),
                PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
            ),
        )
        .addAction(
            0, "Stop",
            PendingIntent.getService(
                this, 1, Intent(this, LocalRuntimeService::class.java).setAction(ACTION_STOP),
                PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
            ),
        )
        .build()

    companion object {
        private const val CHANNEL = "local_runtime"
        private const val NOTIFICATION = 1
        private const val ACTION_STOP = "io.oneloop.remote.action.STOP_LOCAL"
        private const val ACTION_RELEASE_IDLE = "io.oneloop.remote.action.RELEASE_LOCAL_IDLE"
        private const val IDLE_MS = 15 * 60 * 1000L
        private const val IDLE_CHECK_MS = 60 * 1000L

        // An instance exists or was asked for: the release signal must never
        // recreate a destroyed one, but must reach one about to be created.
        @Volatile private var alive = false
        private val requests = java.util.concurrent.atomic.AtomicLong()

        /** Called by the runtime before it installs or starts anything. */
        fun ensureRunning(context: Context) {
            val previous = alive
            val request = requests.incrementAndGet()
            alive = true // before the start: a release sent before onCreate must not be lost
            try {
                context.startForegroundService(Intent(context, LocalRuntimeService::class.java))
            } catch (failure: RuntimeException) {
                if (requests.get() == request) alive = previous
                throw failure
            }
        }

        /** Called by the runtime once its setup, start or engine has ended: stops the service if idle. */
        fun releaseIfIdle(context: Context) {
            if (!alive) return
            runCatching { context.startService(Intent(context, LocalRuntimeService::class.java).setAction(ACTION_RELEASE_IDLE)) }
        }
    }
}
