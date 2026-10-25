package io.oneloop.remote

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.graphics.PixelFormat
import android.os.Build
import android.os.Bundle
import android.os.IBinder
import android.view.Gravity
import android.view.WindowManager
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
    private var badge: ComposeView? = null
    private var badgeParams: WindowManager.LayoutParams? = null

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
        if (intent?.action == ACTION_STOP) intent.getStringExtra(EXTRA_CHAT)?.let(AgentStatus::stop)
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
        if (status != null) {
            val stop = PendingIntent.getService(
                this, 3, Intent(this, AgentStatusService::class.java).setAction(ACTION_STOP).putExtra(EXTRA_CHAT, status.chat),
                PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
            )
            builder.addAction(Notification.Action.Builder(null, "Stop", stop).build())
            builder.addAction(Notification.Action.Builder(null, "Open", open).build())
        }
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

    private fun showBadge(status: RunStatus, shown: Boolean) {
        val wanted = !shown && AgentStatus.style(this) == StatusStyle.Badge && AgentStatus.canDrawOverlays(this)
        if (!wanted) return removeBadge()
        val manager = getSystemService(WindowManager::class.java)
        val params = badgeParams ?: WindowManager.LayoutParams(
            WindowManager.LayoutParams.WRAP_CONTENT, WindowManager.LayoutParams.WRAP_CONTENT,
            WindowManager.LayoutParams.TYPE_APPLICATION_OVERLAY,
            // Hardware accelerated: a window added from a service is drawn in software
            // otherwise, and the working ring animates every frame.
            WindowManager.LayoutParams.FLAG_NOT_FOCUSABLE or WindowManager.LayoutParams.FLAG_LAYOUT_NO_LIMITS or
                WindowManager.LayoutParams.FLAG_HARDWARE_ACCELERATED,
            PixelFormat.TRANSLUCENT,
        ).apply {
            gravity = Gravity.TOP or Gravity.END
            y = (resources.displayMetrics.heightPixels * 0.28f).toInt()
        }.also { badgeParams = it }
        // Phone use: translucent and click-through (at most 0.8 opaque, or Android
        // blocks the touches it lets through), so the agent sees and taps behind it.
        val passThrough = WindowManager.LayoutParams.FLAG_NOT_TOUCHABLE
        params.flags = if (status.phoneUse) params.flags or passThrough else params.flags and passThrough.inv()
        params.alpha = if (status.phoneUse) 0.4f else 1f
        val view = badge
        if (view == null) {
            badge = ComposeView(this).apply {
                setViewTreeLifecycleOwner(this@AgentStatusService)
                setViewTreeSavedStateRegistryOwner(this@AgentStatusService)
                setViewCompositionStrategy(ViewCompositionStrategy.DisposeOnDetachedFromWindow)
                setContent {
                    OneLoopTheme {
                        StatusBadge(
                            onStop = AgentStatus::stop,
                            onTalk = { chat -> startActivity(openIntent(this@AgentStatusService, chat).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) },
                            onDrag = { dy -> moveBadge(dy) },
                        )
                    }
                }
            }
            manager.addView(badge, params)
        } else manager.updateViewLayout(view, params)
    }

    private fun moveBadge(dy: Float) {
        val view = badge ?: return
        val params = badgeParams ?: return
        params.y = (params.y + dy.toInt()).coerceIn(0, resources.displayMetrics.heightPixels - view.height)
        getSystemService(WindowManager::class.java).updateViewLayout(view, params)
    }

    private fun removeBadge() {
        badge?.let { runCatching { getSystemService(WindowManager::class.java).removeView(it) } }
        badge = null
    }

    companion object {
        const val CHANNEL = "running"
        const val ID = 4317
        const val ACTION_STOP = "io.oneloop.remote.STOP_RUN"
        const val EXTRA_CHAT = "io.oneloop.remote.CHAT"
        @Volatile var active = false

        /** 1loop on this chat (MainActivity opens it). */
        fun openIntent(context: Context, chat: String?): Intent =
            Intent(context, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP).apply { chat?.let { putExtra(EXTRA_CHAT, it) } }
    }
}
