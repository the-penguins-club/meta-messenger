# Native Android app

Kotlin and Android platform Views, with a Go library embedded through JNI. There
is no Node.js, React Native, Electron, Compose, or Matrix dependency in the app.
Android 8 (API 26) and newer are supported. ARM64 device and x86-64 emulator APKs
are split so each download contains only one native engine.

## Build

Install JDK 17, Go 1.27.3, and Android SDK command-line tools, then:

```sh
sdkmanager 'platforms;android-36' 'build-tools;36.0.0' 'ndk;28.2.13676358'
export ANDROID_HOME="$HOME/Android/Sdk"
# Point JAVA_HOME at your JDK 17 installation if another Java is the default.
npm run build:android-engine
cd android
./gradlew :app:testDebugUnitTest :app:assembleDebug
```

The Gradle wrapper and Go mobile tools are pinned. JNI output is generated into
`app/libs/messenger.aar` and excluded from Git. Debug APKs are under
`app/build/outputs/apk/debug/`; install the `arm64-v8a` APK on a modern phone with
`adb install -r PATH_TO_APK`. Release builds enable R8/resource shrinking and need
your own signing configuration before distribution. No signing keys are tracked.

## Messaging without a browser

- The signed-in app displays plain text messages; no HTML, automatic link
  handling, link previews, Custom Tabs, external browser intents, or feed/reel
  navigation. Long-press opens reactions or Copy text, without Android's web-search
  selection action.
- `LoginActivity` is the sole WebView owner, runs in a separate login process, and
  is not exported. Chromium is not loaded into the native messaging process. It is available
  only when sign-in or verification is needed. It has no address bar, popup
  browser, downloads, file/content access, or JavaScript bridge.
- Top-level navigation is restricted to HTTPS Facebook authentication paths.
  Feed, root/home, watch, reel, and arbitrary URL navigations are blocked, including
  main-frame requests that bypass navigation callbacks. As soon as a complete
  session is detected, the login window stops loading and finishes. Its WebView,
  browser cookies, history, cache, and web storage are cleared on destruction.
- CAPTCHA/2FA pages must work within those rules. Some account-specific login
  routes may need additional narrowly scoped auth paths after device testing.
  There is no general "open Facebook" escape hatch.

## Connection modes

**On this device** is the default. Sessions, the message cache, and encryption
state remain in the app's private, non-backed-up data directory. The app closes
the engine after it goes into the background, and reconnects on return. The first
version has no background notification service or push notifications.

**Use my own server** is optional under Connection. Enter your HTTPS server origin
and bearer token. The same UI calls the self-hosted API and refreshes its cache
every five seconds while visible. The server retains the Facebook session and
encrypted-device keys and decrypts messages there; it is a trusted endpoint.
Closing the phone app does not stop the server. Server credentials are stored in
app-private preferences and excluded from Android backup.

Both modes currently support conversation lists, cached text messages, text
sending, and reactions. Encrypted backup/PIN history restoration and media support
have the same limitations as the CLI. Android unit tests and lint pass, and an
Android 16 emulator smoke test covers JNI startup, cached Bengali conversations,
offline navigation during reconnect, plain non-clickable reel links, the reaction/
copy menu, and rejection of external login activity launches. Real Facebook login,
CAPTCHA/2FA, sending/receiving, and reactions still need validation on a real phone;
successful desktop login does not prove WebView compatibility.

The initial ARM64 debug APK is approximately 25 MiB (the optimized unsigned release
is approximately 24.5 MiB). Most of that is the Go protocol/encryption engine.
Native libraries stay uncompressed and 16 KiB aligned so Android can map them
directly, avoiding a second extracted copy. Emulator memory figures are not a
substitute for measuring performance on a physical low-end phone.
