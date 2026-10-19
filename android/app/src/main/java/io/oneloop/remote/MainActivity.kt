package io.oneloop.remote

import android.os.Build
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.SystemBarStyle
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.lifecycle.lifecycleScope

class MainActivity : ComponentActivity() {
    private lateinit var app: RemoteApp

    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge(SystemBarStyle.dark(android.graphics.Color.TRANSPARENT), SystemBarStyle.dark(android.graphics.Color.TRANSPARENT))
        super.onCreate(savedInstanceState)
        app = RemoteApp(applicationContext, lifecycleScope)
        setContent { OneLoopTheme { Root(app) } }
        if (Build.VERSION.SDK_INT >= 33) requestPermissions(arrayOf(android.Manifest.permission.POST_NOTIFICATIONS), 1)
    }

    override fun onStart() {
        super.onStart()
        app.foreground = true
        AndroidLocalRuntime.get(applicationContext).appForeground = true
        AndroidLocalRuntime.get(applicationContext).refreshPhoneAccess()
    }

    override fun onStop() {
        app.foreground = false
        AndroidLocalRuntime.get(applicationContext).appForeground = false
        super.onStop()
    }
}
