package io.oneloop.remote

import android.annotation.SuppressLint
import android.content.Context
import android.graphics.Bitmap
import android.graphics.Canvas
import android.os.Process
import android.util.Base64
import android.webkit.CookieManager
import android.webkit.WebResourceRequest
import android.webkit.WebStorage
import android.webkit.WebView
import android.webkit.WebViewClient
import android.webkit.RenderProcessGoneDetail
import android.webkit.WebViewDatabase
import androidx.webkit.WebViewCompat
import androidx.webkit.WebViewFeature
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.ByteArrayOutputStream
import org.json.JSONObject

/**
 * Web content has no JavaScript interface to the agent, accounts, files or ADB.
 * With a profile (a ChatGPT (Web) account), its WebViews keep their own
 * cookies and storage, apart from Browser Use's.
 */
class LocalBrowser(private val context: Context, private val profile: String? = null) {
    private val pages = mutableMapOf<String, WebView>() // Main thread owns views.

    suspend fun view(id: String): WebView? = withContext(Dispatchers.Main) { pages[id] }

    /** Erases what WebView keeps: cookies and logins, site storage, cache, form data, the open tabs' history. */
    suspend fun clearData() = withContext(Dispatchers.Main) {
        val cookies = CompletableDeferred<Unit>()
        CookieManager.getInstance().removeAllCookies { cookies.complete(Unit) }
        cookies.await()
        CookieManager.getInstance().flush()
        WebStorage.getInstance().deleteAllData()
        WebViewDatabase.getInstance(context).clearHttpAuthUsernamePassword()
        // The cache is the app's, shared by every WebView: any view clears it.
        val spare = if (pages.isEmpty()) WebView(context) else null
        (pages.values.firstOrNull() ?: spare!!).apply { clearCache(true); clearFormData() }
        pages.values.forEach { it.clearHistory() }
        spare?.destroy()
    }

