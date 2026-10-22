package io.oneloop.remote

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat

/**
 * Notifications while the app is in the background: a chat finished, or
 * waits for an approval or an answer on the computer; a new version is out.
 */
class Notifier(private val context: Context) {
    init {
        val channel = NotificationChannel(CHANNEL, "Chats", NotificationManager.IMPORTANCE_DEFAULT).apply {
            description = "A chat finished or is waiting for you"
        }
        val updates = NotificationChannel(UPDATES, "Updates", NotificationManager.IMPORTANCE_DEFAULT).apply {
            description = "A new version of 1loop is out"
        }
        context.getSystemService(NotificationManager::class.java).createNotificationChannels(listOf(channel, updates))
    }

    fun show(chat: String, title: String, text: String) {
        if (Build.VERSION.SDK_INT >= 33 && context.checkSelfPermission(android.Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) return
        val open = PendingIntent.getActivity(
            context, 0, Intent(context, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        val notification = NotificationCompat.Builder(context, CHANNEL)
            .setSmallIcon(R.drawable.ic_launcher_foreground)
            .setContentTitle(title.ifEmpty { "1loop" })
            .setContentText(text)
            .setContentIntent(open)
            .setAutoCancel(true)
            .build()
        NotificationManagerCompat.from(context).notify(chat.hashCode(), notification)
    }

    /** A new version: tapping opens 1loop on its update (MainActivity). */
    fun showUpdate(title: String, text: String) {
        if (Build.VERSION.SDK_INT >= 33 && context.checkSelfPermission(android.Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) return
        val open = PendingIntent.getActivity(
            context, 1, Intent(context, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP).putExtra(SHOW_UPDATE, true),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        val notification = NotificationCompat.Builder(context, UPDATES)
            .setSmallIcon(R.drawable.ic_launcher_foreground)
            .setContentTitle(title)
            .setContentText(text)
            .setContentIntent(open)
            .setAutoCancel(true)
            .build()
        NotificationManagerCompat.from(context).notify(UPDATES.hashCode(), notification)
    }

    companion object {
        const val CHANNEL = "chats"
        const val UPDATES = "updates"
        const val SHOW_UPDATE = "io.oneloop.remote.SHOW_UPDATE"
    }
}
