import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { MessengerClient } from '../dist/index.js';

async function profile(t) {
  const dir = await mkdtemp(path.join(os.tmpdir(), 'messenger-test-'));
  t.after(() => rm(dir, { recursive: true, force: true }));
  return dir;
}

test('TypeScript API talks to the real Go engine and preserves IDs/session across restart', async t => {
  const dataDir = await profile(t);
  let client = await MessengerClient.open({ dataDir });
  try {
    assert.equal((await client.status()).sessionImported, false);
    await client.importSession({ c_user: '9007199254740993', xs: 'fake-session', datr: 'fake-device' });
    const [status, threads, messages] = await Promise.all([client.status(), client.conversations(), client.messages('9007199254740993')]);
    assert.equal(status.sessionImported, true);
    assert.deepEqual(threads, []); assert.deepEqual(messages, []);
    const disk = JSON.parse(await readFile(path.join(dataDir, 'session.json'), 'utf8'));
    assert.equal(disk.cookies.c_user, '9007199254740993');
    await assert.rejects(client.importSession({ c_user: '2', xs: 'x', datr: 'd' }), { code: 'ACCOUNT_MISMATCH' });
  } finally { await client.close(); }
  client = await MessengerClient.open({ dataDir });
  try { assert.equal((await client.status()).sessionImported, true); }
  finally { await client.close(); }
});

test('only one engine can open a profile; lock is released on close', async t => {
  const dataDir = await profile(t);
  const first = await MessengerClient.open({ dataDir });
  try { await assert.rejects(MessengerClient.open({ dataDir }), { code: 'PROFILE_LOCKED' }); }
  finally { await first.close(); }
  const reopened = await MessengerClient.open({ dataDir }); await reopened.close();
});

test('engine rejects sends without a known conversation and validates IDs/limits', async t => {
  const client = await MessengerClient.open({ dataDir: await profile(t) });
  try {
    await assert.rejects(client.send('123', 'hello'), { code: 'UNKNOWN_CONVERSATION' });
    await assert.rejects(client.messages('unsafe'), { code: 'INVALID_ID' });
    await assert.rejects(client.messages('123', 501), { code: 'INVALID_PARAMS' });
    await assert.rejects(client.connect(), { code: 'AUTH_REQUIRED' });
  } finally { await client.close(); }
  await assert.rejects(client.status(), { code: 'CLIENT_CLOSED' });
});

test('missing engine fails promptly with actionable error', async t => {
  await assert.rejects(MessengerClient.open({ dataDir: await profile(t), enginePath: '/does-not-exist/messenger-engine' }), { code: 'ENGINE_START_FAILED' });
});

test('hung or invalid engines reject outstanding API calls and shut down', async t => {
  const dir = await profile(t);
  const fake = path.join(dir, 'fake-engine');
  await writeFile(fake, `#!/usr/bin/env node
import('node:readline').then(({ createInterface }) => {
  createInterface({ input: process.stdin }).on('line', line => {
    const request = JSON.parse(line);
    if (request.method === 'hello') console.log(JSON.stringify({ id: request.id, result: { protocolVersion: 1 } }));
    if (request.method === 'conversations.list') console.log('not-json');
  });
});
`, { mode: 0o700 });
  const client = await MessengerClient.open({ dataDir: dir, enginePath: fake, requestTimeoutMs: 1000 });
  try { await assert.rejects(client.status(), { code: 'TIMEOUT' }); }
  finally { await client.close(); }
  const invalid = await MessengerClient.open({ dataDir: dir, enginePath: fake });
  try { await assert.rejects(invalid.conversations(), { code: 'INVALID_PROTOCOL' }); }
  finally { await invalid.close(); }
});
