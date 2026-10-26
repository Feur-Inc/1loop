package io.oneloop.remote

import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.SystemBarStyle
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.lifecycle.lifecycleScope
import kotlinx.coroutines.launch

class MainActivity : ComponentActivity() {
    private lateinit var app: RemoteApp

    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge(SystemBarStyle.dark(android.graphics.Color.TRANSPARENT), SystemBarStyle.dark(android.graphics.Color.TRANSPARENT))
        super.onCreate(savedInstanceState)
        app = RemoteApp(applicationContext, lifecycleScope)
        setContent { OneLoopTheme { Root(app) } }
        if (Build.VERSION.SDK_INT >= 33) requestPermissions(arrayOf(android.Manifest.permission.POST_NOTIFICATIONS), 1)
        UpdateCheckJob.schedule(applicationContext)
        receiveShared(intent)
        showUpdate(intent)
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        receiveShared(intent)
        showUpdate(intent)
    }

    /** The update notification was tapped. */
    private fun showUpdate(intent: Intent?) {
        if (intent?.getBooleanExtra(Notifier.SHOW_UPDATE, false) == true) app.updateAsked = true
    }

    /** Files shared to 1loop from another app (Share → 1loop). */
    private fun receiveShared(intent: Intent?) {
        val uris = when (intent?.action) {
            Intent.ACTION_SEND -> listOfNotNull(
                if (Build.VERSION.SDK_INT >= 33) intent.getParcelableExtra(Intent.EXTRA_STREAM, Uri::class.java)
                else @Suppress("DEPRECATION") intent.getParcelableExtra(Intent.EXTRA_STREAM),
            )
            Intent.ACTION_SEND_MULTIPLE -> (if (Build.VERSION.SDK_INT >= 33) intent.getParcelableArrayListExtra(Intent.EXTRA_STREAM, Uri::class.java)
                else @Suppress("DEPRECATION") intent.getParcelableArrayListExtra(Intent.EXTRA_STREAM)).orEmpty()
            else -> emptyList()
        }
        app.shared.addAll(uris)
    }

    override fun onStart() {
        super.onStart()
        app.foreground = true
        AndroidLocalRuntime.get(applicationContext).appForeground = true
        AndroidLocalRuntime.get(applicationContext).refreshPhoneAccess()
        app.resumeLocal()
        app.updates.resume() // back from the "install unknown apps" page
        lifecycleScope.launch { app.checkUpdates() }
    }

    override fun onStop() {
        app.foreground = false
        AndroidLocalRuntime.get(applicationContext).appForeground = false
        super.onStop()
    }
}
