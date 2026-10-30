import { spawn, type ChildProcessWithoutNullStreams } from 'node:child_process';
import { EventEmitter } from 'node:events';
import { createInterface, type Interface } from 'node:readline';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import os from 'node:os';

export interface Account { id: string; name: string }
export interface Conversation {
  id: string; name: string; encrypted: boolean; updatedAt: number;
  snippet: string; type: number; jid?: string;
}
export interface Message {
  id: string; conversationId: string; senderId: string; text: string;
  timestamp: number; encrypted: boolean; reactions?: Record<string, string>;
}
export interface Status {
  sessionImported: boolean; connected: boolean; encryptedConnected: boolean;
  account: Account | null;
}
export interface Reaction {
  conversationId: string; messageId: string; actorId: string; emoji: string;
  status?: 'submitted';
}
export type MessengerEvent =
  | { event: 'resync'; data: Record<string, never> }
  | { event: 'message'; data: Message }
  | { event: 'reaction'; data: Reaction }
  | { event: 'connection'; data: { transport: 'main' | 'encrypted'; state: string } }
  | { event: 'conversationsChanged'; data: { changed: boolean } }
  | { event: 'authenticationRequired' | 'engineError'; data: { code: string; message: string } };

export class MessengerError extends Error {
  constructor(public readonly code: string, message: string) {
    super(message); this.name = 'MessengerError';
  }
}

export interface ClientOptions {
  dataDir?: string;
  enginePath?: string;
  requestTimeoutMs?: number;
}

export interface MessengerApi {
  onEvent(listener: (event: MessengerEvent) => void): () => void;
  importSession(cookies: Record<string, string>): Promise<{ imported: boolean }>;
  status(): Promise<Status>;
  connect(): Promise<Status>;
  conversations(): Promise<Conversation[]>;
  messages(conversationId: string, limit?: number): Promise<Message[]>;
  send(conversationId: string, text: string): Promise<Message>;
  react(conversationId: string, messageId: string, emoji: string): Promise<Reaction>;
  close(): Promise<void>;
}

export function defaultDataDir(): string {
  const base = process.platform === 'win32'
    ? process.env.LOCALAPPDATA ?? path.join(os.homedir(), 'AppData', 'Local')
    : process.platform === 'darwin'
      ? path.join(os.homedir(), 'Library', 'Application Support')
      : process.env.XDG_DATA_HOME ?? path.join(os.homedir(), '.local', 'share');
  return path.join(base, 'local-messenger', 'default');
}

interface Pending { resolve: (value: unknown) => void; reject: (reason: Error) => void; timer: NodeJS.Timeout }

/** One local engine process per client/profile. No TCP listener or Matrix server. */
export class MessengerClient implements MessengerApi {
  private readonly child: ChildProcessWithoutNullStreams;
  private readonly lines: Interface;
  private readonly events = new EventEmitter();
  private readonly pending = new Map<string, Pending>();
  private sequence = 0;
  private closed = false;
  private terminalError?: Error;
  private readonly exited: Promise<void>;
  private closePromise?: Promise<void>;
  readonly dataDir: string;

  private constructor(private readonly options: ClientOptions) {
    this.dataDir = path.resolve(options.dataDir ?? defaultDataDir());
    const binary = options.enginePath ?? process.env.MESSENGER_ENGINE_PATH
      ?? fileURLToPath(new URL(`../bin/messenger-engine${process.platform === 'win32' ? '.exe' : ''}`, import.meta.url));
    this.child = spawn(binary, ['--data-dir', this.dataDir], { stdio: 'pipe', windowsHide: true });
    this.lines = createInterface({ input: this.child.stdout, crlfDelay: Infinity });
    this.lines.on('line', line => this.receive(line));
    // Do not forward third-party logs: they may contain credentials or plaintext.
    this.child.stderr.resume();
    this.child.stdin.on('error', () => this.fail(new MessengerError('ENGINE_EXITED', 'The local engine stopped accepting requests.')));
    this.child.on('error', () => this.fail(new MessengerError('ENGINE_START_FAILED', 'Cannot start the Go engine. Run npm run build or set MESSENGER_ENGINE_PATH.')));
    this.exited = new Promise(resolve => {
      this.child.once('close', () => {
        this.fail(new MessengerError('ENGINE_EXITED', 'The local engine exited.'));
        this.lines.close(); resolve();
      });
    });
  }

