package chat.localmessenger.android

import android.app.Activity
import android.content.Intent
import android.graphics.Color
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.webkit.CookieManager
import android.webkit.WebChromeClient
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebStorage
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.LinearLayout
import android.widget.TextView
import org.json.JSONObject
import java.io.ByteArrayInputStream

/** Login is the sole WebView owner. It has no exported intent/deep link and no address bar. */
class LoginActivity : Activity() {
    private var web: WebView? = null
    private var completed = false
    private val main = Handler(Looper.getMainLooper())
    private lateinit var caption: TextView
    private val pollCookies = object : Runnable {
        override fun run() { if (!completed) { captureSession(); main.postDelayed(this, 250) } }
    }

    override fun onCreate(state: Bundle?) {
        super.onCreate(state)
        // Only MainActivity can launch this non-exported activity after checking
        // the engine's session state. A separate process keeps Chromium out of
        // the native messaging process after login.
        if (!intent.getBooleanExtra("authOnly", false)) { finish(); return }
        window.addFlags(android.view.WindowManager.LayoutParams.FLAG_SECURE)
        val layout = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL; setBackgroundColor(Color.rgb(12,18,26)); fitsSystemWindows = true }
        caption = TextView(this).apply { text = "Sign in to Facebook\nThis login window closes when you're signed in."; setTextColor(Color.WHITE); setPadding(20,20,20,20) }
        layout.addView(caption)
        web = WebView(this).also { browser ->
            layout.addView(browser, LinearLayout.LayoutParams(-1,0,1f))
            browser.settings.apply {
                javaScriptEnabled = true; domStorageEnabled = true
                allowFileAccess = false; allowContentAccess = false
                mixedContentMode = android.webkit.WebSettings.MIXED_CONTENT_NEVER_ALLOW
                javaScriptCanOpenWindowsAutomatically = false
                setSupportMultipleWindows(true)
                setGeolocationEnabled(false)
            }
            browser.webChromeClient = WebChromeClient() // No popup windows, permissions, file picker, or downloads.
            browser.setDownloadListener { _,_,_,_,_ -> caption.text = "Downloads are unavailable during sign-in." }
            browser.webViewClient = object : WebViewClient() {
                override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean {
                    if (!request.isForMainFrame) return false
                    if (captureSession()) return true
                    val allowed = LoginPolicy.allowsNavigation(request.url.toString())
                    if (!allowed) caption.text = "Only sign-in pages are available here. Complete sign-in or go back."
                    return !allowed
                }
                override fun shouldInterceptRequest(view: WebView, request: WebResourceRequest): WebResourceResponse? {
                    // Also guard redirects/programmatic loads which bypass shouldOverrideUrlLoading.
                    if (request.isForMainFrame && !LoginPolicy.allowsNavigation(request.url.toString())) {
                        main.post { captureSession() }
                        return WebResourceResponse("text/plain", "utf-8", ByteArrayInputStream(ByteArray(0)))
                    }
                    return null
                }
                override fun onPageFinished(view: WebView, url: String) { captureSession() }
            }
        }
        setContentView(layout)
        CookieManager.getInstance().removeAllCookies {
            if (!isFinishing) { web?.loadUrl("https://www.facebook.com/login/"); main.post(pollCookies) }
        }
    }

    private fun captureSession(): Boolean {
        if (completed) return true
        val cookies = LoginPolicy.authenticatedCookies(CookieManager.getInstance().getCookie("https://www.facebook.com")) ?: return false
        completed = true; main.removeCallbacks(pollCookies)
        web?.stopLoading(); web?.visibility = android.view.View.GONE
        setResult(RESULT_OK, Intent().putExtra("cookies", JSONObject(cookies).toString()))
        finish()
        return true
    }

    override fun onDestroy() {
        main.removeCallbacks(pollCookies)
        web?.apply { stopLoading(); clearHistory(); clearCache(true); (parent as? android.view.ViewGroup)?.removeView(this); destroy() }
        web = null
        CookieManager.getInstance().removeAllCookies(null); CookieManager.getInstance().flush()
        WebStorage.getInstance().deleteAllData()
        super.onDestroy()
    }
}
