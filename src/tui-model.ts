import type { Conversation, Message } from './index.js';

export type Mode = 'normal' | 'compose' | 'search' | 'command' | 'reaction';
export type Effect =
  | { type: 'quit' | 'refresh' }
  | { type: 'open'; conversationId: string }
  | { type: 'send'; conversationId: string; text: string }
  | { type: 'react'; conversationId: string; messageId: string; emoji: string };

/** Input/navigation state is independent of the terminal and Messenger transport. */
export class TuiModel {
  mode: Mode = 'normal';
  focus: 'conversations' | 'messages' = 'conversations';
  conversations: Conversation[] = [];
  messages: Message[] = [];
  conversationIndex = 0;
  messageIndex = 0;
  activeConversationId?: string;
  query = '';
  input = '';
  notice = '';
  busy = false;
  private pendingG = false;
  private inputTarget?: { conversationId: string; messageId?: string };

  get filtered(): Conversation[] {
    const query = this.query.toLocaleLowerCase();
    return this.conversations.filter(c => `${c.name} ${c.id}`.toLocaleLowerCase().includes(query));
  }
  get selectedConversation(): Conversation | undefined { return this.filtered[this.conversationIndex]; }
  get selectedMessage(): Message | undefined { return this.messages[this.messageIndex]; }

  replaceConversations(items: Conversation[]): void {
    const selected = this.selectedConversation?.id;
    this.conversations = items;
    const index = this.filtered.findIndex(c => c.id === selected);
    this.conversationIndex = Math.max(0, index);
  }

  replaceMessages(items: Message[], conversationId: string): void {
    const same = this.activeConversationId === conversationId;
    const selected = this.selectedMessage?.id;
    const atEnd = this.messageIndex >= this.messages.length - 1;
    this.activeConversationId = conversationId;
    this.messages = items;
    const index = same && !atEnd ? items.findIndex(m => m.id === selected) : items.length - 1;
    this.messageIndex = Math.max(0, index);
  }

  key(text: string, name?: string, ctrl = false, pageSize = 10): Effect | undefined {
    if (ctrl && name === 'c') return { type: 'quit' };
    if (name === 'escape') {
      if (this.mode === 'search') { this.query = ''; this.conversationIndex = 0; }
      this.mode = 'normal'; this.input = ''; this.inputTarget = undefined; this.pendingG = false; return;
    }
    if (this.mode !== 'normal') {
      if (name === 'backspace') { this.input = [...this.input].slice(0, -1).join(''); }
      else if (name === 'return') {
        const input = this.input;
        const mode = this.mode;
        const target = this.inputTarget;
        if (this.busy && (mode === 'compose' || mode === 'reaction')) return;
        this.mode = 'normal'; this.input = ''; this.inputTarget = undefined;
        if (mode === 'search') { this.query = input; this.conversationIndex = 0; return; }
        if (mode === 'command') {
          if (input === 'q' || input === 'quit') return { type: 'quit' };
          if (input === 'refresh') return { type: 'refresh' };
          this.notice = input === 'help' ? 'j/k move · h/l panes · i compose · r react · R remove · / search · :q quit' : `Unknown command: ${input}`;
          return;
        }
        if (mode === 'compose' && input.trim() && target) return { type: 'send', conversationId: target.conversationId, text: input };
        if (mode === 'reaction' && input.trim() && target?.messageId) {
          return { type: 'react', conversationId: target.conversationId, messageId: target.messageId, emoji: input.trim() };
        }
      } else if (!ctrl && text) {
        this.input += text.replace(/[\x00-\x1f\x7f-\x9f]/g, '');
      }
      return;
    }
    if (this.busy) return;
    let index = this.focus === 'conversations' ? this.conversationIndex : this.messageIndex;
    const count = this.focus === 'conversations' ? this.filtered.length : this.messages.length;
    if (text === 'g') {
      if (this.pendingG) { index = 0; this.pendingG = false; } else { this.pendingG = true; return; }
    } else {
      this.pendingG = false;
      if (text === 'j' || name === 'down') index++;
      else if (text === 'k' || name === 'up') index--;
      else if (text === 'G') index = count - 1;
      else if (ctrl && name === 'd') index += pageSize;
      else if (ctrl && name === 'u') index -= pageSize;
      else if (text === 'h' || name === 'left') this.focus = 'conversations';
      else if (text === 'l' || name === 'right') {
        this.focus = 'messages';
        if (this.selectedConversation && this.activeConversationId !== this.selectedConversation.id) return { type: 'open', conversationId: this.selectedConversation.id };
      } else if (name === 'return' && this.focus === 'conversations' && this.selectedConversation) {
        this.focus = 'messages'; return { type: 'open', conversationId: this.selectedConversation.id };
      } else if (text === '/' || text === ':' || text === 'i' || text === 'r') {
        if ((text === 'i' || text === 'r') && !this.activeConversationId) { this.notice = 'Open a conversation first.'; return; }
        if ((text === 'i' || text === 'r') && this.focus === 'conversations' && this.selectedConversation?.id !== this.activeConversationId) {
          this.notice = 'Open the selected conversation with Enter first.'; return;
        }
        if (text === 'r' && !this.selectedMessage) { this.notice = 'Select a message first.'; return; }
        this.inputTarget = this.activeConversationId ? { conversationId: this.activeConversationId, messageId: this.selectedMessage?.id } : undefined;
        this.mode = text === '/' ? 'search' : text === ':' ? 'command' : text === 'i' ? 'compose' : 'reaction';
        this.input = ''; return;
      } else if (text === 'R' && this.selectedMessage && this.activeConversationId) {
        if (this.focus === 'conversations' && this.selectedConversation?.id !== this.activeConversationId) { this.notice = 'Open the selected conversation with Enter first.'; return; }
        return { type: 'react', conversationId: this.activeConversationId, messageId: this.selectedMessage.id, emoji: '' };
      } else if (text === 'q') return { type: 'quit' };
    }
    index = Math.max(0, Math.min(Math.max(0, count - 1), index));
    // Do not assign an index from the old pane after changing focus.
    if (text === 'h' || text === 'l' || name === 'left' || name === 'right') return;
    if (this.focus === 'conversations') this.conversationIndex = index; else this.messageIndex = index;
    return;
  }
}
