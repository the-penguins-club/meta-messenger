# Optional self-hosted engine

The default CLI and Android app run directly. This service is optional: one
persistent Go engine, one Facebook account/profile, and authenticated clients.
There is no Matrix server. Multiple clients can share the server's device/cache;
only the service opens its profile directory.

## Run locally

```sh
npm run build:service
./bin/messenger-service --data-dir "$HOME/.local/share/local-messenger/server"
```

It listens on `127.0.0.1:8787` and generates a random bearer token in the profile's
`service-token` file with mode 0600. The token is never printed in service logs.
Use a separate profile from an already running local CLI. Server cache and crypto
state survive process restart. An imported session reconnects on service startup.

Connect the CLI using the token file:

```sh
node dist/cli.js --server http://127.0.0.1:8787 \
  --token-file "$HOME/.local/share/local-messenger/server/service-token" \
  login --cookies /path/to/facebook.cookies.json
node dist/cli.js --server http://127.0.0.1:8787 \
  --token-file "$HOME/.local/share/local-messenger/server/service-token"
```

Authentication may instead be performed from Android's login window after
configuring the server. The credentials are then stored on your server.

For remote access, put the loopback listener behind an HTTPS reverse proxy (for
example Caddy), forwarding both `/v1/rpc` and `/v1/events`. Keep SSE proxy buffering
disabled. Both clients refuse plain HTTP for remote hosts. The daemon's HTTP
listener itself does not terminate TLS; do not expose it directly to the Internet.

```caddyfile
messenger.example.com {
    reverse_proxy 127.0.0.1:8787
}
```

The token grants account-level messaging access. Give it only to your own clients.
The service decrypts messages, so its administrator/storage is part of your trust
boundary. This is a single-account personal service, not a multi-tenant gateway.

## Container

```sh
docker compose up --build -d
# Retrieve the generated token privately from /data/service-token in the container.
```

The Compose definition exposes only loopback, runs as UID 10001, drops capabilities,
and stores the profile in a named volume. The build context excludes captures,
cookies, Node dependencies, and all files outside the Go engine. Keep the volume
when replacing containers. The container recipe is provided separately from native
build validation; it requires Docker to build/run.

## TypeScript

```ts
import { RemoteMessengerClient } from '@local-messenger/client/remote';

const client = await RemoteMessengerClient.open({
  url: 'https://messenger.example.com',
  token: process.env.MESSENGER_SERVICE_TOKEN!,
});
const unsubscribe = client.onEvent(event => console.log(event));
try {
  await client.connect();
  const conversations = await client.conversations();
} finally {
  unsubscribe();
  await client.close(); // Disconnects this client, leaves the service running.
}
```

The remote client implements the same `MessengerApi` interface as the local
subprocess client. Sends are not automatically retried after uncertain failures.

## Protocol

- `POST /v1/rpc`: JSON `{ "id": "1", "method": "status", "params": {} }`.
  Response is `{ "id": "1", "result": ... }` or `{ "id": "1", "error": { "code": ..., "message": ... } }`.
- `GET /v1/events`: server-sent events, one JSON object per `data:` frame. Each new
  stream begins with `resync`, telling clients to refresh the authoritative cache.
- Both endpoints require `Authorization: Bearer TOKEN`. Tokens in query strings
  and cookie authentication are unsupported. There is no permissive CORS policy.
- Requests are limited to 1 MiB. Slow event clients are disconnected instead of
  blocking the engine; reconnect and refresh to recover. Events are not a durable
  message log. The existing bounded cache/history limitations still apply.

Live notifications while Android is closed are not implemented. A persistent
server connection alone does not provide phone push notifications.
