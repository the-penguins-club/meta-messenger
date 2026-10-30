# Local Messenger

A directly installed Messenger CLI and TypeScript API, backed by a local Go
engine using [mautrix-meta](https://github.com/mautrix/meta). No Matrix server,
hosted account, or separately managed service is required. The TypeScript
package starts and stops the engine as a subprocess; IPC is newline-delimited
JSON over stdin/stdout. A [native Android app](android/README.md) embeds the same
engine, and an [optional self-hosted service](SERVICE.md) lets multiple clients
share a persistent connection.

This is an initial implementation. Local build, IPC, storage, and terminal behavior
are tested. Browser-session login, conversation listing, encrypted transport
connection, and device reuse after restart have also been validated with a live
Facebook account. Sending, receiving new messages, and reactions still need live
validation.

## Build and run

Development prerequisites: Node.js 18.17+, npm, Go 1.27.3, and a C compiler
(SQLite uses CGO). Dependency versions and the mautrix-meta commit are pinned.

```sh
npm ci
npm run build
node dist/cli.js --help
```

For a local executable command, run `npm link`, then use `messenger`. The package
includes the compiled engine in `bin/`; users of a release built for their OS
would not need Go. Prebuilt release archives are not published yet. Build on the
target platform; do not assume an npm tarball built on Linux runs on macOS/Windows.

## Login

1. Sign into **facebook.com** in your browser. Complete CAPTCHA/2FA there.
2. Export that site's cookies to a local JSON file, or save a request's `Cookie`
   header to a local file. JSON can be a name-to-string object or a browser-export
   array containing `name`, `value`, and optional `domain`.
3. Import the file, then connect:

```sh
messenger login --cookies /path/to/facebook.cookies.json
messenger conversations
messenger
```

Required cookies are `xs`, `c_user`, and `datr`. Session import only validates and
saves the cookie format; it does not contact Facebook. The first online command
validates the session and registers an encrypted Messenger device. The existing
HAR is never consumed automatically and is excluded from source control.

`--cookies -` reads from stdin. Credentials are never command-line argument values,
printed in normal logs, or sent anywhere except Facebook by the Go client.
The app does not collect passwords or integrate a CAPTCHA-solving service.

Profiles default to the OS user-data directory (`~/.local/share/local-messenger/default`
on Linux). Use `--data-dir /path/to/profile` for another profile. Only one process
can open a profile at a time, to protect encrypted-device state. Close the TUI/watch
command before using another CLI process with the same profile.

Cookies, the last 500 cached messages per conversation, and encryption keys are
stored locally. On Unix the profile directory is mode 0700 and credential/cache
files are mode 0600; the cache and key database are **not encrypted at rest**.
Windows permissions inherit the user's directory ACL. Protect and back up the
profile as sensitive account data. Use a separate profile for a different account.

## Vim-style interface

Run `messenger` or `messenger tui`. `messenger tui --offline` opens the cache.

| Key | Action |
| --- | --- |
| `j` / `k` | Move down/up in the focused pane |
| `h` / `l` | Focus conversations/messages |
| `Enter` | Open the selected conversation |
| `gg` / `G` | Jump to the first/last item |
| `Ctrl-U` / `Ctrl-D` | Move half a page |
| `/`, text, `Enter` | Filter conversations by name or ID |
| `i`, text, `Enter` | Compose and send a text message |
| `r`, emoji, `Enter` | React to the selected message |
| `R` | Remove your reaction from the selected message |
| `Esc` | Cancel editing and return to navigation |
| `:refresh`, `Enter` | Refresh the displayed local cache |
| `:q`, `Enter`, or `q` | Quit |
| `Ctrl-C` | Quit from any mode |

The terminal needs at least 55 columns and 12 rows. Text is single-line in the
initial UI; long messages are clipped. The selected message ID and body also
appear below the panes. Remote text is sanitized before terminal rendering.

## Scriptable commands

```sh
messenger status --json
messenger conversations --json
messenger messages THREAD_ID --limit 100 --json
messenger messages THREAD_ID --offline --json
messenger send THREAD_ID "Hello"
messenger react THREAD_ID MESSAGE_ID "👍"
messenger unreact THREAD_ID MESSAGE_ID
messenger watch
```

IDs remain strings throughout the API, including values outside JavaScript's safe
integer range. `watch` streams connection, message, reaction, and authentication
events as NDJSON. Short commands disconnect when finished; `watch` and the TUI keep
the engine connected. Messages lists read the local cache after connecting; they
do not fetch complete historical conversations. Some encrypted chats only become
visible after a new message arrives.

## TypeScript API

```ts
import { MessengerClient } from '@local-messenger/client';

const client = await MessengerClient.open({ dataDir: '/path/to/profile' });
const unsubscribe = client.onEvent(event => console.log(event));
try {
  await client.connect();
  const conversations = await client.conversations();
  // Choose a conversation before calling client.send(id, text) or
  // client.react(id, messageId, emoji). Empty emoji removes your reaction.
} finally {
  unsubscribe();
  await client.close();
}
```

The TypeScript API targets Node.js; Android uses a native Go/JNI binding to the same
engine protocol. `MESSENGER_ENGINE_PATH` or the `enginePath` option can
select a separately built engine binary.

## Current limits and validation

- Implemented: session import, recent conversation metadata, cached messages,
  live encrypted/plaintext text, reactions, local persistence, Vim-style navigation.
- Encrypted backups/PIN-based history restoration, attachments, edits, calls,
  read receipts, notifications, and iOS packaging are not
  implemented yet. Unsupported incoming content appears as a placeholder.
- Network reconnects use the upstream clients. Session expiry, permanent device
  rejection, and some protocol failures require browser login/CLI restart; there
  is no guarantee of gap-free history recovery.
- A send timeout is reported as **unconfirmed**. The app does not blindly resend;
  check the conversation before trying again. Reactions are reported as submitted.
- Account challenges are surfaced as events/errors so the user can resolve them
  in their browser. Private interfaces may change.

```sh
npm test
npm run test:engine
```

Automated tests use synthetic sessions and local engines; they do not contact
Facebook or send messages. A separate live smoke test verified browser-session
login, conversation listing, both realtime transports, and restart with the same
stored encrypted device. Remaining live checks: send/receive encrypted text with
a test contact, add/remove reactions, then interrupt/recover the network.

The source is AGPL-3.0-or-later. See LICENSE and NOTICE for upstream attribution.
