import { emitKeypressEvents, type Key } from 'node:readline';
import { type MessengerApi, type MessengerEvent, type Status } from './index.js';
import { TuiModel, type Effect } from './tui-model.js';
import { clip, paneRow, safeText } from './terminal.js';

export async function runTui(client: MessengerApi, offline = false): Promise<void> {
  const model = new TuiModel();
  const input = process.stdin, output = process.stdout;
  let stopped = false;
  let status: Status = await client.status();
  let refreshTimer: NodeJS.Timeout | undefined;
  let refreshRunning = false, refreshAgain = false;
  let stop!: () => void;
  const done = new Promise<void>(resolve => { stop = () => { stopped = true; resolve(); }; });

  function render(): void {
    if (stopped) return;
    const width = Math.max(1, (output.columns ?? 80) - 1);
    const height = Math.max(1, output.rows ?? 24);
    if (width < 55 || height < 12) { output.write('\x1b[H\x1b[2J' + clip('Resize terminal to at least 55 columns × 12 rows. Ctrl-C quits.', width)); return; }
    const left = Math.min(36, Math.floor(width * 0.32)), right = width - left - 3;
    const rows = height - 8;
    const active = model.conversations.find(c => c.id === model.activeConversationId);
    const connection = offline ? 'OFFLINE CACHE' : `${status.connected ? 'connected' : 'connecting'} · E2EE ${status.encryptedConnected ? 'connected' : 'pending'}`;
    const lines: string[] = [clip(`Messenger  ${status.account?.name ?? ''}  ${connection}`, width),
      paneRow(model.focus === 'conversations' ? '> Conversations' : '  Conversations',
        `${model.focus === 'messages' ? '> ' : ''}${active?.name || active?.id || 'Messages'}`, left, right)];
    const threads = model.filtered;
    const startThread = Math.max(0, model.conversationIndex - rows + 1);
    const startMessage = Math.max(0, model.messageIndex - rows + 1);
    for (let row = 0; row < rows; row++) {
      const ti = startThread + row, mi = startMessage + row;
      const thread = threads[ti], message = model.messages[mi];
      const threadText = thread ? `${thread.encrypted ? '* ' : '  '}${thread.name || thread.id}` : '';
      const sender = message?.senderId === status.account?.id ? 'you' : message?.senderId;
      const messageText = message ? `${new Date(message.timestamp).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })} ${sender}: ${message.text}${message.reactions ? ' ' + Object.values(message.reactions).join(' ') : ''}` : '';
      lines.push(paneRow(threadText, messageText, left, right,
        !!thread && ti === model.conversationIndex && model.focus === 'conversations',
        !!message && mi === model.messageIndex && model.focus === 'messages'));
    }
    const selected = model.selectedMessage;
    lines.push(clip(selected ? `Message ${selected.id} · ${selected.encrypted ? 'encrypted' : 'unencrypted'}` : 'Messages show the local cache. * marks encrypted conversations.', width));
    lines.push(clip(selected?.text ?? 'Open a conversation with Enter or l.', width));
    lines.push(clip(model.notice, width));
    lines.push(clip(`${model.mode.toUpperCase()}${model.busy ? ' · working…' : ''}  j/k move · h/l panes · i compose · r react · / search · :q quit`, width));
    const prefix = model.mode === 'compose' ? 'Send> ' : model.mode === 'reaction' ? 'React> ' : model.mode === 'search' ? '/' : model.mode === 'command' ? ':' : '';
    lines.push(clip(prefix ? `${prefix}${model.input}` : 'Enter sends in compose mode. Esc cancels. Ctrl-U/D scroll. :help shows keys.', width));
    // Write complete rows without filling the terminal's final column (autowrap).
    output.write('\x1b[H' + lines.map(line => line + '\x1b[K').join('\r\n') + '\x1b[J');
  }

  async function refresh(): Promise<void> {
    if (refreshRunning) { refreshAgain = true; return; }
    refreshRunning = true;
    try {
      do {
        refreshAgain = false;
        model.replaceConversations(await client.conversations());
        const id = model.activeConversationId ?? model.selectedConversation?.id;
        if (id) model.replaceMessages(await client.messages(id, 200), id);
        status = await client.status(); render();
      } while (refreshAgain && !stopped);
    } catch (error) { model.notice = safeText(error instanceof Error ? error.message : 'Cannot read local cache.'); render(); }
    finally { refreshRunning = false; }
  }

  async function effect(action: Effect): Promise<void> {
    if (action.type === 'quit') { stop(); return; }
    model.busy = true; render();
    try {
      if (action.type === 'open') model.replaceMessages(await client.messages(action.conversationId, 200), action.conversationId);
      else if (action.type === 'send') {
        if (offline) throw new Error('Sending requires a connection. Restart without --offline.');
        await client.send(action.conversationId, action.text); model.notice = 'Message accepted by Facebook.';
      } else if (action.type === 'react') {
        if (offline) throw new Error('Reactions require a connection. Restart without --offline.');
        await client.react(action.conversationId, action.messageId, action.emoji); model.notice = 'Reaction submitted.';
      }
      await refresh();
    } catch (error) { model.notice = safeText(error instanceof Error ? error.message : 'Operation failed.'); }
    finally { model.busy = false; render(); }
  }

  const onEvent = (event: MessengerEvent): void => {
    if (event.event === 'connection') {
      if (event.data.transport === 'main') status.connected = event.data.state === 'connected';
      else status.encryptedConnected = event.data.state === 'connected';
      render();
    } else if (event.event === 'authenticationRequired' || event.event === 'engineError') {
      model.notice = event.data.message; render();
    } else {
      if (refreshTimer) clearTimeout(refreshTimer);
      refreshTimer = setTimeout(() => { void refresh(); }, 100);
    }
  };
  const unsubscribe = client.onEvent(onEvent);
  const keypress = (text: string | undefined, key: Key): void => {
    const action = model.key(text ?? '', key.name, Boolean(key.ctrl), Math.max(1, Math.floor(((output.rows ?? 24) - 8) / 2)));
    if (action) void effect(action); else render();
  };
  const resize = () => render();
  const wasRaw = Boolean(input.isRaw);
  emitKeypressEvents(input);
  output.write('\x1b[?1049h\x1b[?25l\x1b[2J');
  try {
    input.setRawMode(true); input.resume(); input.on('keypress', keypress);
    output.on('resize', resize); process.once('SIGTERM', stop); process.once('SIGINT', stop);
    model.notice = offline ? 'Reading the local cache.' : 'Connecting to Facebook…';
    render();
    // Keep the terminal responsive while connecting; Ctrl-C/Esc handling stays active.
    void (async () => {
      try { if (!offline) status = await client.connect(); await refresh(); if (!offline) model.notice = 'Connected. Encrypted history restoration is not enabled yet.'; }
      catch (error) { model.notice = safeText(error instanceof Error ? error.message : 'Connection failed.'); await refresh(); }
      render();
    })();
    await done;
  } finally {
    stopped = true;
    if (refreshTimer) clearTimeout(refreshTimer);
    unsubscribe(); input.off('keypress', keypress); output.off('resize', resize);
    process.off('SIGTERM', stop); process.off('SIGINT', stop);
    input.setRawMode(wasRaw); input.pause();
    output.write('\x1b[0m\x1b[?25h\x1b[?1049l');
  }
}
