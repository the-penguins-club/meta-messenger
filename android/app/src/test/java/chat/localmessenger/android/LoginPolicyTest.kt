package chat.localmessenger.android

import org.junit.Assert.*
import org.junit.Test

class LoginPolicyTest {
    @Test fun authenticationRoutesOnly(){
        for(url in listOf("https://www.facebook.com/login/","https://m.facebook.com/login.php","https://www.facebook.com/checkpoint/123","https://www.facebook.com/two_step_verification/authentication/"))assertTrue(url,LoginPolicy.allowsNavigation(url))
        for(url in listOf("https://www.facebook.com/","https://www.facebook.com/reel/123","https://www.facebook.com/watch/","https://www.facebook.com/messages/","https://evilfacebook.com/login/","https://facebook.com.evil.test/login/","http://www.facebook.com/login/","javascript:alert(1)","intent://login/","https://attacker@www.facebook.com/login/","https://www.facebook.com:8443/login/","https://www.facebook.com/login/../reels/"))assertFalse(url,LoginPolicy.allowsNavigation(url))
    }
    @Test fun completeSessionRequired(){
        assertNull(LoginPolicy.authenticatedCookies("datr=d;xs=x"))
        assertNull(LoginPolicy.authenticatedCookies("datr=d;xs=x;c_user=wrong"))
        assertEquals("1",LoginPolicy.authenticatedCookies("datr=d;xs=x=y;c_user=1")?.get("c_user"))
    }
}