    @SuppressLint("SetJavaScriptEnabled")
    suspend fun execute(arguments: JSONObject): JSONObject = withContext(Dispatchers.Main) {
        val id = arguments.getString("id")
        require(id.matches(Regex("[a-zA-Z0-9-]{1,80}"))) { "Invalid browser tab." }
        when (arguments.getString("action")) {
            "open" -> {
                if (!pages.containsKey(id)) {
                    WebView.setWebContentsDebuggingEnabled(true)
                    val view = WebView(context)
                    profile?.let { usingProfile(view, it) }
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
                        override fun shouldOverrideUrlLoading(webView: WebView, request: WebResourceRequest): Boolean =
                            request.url.scheme !in listOf("https", "http", "about")

                        // The shared renderer ended (memory, or another browser's close): this
                        // tab ends instead of the app; the engine opens a new one when needed.
                        override fun onRenderProcessGone(webView: WebView, detail: RenderProcessGoneDetail): Boolean {
                            if (pages[id] === webView) pages.remove(id)
                            (webView.parent as? android.view.ViewGroup)?.removeView(webView)
                            webView.destroy()
                            return true
                        }
                    }
                    pages[id] = view
                    size(view, arguments)
                    view.loadUrl("about:blank#1loop-$id")
                }
                JSONObject().put("socket", "webview_devtools_remote_${Process.myPid()}")
                    .put("marker", "about:blank#1loop-$id")
            }
            "resize" -> {
                size(pages[id] ?: error("Browser tab is closed."), arguments)
                JSONObject()
            }
            "capture" -> {
                val view = pages[id] ?: error("Browser tab is closed.")
                check(!arguments.optBoolean("full")) { "Full-page capture is not available in Android WebView; capture the viewport and scroll." }
                check(view.width.toLong() * view.height <= 16_000_000) { "Viewport is too large to capture; choose smaller dimensions." }
                val bitmap = Bitmap.createBitmap(view.width, view.height, Bitmap.Config.ARGB_8888)
                val output = ByteArrayOutputStream()
                try {
                    view.draw(Canvas(bitmap))
                    bitmap.compress(Bitmap.CompressFormat.PNG, 100, output)
                    JSONObject().put("mime", "image/png").put("data", Base64.encodeToString(output.toByteArray(), Base64.NO_WRAP))
                } finally { bitmap.recycle() }
            }
            "close" -> {
                pages.remove(id)?.let { view ->
                    (view.parent as? android.view.ViewGroup)?.removeView(view)
                    view.stopLoading()
                    view.destroy()
                }
                JSONObject()
            }
            "close_all" -> {
                close()
                JSONObject()
            }
            else -> error("Unknown browser operation.")
        }
    }

    private fun size(view: WebView, arguments: JSONObject) {
        val width = arguments.optInt("width", 1280).coerceIn(240, 3840)
        val height = arguments.optInt("height", 800).coerceIn(200, 2160)
        val mobile = arguments.optBoolean("mobile")
        val density = context.resources.displayMetrics.density
        val pixelsWide = kotlin.math.ceil(width * density.toDouble()).toInt()
        val pixelsHigh = kotlin.math.ceil(height * density.toDouble()).toInt()
        view.settings.apply {
            useWideViewPort = false
            loadWithOverviewMode = false
            val native = android.webkit.WebSettings.getDefaultUserAgent(context)
            userAgentString = if (mobile) native else native
                .replace(Regex("\\(Linux; Android[^)]*\\)"), "(X11; Linux x86_64)")
                .replace("; wv", "").replace(" Mobile", "").replace(Regex("Version/[0-9.]+ "), "")
        }
        view.layoutParams = android.view.ViewGroup.LayoutParams(pixelsWide, pixelsHigh)
        view.measure(android.view.View.MeasureSpec.makeMeasureSpec(pixelsWide, android.view.View.MeasureSpec.EXACTLY),
            android.view.View.MeasureSpec.makeMeasureSpec(pixelsHigh, android.view.View.MeasureSpec.EXACTLY))
        view.layout(0, 0, pixelsWide, pixelsHigh)
    }

    suspend fun close() = withContext(Dispatchers.Main) {
        if (pages.isEmpty()) return@withContext
        val renderers = pages.values.mapNotNull { view ->
            if (android.os.Build.VERSION.SDK_INT >= 29) view.webViewRenderProcess else null
        }.toSet()
        android.webkit.CookieManager.getInstance().flush()
        pages.values.toList().forEach { view ->
            (view.parent as? android.view.ViewGroup)?.removeView(view)
            view.destroy()
        }
        pages.clear()
        // The renderer and the debugging socket are the app's: they stay while
        // another browser (Browser Use, a ChatGPT account, its sign-in) has pages.
        if (synchronized(open) { open.any { it.pages.isNotEmpty() } } || screens > 0) return@withContext
        if (android.os.Build.VERSION.SDK_INT >= 29) renderers.forEach { it.terminate() }
        WebView.setWebContentsDebuggingEnabled(false)
    }

    init {
        synchronized(open) { open += this }
    }

    companion object {
        private val open = mutableListOf<LocalBrowser>()

        /** WebViews shown outside these browsers (the ChatGPT sign-in), on the main thread. */
        var screens = 0

        /** Whether this phone's Android System WebView keeps separate profiles (androidx.webkit's MULTI_PROFILE). */
        fun profilesSupported() = WebViewFeature.isFeatureSupported(WebViewFeature.MULTI_PROFILE)

        /** Before the view loads anything: its cookies and storage are the profile's. */
        fun usingProfile(view: WebView, profile: String) {
            check(profilesSupported()) { "ChatGPT (Web) needs a newer Android System WebView, with separate browser profiles." }
            androidx.webkit.ProfileStore.getInstance().getOrCreateProfile(profile)
            WebViewCompat.setProfile(view, profile)
        }
    }
}
