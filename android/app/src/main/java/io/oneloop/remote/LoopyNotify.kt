// SPDX-License-Identifier: MPL-2.0

package io.oneloop.remote

import android.app.PendingIntent
import android.app.job.JobInfo
import android.app.job.JobParameters
import android.app.job.JobScheduler
import android.app.job.JobService
import android.content.BroadcastReceiver
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.edit
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch
import org.json.JSONArray
import org.json.JSONObject

// Loopy's notifications: what it says on its own, and the cards that wait,
// with their buttons (an answer without opening 1loop). They come from the
// event stream while the app or this phone's engine runs, else from a look
// every quarter of an hour at the Loopy of the target it lives on.

/** Shows what Loopy says; [message] is the card it is about, whose buttons it carries. */
fun notifyLoopy(context: Context, server: Server, title: String, text: String, message: JSONObject?) {
    if (Build.VERSION.SDK_INT >= 33 && context.checkSelfPermission(android.Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) return
    val said = message?.let { LoopyMessage.from(it) }
    val id = said?.id ?: "loopy:$text"
    if (said != null && !LoopySeen.first(context, said.id)) return // the look and the stream say it once
    val open = PendingIntent.getActivity(
        context, 2, Intent(context, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP).putExtra(LOOPY_OPEN, said?.id ?: ""),
        PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
    )
    val builder = NotificationCompat.Builder(context, Notifier.LOOPY)
        .setSmallIcon(R.drawable.ic_launcher_foreground)
        .setContentTitle(title.ifEmpty { "Loopy" }).setContentText(text)
        .setStyle(NotificationCompat.BigTextStyle().bigText(text))
        .setContentIntent(open).setAutoCancel(true)
    // A card that takes no typing is answered from here; a draft only opens it, so that it is read first.
    val actions = said?.card?.takeIf { !it.editable }?.plainActions.orEmpty().take(3)
    actions.forEachIndexed { i, action ->
        val answer = PendingIntent.getBroadcast(
            context, (id + action).hashCode(),
            Intent(context, LoopyActionReceiver::class.java).setAction("loopy:$id:$i").putExtra("server", server.key).putExtra("message", id).putExtra("answer", action),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        // From a locked phone too, once unlocked.
        builder.addAction(NotificationCompat.Action.Builder(0, action, answer).setAuthenticationRequired(true).build())
    }
    Notifier(context)
    NotificationManagerCompat.from(context).notify(id.hashCode(), builder.build())
}

const val LOOPY_OPEN = "io.oneloop.remote.LOOPY_OPEN"

/** The cards already notified. */
private object LoopySeen {
    fun first(context: Context, id: String): Boolean {
        val prefs = context.getSharedPreferences("loopy", Context.MODE_PRIVATE)
        val seen = prefs.getString("seen", "").orEmpty().split(' ').filter { it.isNotEmpty() }
        if (id in seen) return false
        prefs.edit { putString("seen", (seen + id).takeLast(100).joinToString(" ")) }
        return true
    }
}

/** The target Loopy lives on, as the app keeps it: this phone's running engine, or a paired computer or server. */
fun loopyServer(context: Context, key: String? = null): Server? {
    val prefs = context.getSharedPreferences("1loop", Context.MODE_PRIVATE)
    val at = key ?: prefs.getString("loopyAt", null) ?: prefs.getString("last", null) ?: return null
    if (at == PHONE_TARGET) return AndroidLocalRuntime.get(context).activeServer.value
    val array = runCatching { JSONArray(prefs.getString("servers", "[]")) }.getOrNull() ?: return null
    return (0 until array.length()).map { array.getJSONObject(it) }.map {
        Server(it.getString("name"), it.getString("host"), it.getInt("port"), it.optString("token"), it.optString("scheme", "http"), it.optString("fingerprint"), desktop = it.optBoolean("desktop"))
    }.firstOrNull { it.key == at && it.paired }
}

/** A button of a notification of Loopy's: its answer goes to the card. */
class LoopyActionReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        val id = intent.getStringExtra("message") ?: return
        val answer = intent.getStringExtra("answer") ?: return
        val server = loopyServer(context, intent.getStringExtra("server"))
        val manager = NotificationManagerCompat.from(context)
        val pending = goAsync()
        CoroutineScope(Dispatchers.IO).launch {
            try {
                if (server == null) throw ApiError("Its Loopy cannot be reached now: open 1loop.")
                Api(server).post("/api/loopy/answer", JSONObject().put("message", id).put("answer", answer))
                manager.cancel(id.hashCode())
            } catch (failure: Exception) {
                if (Build.VERSION.SDK_INT < 33 || context.checkSelfPermission(android.Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED) {
                    val open = PendingIntent.getActivity(context, 2, Intent(context, MainActivity::class.java).putExtra(LOOPY_OPEN, id), PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
                    manager.notify(
                        id.hashCode(),
                        NotificationCompat.Builder(context, Notifier.LOOPY).setSmallIcon(R.drawable.ic_launcher_foreground)
                            .setContentTitle("“$answer” did not reach Loopy").setContentText(failure.message).setContentIntent(open).setAutoCancel(true).build(),
                    )
                }
            } finally {
                pending.finish()
            }
        }
    }
}

/** Looks every quarter of an hour at what waits for the user, while 1loop is closed. */
class LoopyCheckJob : JobService() {
    private var work: Job? = null

    override fun onStartJob(params: JobParameters): Boolean {
        work = CoroutineScope(Dispatchers.IO).launch {
            runCatching {
                val server = loopyServer(applicationContext) ?: return@runCatching
                if (server.localPhone) return@runCatching // its own service says it
                val view = Api(server).get("/api/loopy")
                val waiting = view.strings("waiting").toSet()
                for (message in view.objects("messages") { it }) {
                    if (message.optString("id") !in waiting) continue
                    val card = LoopyCard.from(message.optJSONObject("card") ?: continue)
                    notifyLoopy(applicationContext, server, card.title, card.body.ifEmpty { card.subtitle.ifEmpty { "Waiting for you" } }, message)
                }
            }
            jobFinished(params, false)
        }
        return true
    }

    override fun onStopJob(params: JobParameters): Boolean {
        work?.cancel()
        return true
    }

    companion object {
        private const val ID = 4402

        /** Once Loopy is used, from its home target. */
        fun schedule(context: Context) {
            val scheduler = context.getSystemService(JobScheduler::class.java)
            if (scheduler.getPendingJob(ID) != null) return
            scheduler.schedule(
                JobInfo.Builder(ID, ComponentName(context, LoopyCheckJob::class.java))
                    .setRequiredNetworkType(JobInfo.NETWORK_TYPE_ANY).setPeriodic(15 * 60_000L).setPersisted(true).build(),
            )
        }
    }
}