  static async open(options: ClientOptions = {}): Promise<MessengerClient> {
    const client = new MessengerClient(options);
    try {
      const hello = await client.request<{ protocolVersion: number }>('hello');
      if (hello.protocolVersion !== 1) throw new MessengerError('PROTOCOL_MISMATCH', 'Rebuild the engine and TypeScript package together.');
      return client;
    } catch (error) { await client.close(); throw error; }
  }

  onEvent(listener: (event: MessengerEvent) => void): () => void {
    this.events.on('update', listener);
    return () => this.events.off('update', listener);
  }

  importSession(cookies: Record<string, string>): Promise<{ imported: boolean }> { return this.request('session.import', { cookies }); }
  status(): Promise<Status> { return this.request('status'); }
  connect(): Promise<Status> { return this.request('connect'); }
  conversations(): Promise<Conversation[]> { return this.request('conversations.list'); }
  messages(conversationId: string, limit = 50): Promise<Message[]> { return this.request('messages.list', { conversationId, limit }); }
  send(conversationId: string, text: string): Promise<Message> { return this.request('messages.send', { conversationId, text }); }
  react(conversationId: string, messageId: string, emoji: string): Promise<Reaction> { return this.request('messages.react', { conversationId, messageId, emoji }); }

  private request<T>(method: string, params: unknown = {}): Promise<T> {
    if (this.terminalError || this.closed) return Promise.reject(this.terminalError ?? new MessengerError('CLIENT_CLOSED', 'This client is closed.'));
    const id = String(++this.sequence);
    return new Promise<T>((resolve, reject) => {
      const timer = setTimeout(() => {
        const uncertain = method === 'messages.send' || method === 'messages.react';
        const error = new MessengerError(uncertain ? 'OPERATION_UNCONFIRMED' : 'TIMEOUT', uncertain
          ? 'Operation timed out; acceptance is unknown. Check the conversation before retrying.'
          : 'The local engine timed out. Restart the client and check connectivity.');
        this.fail(error); this.child.kill();
      }, this.options.requestTimeoutMs ?? 65_000);
      this.pending.set(id, { resolve: value => resolve(value as T), reject, timer });
      this.child.stdin.write(JSON.stringify({ id, method, params }) + '\n', error => {
        if (error) this.fail(new MessengerError('ENGINE_EXITED', 'Cannot write to the local engine.'));
      });
    });
  }

  private receive(line: string): void {
    let data: { id?: string; event?: string; data?: unknown; result?: unknown; error?: { code: string; message: string } };
    try { data = JSON.parse(line); }
    catch { this.fail(new MessengerError('INVALID_PROTOCOL', 'The engine returned invalid JSON.')); this.child.kill(); return; }
    if (data.event === 'fatal') {
      const error = data.data as { code: string; message: string };
      this.fail(new MessengerError(error.code, error.message)); return;
    }
    if (data.event) { this.events.emit('update', data); return; }
    if (!data.id) return;
    const pending = this.pending.get(data.id);
    if (!pending) return;
    this.pending.delete(data.id); clearTimeout(pending.timer);
    if (data.error) pending.reject(new MessengerError(data.error.code, data.error.message));
    else pending.resolve(data.result);
  }

  private fail(error: Error): void {
    this.terminalError ??= error;
    for (const pending of this.pending.values()) { clearTimeout(pending.timer); pending.reject(this.terminalError); }
    this.pending.clear();
  }

  close(): Promise<void> {
    if (this.closePromise) return this.closePromise;
    this.closed = true;
    this.fail(new MessengerError('CLIENT_CLOSED', 'The client was closed.'));
    this.closePromise = (async () => {
      this.child.stdin.end();
      const terminate = setTimeout(() => this.child.kill('SIGTERM'), 6_000);
      const force = setTimeout(() => this.child.kill('SIGKILL'), 9_000);
      try { await this.exited; } finally { clearTimeout(terminate); clearTimeout(force); }
    })();
    return this.closePromise;
  }
}
