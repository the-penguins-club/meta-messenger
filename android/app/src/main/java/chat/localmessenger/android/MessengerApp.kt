package chat.localmessenger.android

import android.app.Activity
import android.app.Application
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import org.json.JSONObject
import java.io.File
import java.util.concurrent.Executors

class MessengerApp : Application(), Application.ActivityLifecycleCallbacks {
    private val worker = Executors.newSingleThreadExecutor()
    private val readers = Executors.newFixedThreadPool(2)
    private val main = Handler(Looper.getMainLooper())
    @Volatile private var rpc: Rpc? = null
    private var started = 0
    var onEvent: ((JSONObject) -> Unit)? = null
    var loggedIn = false
    var needsLogin = false
    private val prefs by lazy { getSharedPreferences("connection", MODE_PRIVATE) }
    val serverMode get() = prefs.getString("server", "").orEmpty().isNotEmpty()
    val serverAddress get() = prefs.getString("server", "").orEmpty()
    private val suspendEngine = Runnable { worker.execute { synchronized(this) { rpc?.close(); rpc = null } } }

    override fun onCreate() { super.onCreate(); registerActivityLifecycleCallbacks(this) }
    @Synchronized private fun engine(): Rpc = rpc ?: (if (serverMode) ServerRpc(serverAddress, prefs.getString("token", "").orEmpty())
        else LocalRpc(File(noBackupFilesDir, "messenger").absolutePath) { event -> main.post { onEvent?.invoke(event) } }).also { rpc = it }

    fun call(method: String, params: JSONObject = JSONObject(), done: (Any?, Exception?) -> Unit) {
        val executor = if (method in setOf("hello", "status", "conversations.list", "messages.list")) readers else worker
        executor.execute {
            try {
                val value = engine().call(method, params)
                main.post {
                    if (method == "status" || method == "connect") {
                        loggedIn = (value as? JSONObject)?.optBoolean("sessionImported") == true
                        if (loggedIn && method == "connect") needsLogin = false
                    }
                    if (method == "session.import") { loggedIn = true; needsLogin = false }
                    done(value, null)
                }
            } catch (e: Exception) { main.post { if (e is RpcFailure && e.code == "AUTH_REQUIRED") needsLogin = true; done(null, e) } }
        }
    }

    fun configure(server: String, token: String, done: (Exception?) -> Unit) {
        worker.execute {
            try {
                if (server.isNotBlank()) ServerRpc(server.trim(), token.trim()) // Validate before replacing working settings.
                synchronized(this) {
                    rpc?.close(); rpc = null
                    prefs.edit().putString("server", server.trim()).putString("token", token.trim()).commit()
                }
                main.post { loggedIn = false; needsLogin = false; done(null) }
            } catch (e: Exception) { main.post { done(e) } }
        }
    }

    override fun onActivityStarted(activity: Activity) { started++; main.removeCallbacks(suspendEngine) }
    override fun onActivityStopped(activity: Activity) { started--; if (started == 0) main.postDelayed(suspendEngine, 2000) }
    override fun onActivityCreated(activity: Activity, state: Bundle?) {}
    override fun onActivityResumed(activity: Activity) {}
    override fun onActivityPaused(activity: Activity) {}
    override fun onActivitySaveInstanceState(activity: Activity, state: Bundle) {}
    override fun onActivityDestroyed(activity: Activity) {}
}
