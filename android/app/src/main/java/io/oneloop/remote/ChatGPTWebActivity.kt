package io.oneloop.remote

import android.annotation.SuppressLint
import android.content.Context
import android.content.Intent
import android.os.Bundle
import android.webkit.RenderProcessGoneDetail
import android.webkit.WebResourceRequest
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.TextView
import androidx.activity.ComponentActivity
import androidx.activity.OnBackPressedCallback
import androidx.webkit.ProfileStore

/**
 * ChatGPT's own pages for a ChatGPT (Web) account (its sign-in, its Plugins),
 * in the account's WebView profile: the cookies the agent's ChatGPT pages
 * use, apart from Browser Use's.
 */
class ChatGPTWebActivity : ComponentActivity() {
    private var page: WebView? = null
    private var profile = ""

    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        profile = intent.getStringExtra(PROFILE).orEmpty()
        val address = intent.getStringExtra(ADDRESS)?.takeIf { it.startsWith("https://chatgpt.com/") } ?: "https://chatgpt.com/"
        if (!profile.matches(Regex("chatgpt-[a-zA-Z0-9-]{1,80}")) || !LocalBrowser.profilesSupported()) {
            setContentView(TextView(this).apply {
                text = "ChatGPT (Web) needs a newer Android System WebView, with separate browser profiles. Update it from Google Play."
                setPadding(48, 96, 48, 48)
            })
            return
        }
        val view = WebView(this)
        LocalBrowser.usingProfile(view, profile)
        LocalBrowser.screens++
        page = view
        view.settings.apply {
            javaScriptEnabled = true
            domStorageEnabled = true
            allowFileAccess = false
            allowContentAccess = false
            mixedContentMode = android.webkit.WebSettings.MIXED_CONTENT_NEVER_ALLOW
            javaScriptCanOpenWindowsAutomatically = false
            setSupportMultipleWindows(false)
            setGeolocationEnabled(false)
            mediaPlaybackRequiresUserGesture = true
        }
        view.webViewClient = object : WebViewClient() {
            override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean =
                request.url.scheme != "https"

            override fun onRenderProcessGone(view: WebView, detail: RenderProcessGoneDetail): Boolean {
                finish()
                return true
            }
        }
        setContentView(view)
        onBackPressedDispatcher.addCallback(this, object : OnBackPressedCallback(true) {
            override fun handleOnBackPressed() {
                if (view.canGoBack()) view.goBack() else finish()
            }
        })
        view.loadUrl(address)
    }

    override fun onResume() {
        super.onResume()
        page?.onResume()
    }

    override fun onPause() {
        page?.onPause()
        super.onPause()
    }

    override fun onDestroy() {
        page?.let {
            ProfileStore.getInstance().getProfile(profile)?.cookieManager?.flush()
            (it.parent as? android.view.ViewGroup)?.removeView(it)
            it.stopLoading()
            it.destroy()
            LocalBrowser.screens--
        }
        page = null
        super.onDestroy()
    }

    companion object {
        private const val PROFILE = "profile"
        private const val ADDRESS = "address"

        fun open(context: Context, profile: String, address: String = "https://chatgpt.com/") {
            context.startActivity(Intent(context, ChatGPTWebActivity::class.java).putExtra(PROFILE, profile).putExtra(ADDRESS, address))
        }
    }
}
