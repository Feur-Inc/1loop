package io.oneloop.remote

import android.annotation.SuppressLint
import android.content.Context
import android.content.Intent
import android.graphics.Color
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.view.Gravity
import android.widget.Button
import android.widget.LinearLayout
import android.webkit.RenderProcessGoneDetail
import android.webkit.WebResourceRequest
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.TextView
import androidx.activity.ComponentActivity
import androidx.activity.OnBackPressedCallback
import androidx.webkit.ProfileStore
import androidx.webkit.WebViewFeature

/**
 * ChatGPT's own pages for a ChatGPT (Web) account (its sign-in, its Plugins),
 * in the account's WebView profile: the cookies the agent's ChatGPT pages
 * use, apart from Browser Use's.
 */
class ChatGPTWebActivity : ComponentActivity() {
    private var page: WebView? = null
    private var profile = ""
    private val ticks = Handler(Looper.getMainLooper())

    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        profile = intent.getStringExtra(PROFILE).orEmpty()
        val address = intent.getStringExtra(ADDRESS)?.takeIf { it.startsWith("https://chatgpt.com/") } ?: "https://chatgpt.com/"
        val signIn = address == "https://chatgpt.com/"
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
        // Done closes it; a sign-in also closes it once ChatGPT shows the
        // signed-in page (after having shown its sign-in button).
        val done = Button(this).apply { text = "Done"; setOnClickListener { finish() } }
        val title = TextView(this).apply {
            text = if (signIn) "Sign in to ChatGPT" else "ChatGPT"
            setTextColor(Color.WHITE)
            textSize = 16f
        }
        setContentView(LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setBackgroundColor(Color.rgb(20, 20, 20))
            fitsSystemWindows = true
            addView(LinearLayout(context).apply {
                gravity = Gravity.CENTER_VERTICAL
                setPadding(40, 8, 16, 8)
                addView(title, LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1f))
                addView(done)
            })
            addView(view, LinearLayout.LayoutParams(LinearLayout.LayoutParams.MATCH_PARENT, 0, 1f))
        })
        if (signIn) {
            var signedOut = false
            val check = object : Runnable {
                override fun run() {
                    view.evaluateJavascript(SIGNED_IN) { state ->
                        when (state) {
                            "\"out\"" -> signedOut = true
                            "\"in\"" -> if (signedOut) { finish(); return@evaluateJavascript }
                        }
                        ticks.postDelayed(this, 1000)
                    }
                }
            }
            ticks.postDelayed(check, 1000)
        }
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
        ticks.removeCallbacksAndMessages(null)
        page?.let {
            if (WebViewFeature.isFeatureSupported(WebViewFeature.MULTI_PROFILE)) ProfileStore.getInstance().getProfile(profile)?.cookieManager?.flush()
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

        // Signed out: ChatGPT's sign-in button. Signed in: the message box without it.
        private const val SIGNED_IN = "(()=>{const login=document.querySelector('[data-testid=\"login-button\"]');if(login)return 'out';return document.querySelector('form .ProseMirror')?'in':''})()"

        fun open(context: Context, profile: String, address: String = "https://chatgpt.com/") {
            context.startActivity(Intent(context, ChatGPTWebActivity::class.java).putExtra(PROFILE, profile).putExtra(ADDRESS, address))
        }
    }
}
