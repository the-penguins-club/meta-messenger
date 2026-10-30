package chat.localmessenger.android

import chat.localmessenger.engine.mobile.Mobile
import chat.localmessenger.engine.mobile.Listener
import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.URI
import java.util.concurrent.atomic.AtomicLong

class RpcFailure(val code: String, message: String) : Exception(message)

interface Rpc : AutoCloseable {
    fun call(method: String, params: JSONObject = JSONObject()): Any?
}

private val sequence = AtomicLong()
private fun request(method: String, params: JSONObject) = JSONObject().put("id", sequence.incrementAndGet().toString()).put("method", method).put("params", params).toString()
private fun result(raw: String): Any? {
    val response = JSONObject(raw)
    response.optJSONObject("error")?.let { throw RpcFailure(it.getString("code"), it.getString("message")) }
    return response.opt("result").takeUnless { it == JSONObject.NULL }
}

class LocalRpc(dir: String, onEvent: (JSONObject) -> Unit) : Rpc {
    private val client = Mobile.open(dir, object : Listener {
        override fun onEvent(json: String) { onEvent(JSONObject(json)) }
    })
    override fun call(method: String, params: JSONObject): Any? = result(client.call(request(method, params)))
    override fun close() = client.close()
}

class ServerRpc(url: String, private val token: String) : Rpc {
    private val base = URI(url)
    init {
        require(base.scheme == "https" && !base.host.isNullOrBlank() && base.userInfo == null && base.query == null && base.fragment == null && base.path.orEmpty() in listOf("", "/")) { "Use an HTTPS server address without a path or credentials." }
        require(token.length >= 32 && !token.contains('\n') && !token.contains('\r')) { "Enter the token from your server's service-token file." }
    }
    override fun call(method: String, params: JSONObject): Any? {
        val connection = base.resolve("/v1/rpc").toURL().openConnection() as HttpURLConnection
        try {
            connection.requestMethod = "POST"
            connection.instanceFollowRedirects = false
            connection.connectTimeout = 15_000; connection.readTimeout = 70_000
            connection.setRequestProperty("Authorization", "Bearer $token")
            connection.setRequestProperty("Content-Type", "application/json")
            connection.doOutput = true
            connection.outputStream.use { it.write(request(method, params).toByteArray(Charsets.UTF_8)) }
            if (connection.responseCode == 401) throw RpcFailure("SERVICE_AUTH_REQUIRED", "Your server token was not accepted.")
            if (connection.responseCode != 200) throw RpcFailure("SERVICE_ERROR", "Cannot reach the Messenger service (HTTP ${connection.responseCode}).")
            return result(connection.inputStream.bufferedReader().use { it.readText() })
        } finally { connection.disconnect() }
    }
    override fun close() {} // Closing a phone UI must not stop the shared server engine.
}
