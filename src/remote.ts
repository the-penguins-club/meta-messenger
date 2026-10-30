import { EventEmitter } from 'node:events';
import { MessengerError, type MessengerApi, type MessengerEvent, type Status, type Conversation, type Message, type Reaction } from './index.js';

export interface RemoteOptions { url: string; token: string; requestTimeoutMs?: number }

/** Optional client for a persistent, self-hosted engine. Direct installation stays the default. */
export class RemoteMessengerClient implements MessengerApi {
  private readonly base: URL;
  private readonly events = new EventEmitter();
  private readonly lifetime = new AbortController();
  private sequence = 0;
  private stream?: Promise<void>;

  private constructor(private readonly options: RemoteOptions) {
    this.base = new URL(options.url);
    const loopback = ['localhost', '127.0.0.1', '[::1]'].includes(this.base.hostname);
    if ((this.base.protocol !== 'https:' && !(loopback && this.base.protocol === 'http:')) || this.base.username || this.base.password || this.base.search || this.base.hash || this.base.pathname !== '/') {
      throw new MessengerError('INVALID_SERVER', 'Use an HTTPS server origin, or HTTP on loopback for local development.');
    }
    if (options.token.length < 32 || /[\r\n]/.test(options.token)) throw new MessengerError('INVALID_TOKEN', 'Use the token from the service-token file.');
  }

  static async open(options: RemoteOptions): Promise<RemoteMessengerClient> {
    const client = new RemoteMessengerClient(options);
    const hello = await client.request<{ protocolVersion: number }>('hello');
    if (hello.protocolVersion !== 1) throw new MessengerError('PROTOCOL_MISMATCH', 'Client and service versions do not match.');
    client.stream = client.listen();
    return client;
  }
  onEvent(listener: (event: MessengerEvent) => void): () => void { this.events.on('update', listener); return () => this.events.off('update', listener); }
  importSession(cookies: Record<string, string>): Promise<{ imported: boolean }> { return this.request('session.import', { cookies }); }
  status(): Promise<Status> { return this.request('status'); }
  connect(): Promise<Status> { return this.request('connect'); }
  conversations(): Promise<Conversation[]> { return this.request('conversations.list'); }
  messages(conversationId: string, limit = 50): Promise<Message[]> { return this.request('messages.list', { conversationId, limit }); }
  send(conversationId: string, text: string): Promise<Message> { return this.request('messages.send', { conversationId, text }); }
  react(conversationId: string, messageId: string, emoji: string): Promise<Reaction> { return this.request('messages.react', { conversationId, messageId, emoji }); }

  private async request<T>(method: string, params: unknown = {}): Promise<T> {
    if (this.lifetime.signal.aborted) throw new MessengerError('CLIENT_CLOSED', 'This client is closed.');
    const controller = new AbortController();
    const cancel = () => controller.abort();
    this.lifetime.signal.addEventListener('abort', cancel, { once: true });
    const timer = setTimeout(cancel, this.options.requestTimeoutMs ?? 65_000);
    try {
      const response = await fetch(new URL('/v1/rpc', this.base), { method: 'POST', redirect: 'error', signal: controller.signal,
        headers: { Authorization: `Bearer ${this.options.token}`, 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: String(++this.sequence), method, params }) });
      if (response.status === 401) throw new MessengerError('SERVICE_AUTH_REQUIRED', 'The service token was not accepted.');
      if (!response.ok) throw new MessengerError('SERVICE_ERROR', `The service returned HTTP ${response.status}.`);
      const reply = await response.json() as { result: T; error?: { code: string; message: string } };
      if (reply.error) throw new MessengerError(reply.error.code, reply.error.message);
      return reply.result;
    } catch (error) {
      if (error instanceof MessengerError) throw error;
      if (method === 'messages.send' || method === 'messages.react') throw new MessengerError('OPERATION_UNCONFIRMED', 'Operation acceptance is unknown. Check the conversation before retrying.');
      throw new MessengerError('SERVICE_UNAVAILABLE', 'Cannot reach the Messenger service.');
    } finally { clearTimeout(timer); this.lifetime.signal.removeEventListener('abort', cancel); }
  }

  private async listen(): Promise<void> {
    while (!this.lifetime.signal.aborted) {
      try {
        const response = await fetch(new URL('/v1/events', this.base), { headers: { Authorization: `Bearer ${this.options.token}` }, redirect: 'error', signal: this.lifetime.signal });
        if (!response.ok || !response.body) throw new Error('Event stream unavailable.');
        const reader = response.body.getReader(), decoder = new TextDecoder();
        let buffer = '';
        try {
          while (!this.lifetime.signal.aborted) {
            const { value, done } = await reader.read(); if (done) break;
            buffer += decoder.decode(value, { stream: true });
            if (buffer.length > 1024 * 1024) throw new Error('Event exceeded size limit.');
            const frames = buffer.split(/\r?\n\r?\n/); buffer = frames.pop() ?? '';
            for (const frame of frames) {
              const data = frame.split(/\r?\n/).filter(line => line.startsWith('data:')).map(line => line.slice(5).trimStart()).join('\n');
              if (data) this.events.emit('update', JSON.parse(data));
            }
          }
        } finally { await reader.cancel().catch(() => {}); }
      } catch { /* The next stream starts with resync; clients refresh their cache. */ }
      if (!this.lifetime.signal.aborted) await new Promise<void>(resolve => {
        const signal = this.lifetime.signal;
        const finish = () => { clearTimeout(timer); signal.removeEventListener('abort', finish); resolve(); };
        const timer = setTimeout(finish, 1500); signal.addEventListener('abort', finish, { once: true });
      });
    }
  }
  async close(): Promise<void> { this.lifetime.abort(); await this.stream; }
}
