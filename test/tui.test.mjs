import test from 'node:test';
import assert from 'node:assert/strict';
import { TuiModel } from '../dist/tui-model.js';
import { safeText, clip } from '../dist/terminal.js';
import { parseCookies } from '../dist/cookies.js';
import stringWidth from 'string-width';
import { paneRow } from '../dist/terminal.js';

function model() {
  const state = new TuiModel();
  state.replaceConversations(['Alice', 'Bob', 'Carol'].map((name, i) => ({ id: String(i + 1), name })));
  state.replaceMessages([{ id: 'a', text: 'first' }, { id: 'b', text: 'second' }], '1');
  return state;
}

test('Vim navigation, pane changes, jumps, search, and command mode', () => {
  const state = model();
  state.key('j'); assert.equal(state.selectedConversation.name, 'Bob');
  state.key('G'); assert.equal(state.selectedConversation.name, 'Carol');
  state.key('g'); state.key('g'); assert.equal(state.selectedConversation.name, 'Alice');
  state.key('k'); assert.equal(state.conversationIndex, 0);
  state.key('/'); state.key('Bob'); state.key('', 'return');
  assert.equal(state.filtered.length, 1);
  assert.deepEqual(state.key('', 'return'), { type: 'open', conversationId: '2' });
  assert.equal(state.focus, 'messages');
  state.key('h'); assert.equal(state.focus, 'conversations');
  state.key(':'); state.key('q'); assert.deepEqual(state.key('', 'return'), { type: 'quit' });
});

test('typing is local until Enter, Esc cancels, reactions require explicit submission', () => {
  const state = model();
  state.key('i'); assert.equal(state.mode, 'compose');
  assert.equal(state.key('hello'), undefined);
  state.key('', 'escape'); assert.equal(state.mode, 'normal'); assert.equal(state.input, '');
  state.key('i'); state.key('hello');
  assert.deepEqual(state.key('', 'return'), { type: 'send', conversationId: '1', text: 'hello' });
  state.key('r'); state.key('👍');
  assert.deepEqual(state.key('', 'return'), { type: 'react', conversationId: '1', messageId: 'b', emoji: '👍' });
  assert.deepEqual(state.key('R'), { type: 'react', conversationId: '1', messageId: 'b', emoji: '' });
});

test('selected thread remains stable when updates reorder conversations', () => {
  const state = model(); state.key('j');
  state.replaceConversations([...state.conversations].reverse());
  assert.equal(state.selectedConversation.name, 'Bob');
  state.busy = true; assert.equal(state.key('i'), undefined); assert.equal(state.mode, 'normal');
  assert.deepEqual(state.key('', 'c', true), { type: 'quit' });
});

test('updates cannot redirect an in-progress reaction or compose to another target', () => {
  const state = model();
  state.key('r'); state.key('👍');
  state.replaceMessages([...state.messages, { id: 'c', text: 'new arrival' }], '1');
  assert.deepEqual(state.key('', 'return'), { type: 'react', conversationId: '1', messageId: 'b', emoji: '👍' });
  state.key('j'); // Bob is highlighted, but Alice remains open.
  state.key('i');
  assert.equal(state.mode, 'normal');
  assert.match(state.notice, /Open the selected conversation/);
});

test('remote text cannot inject terminal controls', () => {
  assert.equal(safeText('\x1b[2Jhello\x1b]8;;https://example.com\x07click\x1b]8;;\x07\r\n'), 'helloclick  ');
  assert.equal(clip('abcdef', 3), 'abc');
  assert.equal(clip('👍', 1), ' ');
});

test('Unicode names use display cells and never split combining sequences or emoji', () => {
  assert.equal(clip('é', 4), 'é   ');
  assert.equal(clip('e\u0301x', 1), 'e\u0301');
  assert.equal(clip('张三', 5), '张三 ');
  assert.equal(clip('👨‍👩‍👧‍👦x', 2), '👨‍👩‍👧‍👦');
  assert.equal(clip('👍🏽x', 2), '👍🏽');
  assert.equal(clip('🇧🇩x', 2), '🇧🇩');
  for (const name of ['Naresh Yadav', 'অরিতুল অঙ্কন বণিক', 'মোঃ সজিব হোসেন', 'সুজল শাই', '张三', 'Mission 👨‍👩‍👧‍👦']) {
    const clipped = clip(name, 36);
    assert.equal(stringWidth(clipped), 36, name);
    assert.ok(clipped.startsWith(name), `name unexpectedly truncated: ${name}`);
  }
});

test('pane divider and message columns stay fixed for every script and selection state', () => {
  for (const name of ['Alice', 'মোঃ সজিব হোসেন', '张三', '👩🏽‍💻']) {
    for (const selected of [true, false]) {
      const row = paneRow(name, 'message', 36, 40, selected, !selected);
      assert.ok(row.startsWith('\x1b[2K'), 'old row must be cleared before repainting');
      assert.ok(row.includes('\x1b[38G│\x1b[40G'), name);
      assert.equal(row.match(/│/g)?.length, 1);
    }
  }
});

test('cookie imports accept JSON exports and headers without evaluating shell input', () => {
  const flat = parseCookies('{"c_user":"9007199254740993","xs":"x:y=z","datr":"d"}');
  assert.equal(flat.c_user, '9007199254740993');
  assert.equal(parseCookies('Cookie: c_user=123; xs=x=y; datr=d').xs, 'x=y');
  const exported = [{ name: 'xs', value: 'x', domain: '.facebook.com' }, { name: 'c_user', value: '1', domain: '.facebook.com' }, { name: 'datr', value: 'd', domain: '.facebook.com' }, { name: 'xs', value: 'other-site', domain: 'evilfacebook.com' }];
  assert.equal(parseCookies(JSON.stringify(exported)).xs, 'x');
  assert.throws(() => parseCookies('curl $(touch /tmp/not-executed)'));
});
