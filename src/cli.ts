#!/usr/bin/env node
import { readFile } from 'node:fs/promises';
import { readFileSync } from 'node:fs';
import { parseArgs } from 'node:util';
import { MessengerClient, MessengerError, type MessengerEvent } from './index.js';
import { parseCookies } from './cookies.js';
import { safeText } from './terminal.js';
import { runTui } from './tui.js';

const help = `messenger — local Messenger CLI

Usage:
  messenger                           Open the Vim-style terminal interface
  messenger login --cookies FILE      Import Facebook cookies locally (FILE=- for stdin)
  messenger status                    Show local session and cache status
  messenger conversations             Connect and list recent conversations
  messenger messages THREAD           Show cached messages after connecting
  messenger send THREAD "message"      Send text (quote it as one argument)
  messenger react THREAD MESSAGE "👍"   Add/change a reaction
  messenger unreact THREAD MESSAGE    Remove your reaction
  messenger watch                     Stream incoming updates as NDJSON
  messenger tui                       Open the terminal interface

Options:
  --data-dir DIR    Separate local profile directory
  --offline         Read the cache without contacting Facebook
  --json            Machine-readable output
  --limit N         Messages to show (1–500; default 50)
  --help            Show this help

Vim keys: j/k move · h/l panes · gg/G first/last · / search threads
          Enter open · i compose · Esc normal · r react · R unreact
          :q quit · :refresh reload cache · Ctrl-C quit

Login uses a browser session; complete CAPTCHA/2FA in Facebook itself.
Encrypted history from before device registration is not restored yet.
`;

async function main(): Promise<void> {
  const { values, positionals } = parseArgs({ allowPositionals: true, options: {
    'data-dir': { type: 'string' }, cookies: { type: 'string' },
    json: { type: 'boolean', default: false }, offline: { type: 'boolean', default: false },
    limit: { type: 'string', default: '50' }, help: { type: 'boolean', short: 'h' },
  } });
  if (values.help) { process.stdout.write(help); return; }
  const [command = 'tui', ...args] = positionals;
  const counts: Record<string, number> = { tui: 0, login: 0, status: 0, conversations: 0, messages: 1, send: 2, react: 3, unreact: 2, watch: 0 };
  if (!(command in counts) || args.length !== counts[command]) throw new Error('Invalid command or arguments. Run messenger --help.');
  if (values.cookies && command !== 'login') throw new Error('--cookies is only valid with login.');
  if (command === 'login' && !values.cookies) throw new Error('Use messenger login --cookies FILE.');
  if (values.offline && ['send', 'react', 'unreact', 'watch'].includes(command)) throw new Error(`${command} requires a connection; remove --offline.`);
  const limit = Number(values.limit);
  if (!Number.isInteger(limit) || limit < 1 || limit > 500) throw new Error('--limit must be an integer between 1 and 500.');
  if (command === 'tui' && (!process.stdin.isTTY || !process.stdout.isTTY)) throw new Error('The interactive interface requires a terminal. Use conversations/messages --json for scripts.');

  // Parse credentials before spawning an engine or creating a profile.
  const cookies = command === 'login' ? parseCookies(values.cookies === '-' ? readFileSync(0, 'utf8') : await readFile(values.cookies!, 'utf8')) : undefined;
  const client = await MessengerClient.open({ dataDir: values['data-dir'] });
  const shutdown = () => { void client.close(); };
  process.once('SIGTERM', shutdown);
  const output = (value: unknown): void => {
    if (values.json) process.stdout.write(JSON.stringify(value) + '\n');
    else if (Array.isArray(value)) {
      for (const item of value) {
        if ('senderId' in item) process.stdout.write(`${safeText(item.id)}  ${safeText(item.senderId)}  ${safeText(item.text)}\n`);
        else process.stdout.write(`${safeText(item.id)}  ${item.encrypted ? '[E2EE] ' : ''}${safeText(item.name || item.id)}  ${safeText(item.snippet)}\n`);
      }
      if (!value.length) process.stdout.write('No cached items yet.\n');
    } else process.stdout.write(safeText(JSON.stringify(value)) + '\n');
  };
  try {
    if (command === 'login') {
      await client.importSession(cookies!);
      if (values.json) output({ imported: true });
      else process.stdout.write('Session imported locally. Run messenger conversations to connect and register the encrypted device.\n');
      return;
    }
    if (command === 'status') { output(await client.status()); return; }
    if (command === 'tui') { await runTui(client, Boolean(values.offline)); return; }
    const unsubscribe = client.onEvent((event: MessengerEvent) => {
      if (command === 'watch') process.stdout.write(JSON.stringify(event) + '\n');
      else if (event.event === 'authenticationRequired' || event.event === 'engineError') process.stderr.write(safeText(event.data.message) + '\n');
    });
    try {
      if (!values.offline) await client.connect();
      switch (command) {
        case 'conversations': output(await client.conversations()); break;
        case 'messages': output(await client.messages(args[0]!, limit)); break;
        case 'send': output(await client.send(args[0]!, args[1]!)); break;
        case 'react': output(await client.react(args[0]!, args[1]!, args[2]!)); break;
        case 'unreact': output(await client.react(args[0]!, args[1]!, '')); break;
        case 'watch':
          await new Promise<void>(resolve => {
            const stop = () => { process.off('SIGINT', stop); process.off('SIGTERM', stop); resolve(); };
            process.once('SIGINT', stop); process.once('SIGTERM', stop);
          }); break;
      }
    } finally { unsubscribe(); }
  } finally { process.off('SIGTERM', shutdown); await client.close(); }
}

main().catch((error: unknown) => {
  const message = error instanceof MessengerError ? `${error.code}: ${error.message}`
    : error instanceof SyntaxError ? 'Invalid cookie JSON.'
      : error instanceof Error ? error.message : 'Unexpected CLI error.';
  process.stderr.write(safeText(message) + '\n'); process.exitCode = 1;
});
