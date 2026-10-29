package engine

import (
	"strconv"
	"time"

	"go.mau.fi/mautrix-meta/pkg/messagix/table"
	"go.mau.fi/whatsmeow/types/events"
)

func idString(n int64) string { return strconv.FormatInt(n, 10) }

func (e *Engine) ingest(tbl *table.LSTable, live bool) error {
	if tbl == nil {
		return nil
	}
	s := e.store
	s.mu.Lock()
	for _, v := range tbl.LSUpdateThreadAuthorityAndMappingWithOTIDFromJID {
		s.cache.Mappings[idString(v.ThreadKey)] = idString(v.ThreadJID)
	}
	for _, v := range tbl.LSVerifyHybridThreadExists {
		s.cache.Mappings[idString(v.ThreadKey)] = idString(v.ThreadJID)
	}
	canonical := func(id int64) string {
		str := idString(id)
		if mapped := s.cache.Mappings[str]; mapped != "" {
			return mapped
		}
		return str
	}
	put := func(id int64, name, snippet string, kind table.ThreadType, ts int64) {
		key := canonical(id)
		c := s.cache.Conversations[key]
		previousType := c.Type
		c.ID = key
		if name != "" {
			c.Name = name
		}
		if snippet != "" {
			c.Snippet = snippet
		}
		if ts > c.UpdatedAt {
			c.UpdatedAt = ts
		}
		if kind != 0 {
			c.Type = int64(kind)
		}
		if c.Encrypted && !kind.IsWhatsApp() {
			c.Type = previousType
		}
		if kind.IsWhatsApp() || key != idString(id) {
			c.Encrypted = true
			if c.Type != 16 {
				c.Type = 15
			}
		}
		s.cache.Conversations[key] = c
	}
	for oldID, newID := range s.cache.Mappings {
		if oldID == newID {
			continue
		}
		if old, ok := s.cache.Conversations[oldID]; ok {
			if _, exists := s.cache.Conversations[newID]; !exists {
				old.ID = newID
				old.Encrypted = true
				if old.Type != 16 {
					old.Type = 15
				}
				s.cache.Conversations[newID] = old
			}
			delete(s.cache.Conversations, oldID)
		}
		for k, m := range s.cache.Messages {
			if m.ConversationID == oldID {
				delete(s.cache.Messages, k)
				m.ConversationID = newID
				s.cache.Messages[messageKey(newID, m.ID)] = m
			}
		}
	}
	for _, v := range tbl.LSVerifyThreadExists {
		put(v.ThreadKey, "", "", v.ThreadType, 0)
	}
	for _, v := range tbl.LSDeleteThenInsertThread {
		put(v.ThreadKey, v.ThreadName, v.Snippet, v.ThreadType, v.LastActivityTimestampMs)
	}
	for _, v := range tbl.LSUpdateOrInsertThread {
		put(v.ThreadKey, v.ThreadName, v.Snippet, v.ThreadType, v.LastActivityTimestampMs)
	}
	for _, v := range tbl.LSVerifyHybridThreadExists {
		kind := table.ENCRYPTED_OVER_WA_ONE_TO_ONE
		if v.IsGroupThread {
			kind = table.ENCRYPTED_OVER_WA_GROUP
		}
		put(v.ThreadJID, "", "", kind, v.LastActivityTimestampMS)
	}
	// One-to-one thread IDs often match contact IDs.
	for _, v := range tbl.LSDeleteThenInsertContact {
		key := canonical(v.Id)
		c, ok := s.cache.Conversations[key]
		if ok && c.Name == "" {
			c.Name = v.Name
			s.cache.Conversations[key] = c
		}
	}
	for _, v := range tbl.LSVerifyContactRowExists {
		key := canonical(v.ContactId)
		c, ok := s.cache.Conversations[key]
		if ok && c.Name == "" {
			c.Name = v.Name
			s.cache.Conversations[key] = c
		}
	}
	for _, v := range tbl.LSUpdateThreadSnippet {
		key := canonical(v.ThreadKey)
		c := s.cache.Conversations[key]
		c.ID = key
		c.Snippet = v.Snippet
		s.cache.Conversations[key] = c
	}
	for _, v := range tbl.LSDeleteMessage {
		delete(s.cache.Messages, messageKey(canonical(v.ThreadKey), v.MessageId))
	}
	for _, v := range tbl.LSDeleteThread {
		key := canonical(v.ThreadKey)
		delete(s.cache.Conversations, key)
		for k, m := range s.cache.Messages {
			if m.ConversationID == key {
				delete(s.cache.Messages, k)
			}
		}
	}
	var messages []Message
	add := func(id string, thread, sender, ts int64, text string) {
		key := canonical(thread)
		c := s.cache.Conversations[key]
		// Encrypted-history placeholders are not decrypted message bodies.
		if c.Encrypted {
			return
		}
		messages = append(messages, Message{ID: id, ConversationID: key, SenderID: idString(sender), Timestamp: ts, Text: text})
	}
	for _, v := range tbl.LSInsertMessage {
		add(v.MessageId, v.ThreadKey, v.SenderId, v.TimestampMs, v.Text)
	}
	for _, v := range tbl.LSUpsertMessage {
		add(v.MessageId, v.ThreadKey, v.SenderId, v.TimestampMs, v.Text)
	}
	for _, v := range tbl.LSDeleteThenInsertMessage {
		add(v.MessageId, v.ThreadKey, v.SenderId, v.TimestampMs, v.Text)
	}
	err := s.saveLocked()
	s.mu.Unlock()
	if err != nil {
		return err
	}
	for _, m := range messages {
		changed, err := s.putMessage(m)
		if err != nil {
			return err
		}
		if live && changed {
			e.wire.event("message", m)
		}
	}
	for _, v := range tbl.LSUpsertReaction {
		thread := idString(v.ThreadKey)
		s.mu.Lock()
		if mapped := s.cache.Mappings[thread]; mapped != "" {
			thread = mapped
		}
		s.mu.Unlock()
		if err = s.reaction(thread, v.MessageId, idString(v.ActorId), v.Reaction); err != nil {
			return err
		}
		if live {
			e.wire.event("reaction", map[string]string{"conversationId": thread, "messageId": v.MessageId, "actorId": idString(v.ActorId), "emoji": v.Reaction})
		}
	}
	for _, v := range tbl.LSDeleteReaction {
		thread := idString(v.ThreadKey)
		s.mu.Lock()
		if mapped := s.cache.Mappings[thread]; mapped != "" {
			thread = mapped
		}
		s.mu.Unlock()
		if err = s.reaction(thread, v.MessageId, idString(v.ActorId), ""); err != nil {
			return err
		}
		if live {
			e.wire.event("reaction", map[string]string{"conversationId": thread, "messageId": v.MessageId, "actorId": idString(v.ActorId), "emoji": ""})
		}
	}
	if live {
		e.wire.event("conversationsChanged", map[string]bool{"changed": true})
	}
	return nil
}

