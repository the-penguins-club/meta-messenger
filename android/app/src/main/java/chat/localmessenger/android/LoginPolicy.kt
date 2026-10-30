package chat.localmessenger.android

import java.net.URI

/** The only web surface is an unexported, temporary authentication screen. */
object LoginPolicy {
    private val hosts = setOf("www.facebook.com", "facebook.com", "m.facebook.com", "mbasic.facebook.com")
    private val authPaths = listOf("/login", "/checkpoint", "/two_step_verification", "/two_factor", "/recover", "/auth", "/privacy/consent", "/dialog", "/confirmemail", "/device", "/secure", "/help/security")

    fun allowsNavigation(url: String): Boolean = try {
        val uri = URI(url)
        val path = uri.path.orEmpty()
        uri.scheme == "https" && uri.host in hosts && uri.userInfo == null && (uri.port == -1 || uri.port == 443) &&
            path.split('/').none { it == "." || it == ".." || it.contains('\\') } &&
            authPaths.any { path == it || path.startsWith("$it/") || path == "$it.php" }
    } catch (_: Exception) { false }

    fun authenticatedCookies(header: String?): Map<String, String>? {
        val cookies = header.orEmpty().split(';').mapNotNull {
            val parts = it.trim().split('=', limit = 2)
            if (parts.size == 2 && parts[0].isNotBlank()) parts[0] to parts[1] else null
        }.toMap()
        return if (listOf("xs", "c_user", "datr").all { !cookies[it].isNullOrBlank() } && cookies["c_user"]!!.matches(Regex("[1-9][0-9]*"))) cookies else null
    }
}
