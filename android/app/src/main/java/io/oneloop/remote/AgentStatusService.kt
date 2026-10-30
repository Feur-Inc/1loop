package io.oneloop.remote

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.content.res.Configuration
import android.os.Build
import android.os.Bundle
import android.os.IBinder
import androidx.compose.runtime.Composable
import androidx.compose.ui.platform.ComposeView
import androidx.compose.ui.platform.ViewCompositionStrategy
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleOwner
import androidx.lifecycle.LifecycleRegistry
import androidx.lifecycle.setViewTreeLifecycleOwner
import androidx.savedstate.SavedStateRegistry
import androidx.savedstate.SavedStateRegistryController
import androidx.savedstate.SavedStateRegistryOwner
import androidx.savedstate.setViewTreeSavedStateRegistryOwner
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.launch

/**
 * While a model runs (AgentStatus): an ongoing notification, promoted to a
 * Live Update (the status bar chip) from Android 16, with Stop and Open; and,
 * when chosen and allowed, the floating badge over other apps while 1loop is
 * not on screen.
 */
class AgentStatusService : Service(), LifecycleOwner, SavedStateRegistryOwner {
    private val registry = LifecycleRegistry(this)
    private val savedState = SavedStateRegistryController.create(this)
    override val lifecycle: Lifecycle get() = registry
    override val savedStateRegistry: SavedStateRegistry get() = savedState.savedStateRegistry

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main)
    private var badge: BadgeWindow? = null

    override fun onCreate() {
        super.onCreate()
        savedState.performRestore(null)
        registry.currentState = Lifecycle.State.RESUMED
        active = true
        getSystemService(NotificationManager::class.java).createNotificationChannel(
            NotificationChannel(CHANNEL, "While a model runs", NotificationManager.IMPORTANCE_LOW).apply {
                description = "What the agent is doing, with Stop and Open"
                setShowBadge(false)
            },
        )
        val first = notification(AgentStatus.status.value)
        if (Build.VERSION.SDK_INT >= 34) startForeground(ID, first, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)
        else startForeground(ID, first)
        scope.launch {
            AgentStatus.status.combine(AgentStatus.foreground) { status, shown -> status to shown }.collect { (status, shown) ->
                if (status == null) return@collect
                getSystemService(NotificationManager::class.java).notify(ID, notification(status))
                showBadge(status, shown)
            }
        }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        val chat = intent?.getStringExtra(EXTRA_CHAT)
        if (chat != null) when (intent.action) {
            ACTION_STOP -> AgentStatus.stop(chat)
            ACTION_DISMISS -> AgentStatus.dismiss(this, chat)
        }
        return START_NOT_STICKY
    }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onDestroy() {
        active = false
        removeBadge()
        scope.cancel()
        registry.currentState = Lifecycle.State.DESTROYED
        super.onDestroy()
    }

    // -- the notification -----------------------------------------------------

    private fun notification(status: RunStatus?): Notification {
        val title = status?.title?.ifEmpty { null } ?: "1loop"
        val text = status?.label?.ifEmpty { null } ?: "Working…"
        val open = PendingIntent.getActivity(
            this, 2, openIntent(this, status?.chat), PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        val builder = Notification.Builder(this, CHANNEL)
            .setSmallIcon(R.drawable.ic_launcher_foreground)
            .setContentTitle(title)
            .setContentText(text)
            .setContentIntent(open)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .setCategory(Notification.CATEGORY_PROGRESS)
            // Its own group: otherwise Android groups it with "Done." and the other chats'
            // notifications, and a grouped notification is no Live Update (Samsung's Now Bar).
            .setGroup(GROUP)
            // One UI shows a live notification (Now Bar) only when its content may show
            // on the lock screen: what the agent does, like a timer or a delivery.
            .setVisibility(Notification.VISIBILITY_PUBLIC)
        if (status != null) {
            // Running: Stop it. Finished (the badge kept it): put it away.
            val (label, action) = if (status.finished) "Dismiss" to ACTION_DISMISS else "Stop" to ACTION_STOP
            val button = PendingIntent.getService(
                this, 3, Intent(this, AgentStatusService::class.java).setAction(action).putExtra(EXTRA_CHAT, status.chat),
                PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
            )
            builder.addAction(Notification.Action.Builder(null, label, button).build())
            builder.addAction(Notification.Action.Builder(null, "Open", open).build())
        }
        // Done: no progress, and no longer a Live Update.
        if (status?.finished == true) return builder.setOngoing(false).setCategory(Notification.CATEGORY_STATUS).build()
        val total = status?.todo?.size ?: 0
        if (Build.VERSION.SDK_INT >= 36) {
            // A Live Update: the chip shows the task count, or that it works. The extra, not
            // setRequestPromotedOngoing (36.1): Android 16.0 reads it too.
            builder.addExtras(Bundle().apply { putBoolean(Notification.EXTRA_REQUEST_PROMOTED_ONGOING, true) })
            builder.setShortCriticalText(if (total > 0) "${status!!.done}/$total" else "1loop")
            if (total > 0) builder.setStyle(
                Notification.ProgressStyle().setProgress(status!!.done)
                    .addProgressSegment(Notification.ProgressStyle.Segment(total)),
            ) else builder.setStyle(Notification.ProgressStyle().setProgressIndeterminate(true))
        } else if (total > 0) builder.setProgress(total, status!!.done, false)
        else builder.setProgress(0, 0, true)
        return builder.build()
    }

    // -- the floating badge ---------------------------------------------------

    // Put away on the ✕ ring while it ran: gone for this chat until 1loop is back on
    // screen, or until the agent asks a new question there.
    private var hiddenChat: String? = null
    private var hiddenQuestion: String? = null

    private fun showBadge(status: RunStatus, shown: Boolean) {
        if (shown) hiddenChat = null
        val question = status.question?.id
        if (status.chat == hiddenChat && question != null && question != hiddenQuestion) hiddenChat = null
        val wanted = !shown && status.chat != hiddenChat && AgentStatus.style(this) == StatusStyle.Badge && AgentStatus.canDrawOverlays(this)
        if (!wanted) return removeBadge()
        val window = badge ?: BadgeWindow(this, ::composeView, onRemove = ::removedByUser) { ui ->
            StatusBadge(
                ui = ui,
                onStop = AgentStatus::stop,
                onAnswer = AgentStatus::answer,
                onTyping = ::setTyping,
                onTalk = { chat -> startActivity(openIntent(this@AgentStatusService, chat).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) },
                onDismiss = { chat -> AgentStatus.dismiss(this@AgentStatusService, chat) },
            )
        }.also { badge = it }
        // Phone use: translucent and click-through, so the agent sees and taps behind it.
        passThrough = status.phoneUse
        window.update(passThrough, typing)
    }

    private fun composeView(content: @Composable () -> Unit) = ComposeView(this).apply {
        setViewTreeLifecycleOwner(this@AgentStatusService)
        setViewTreeSavedStateRegistryOwner(this@AgentStatusService)
        setViewCompositionStrategy(ViewCompositionStrategy.DisposeOnDetachedFromWindow)
        setContent { OneLoopTheme { content() } }
    }

    // Let go on the ✕ ring: a finished run is put away, a running one hidden.
    private fun removedByUser() {
        val status = AgentStatus.status.value
        removeBadge()
        if (status == null) return
        if (status.finished) AgentStatus.dismiss(this, status.chat)
        else {
            hiddenChat = status.chat
            hiddenQuestion = status.question?.id
        }
    }

    // The badge takes the keyboard only while an answer is typed: otherwise it would
    // take it from the app the user is in.
    private var typing = false
    private var passThrough = false

    private fun setTyping(value: Boolean) {
        if (typing == value) return
        typing = value
        badge?.update(passThrough, typing)
    }

    override fun onConfigurationChanged(newConfig: Configuration) {
        super.onConfigurationChanged(newConfig)
        badge?.place()
    }

    private fun removeBadge() {
        badge?.remove()
        badge = null
    }

    companion object {
        const val CHANNEL = "running"
        const val GROUP = "io.oneloop.remote.RUNNING"
        const val ID = 4317
        const val ACTION_STOP = "io.oneloop.remote.STOP_RUN"
        const val ACTION_DISMISS = "io.oneloop.remote.DISMISS_RUN"
        const val EXTRA_CHAT = "io.oneloop.remote.CHAT"
        @Volatile var active = false

        /** 1loop on this chat (MainActivity opens it). */
        fun openIntent(context: Context, chat: String?): Intent =
            Intent(context, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP).apply { chat?.let { putExtra(EXTRA_CHAT, it) } }
    }
}