func (e *Engine) encryptedEvent(raw any) bool {
	if e.ctx.Err() != nil {
		return false
	}
	switch evt := raw.(type) {
	case *events.Connected:
		e.e2eeReady.Store(true)
		select {
		case e.e2eeSignal <- struct{}{}:
		default:
		}
		e.wire.event("connection", map[string]string{"transport": "encrypted", "state": "connected"})
	case *events.Disconnected:
		e.e2eeReady.Store(false)
		e.wire.event("connection", map[string]string{"transport": "encrypted", "state": "reconnecting"})
	case events.PermanentDisconnect:
		e.e2eeReady.Store(false)
		select {
		case e.e2eeSignal <- struct{}{}:
		default:
		}
		e.wire.event("authenticationRequired", rpcError("E2EE_UNAVAILABLE", "Facebook rejected the encrypted device session. Refresh the browser session and restart the CLI."))
	case *events.FBMessage:
		thread := evt.Info.Chat.User
		content := evt.GetConsumerApplication().GetPayload().GetContent()
		if reaction := content.GetReactionMessage(); reaction != nil {
			if err := e.store.reaction(thread, reaction.GetKey().GetID(), evt.Info.Sender.User, reaction.GetText()); err != nil {
				return false
			}
			e.wire.event("reaction", map[string]string{"conversationId": thread, "messageId": reaction.GetKey().GetID(), "actorId": evt.Info.Sender.User, "emoji": reaction.GetText()})
			return true
		}
		text := "[Unsupported message]"
		if body := content.GetMessageText(); body != nil {
			text = body.GetText()
		} else if body := content.GetExtendedTextMessage(); body != nil {
			text = body.GetText().GetText()
		}
		m := Message{ID: evt.Info.ID, ConversationID: thread, SenderID: evt.Info.Sender.User, SenderJID: evt.Info.Sender.ToNonAD().String(), Timestamp: evt.Info.Timestamp.UnixMilli(), Text: text, Encrypted: true}
		if m.Timestamp == 0 {
			m.Timestamp = time.Now().UnixMilli()
		}
		e.store.mu.Lock()
		c := e.store.cache.Conversations[thread]
		c.ID = thread
		c.Encrypted = true
		c.JID = evt.Info.Chat.ToNonAD().String()
		c.Type = 15
		if evt.Info.IsGroup {
			c.Type = 16
		}
		e.store.cache.Conversations[thread] = c
		e.store.mu.Unlock()
		changed, err := e.store.putMessage(m)
		if err != nil {
			return false
		}
		if changed {
			e.wire.event("message", m)
			e.wire.event("conversationsChanged", map[string]bool{"changed": true})
		}
	}
	return true
}
