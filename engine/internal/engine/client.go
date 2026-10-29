package engine

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-meta/pkg/messagix"
	"go.mau.fi/mautrix-meta/pkg/messagix/cookies"
	"go.mau.fi/mautrix-meta/pkg/messagix/httpclient"
	"go.mau.fi/mautrix-meta/pkg/messagix/methods"
	"go.mau.fi/mautrix-meta/pkg/messagix/socket"
	"go.mau.fi/mautrix-meta/pkg/messagix/table"
	metaTypes "go.mau.fi/mautrix-meta/pkg/messagix/types"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waConsumerApplication"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waTypes "go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

type Engine struct {
	ctx         context.Context
	cancel      context.CancelFunc
	store       *Store
	wire        *Wire
	client      *messagix.Client
	wa          *whatsmeow.Client
	devices     *sqlstore.Container
	mainStarted bool
	mainReady   atomic.Bool
	e2eeReady   atomic.Bool
	e2eeSignal  chan struct{}
}

func newEngine(ctx context.Context, s *Store, w *Wire) *Engine {
	ctx, cancel := context.WithCancel(ctx)
	return &Engine{ctx: ctx, cancel: cancel, store: s, wire: w, e2eeSignal: make(chan struct{}, 1)}
}

func upstreamError(err error) *RPCError {
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return rpcError("TIMEOUT", "The connection timed out. Check connectivity and try again.")
	case errors.Is(err, httpclient.ErrTokenInvalidated):
		return rpcError("AUTH_REQUIRED", "Facebook expired this session. Log in in your browser and import fresh cookies.")
	case errors.Is(err, httpclient.ErrChallengeRequired), errors.Is(err, httpclient.ErrCheckpointRequired), errors.Is(err, httpclient.ErrConsentRequired):
		return rpcError("AUTH_REQUIRED", "Complete Facebook's verification in your browser, then import the updated session.")
	case errors.Is(err, httpclient.ErrAccountSuspended):
		return rpcError("ACCOUNT_RESTRICTED", "Facebook restricted this account. Open Facebook in your browser.")
	case errors.Is(err, httpclient.ErrRateLimited):
		return rpcError("RATE_LIMITED", "Facebook rate limited the connection. Wait before retrying.")
	default:
		return rpcError("UPSTREAM_ERROR", "Facebook connection or protocol operation failed. The private API may have changed.")
	}
}

func (e *Engine) status() map[string]any {
	e.store.mu.Lock()
	account := e.store.cache.Account
	e.store.mu.Unlock()
	_, err := e.store.Session()
	return map[string]any{"sessionImported": err == nil, "connected": e.mainReady.Load(), "encryptedConnected": e.e2eeReady.Load(), "account": account}
}

func (e *Engine) connect(ctx context.Context) (any, error) {
	if e.client != nil {
		if e.mainReady.Load() && e.e2eeReady.Load() {
			return e.status(), nil
		}
		return nil, rpcError("NOT_CONNECTED", "The engine is reconnecting or the connection failed. Restart the CLI to reload the session.")
	}
	session, err := e.store.Session()
	if err != nil {
		return nil, err
	}
	c := &cookies.Cookies{Platform: metaTypes.Facebook}
	values := map[cookies.MetaCookieName]string{}
	for k, v := range session.Cookies {
		values[cookies.MetaCookieName(k)] = v
	}
	c.UpdateValues(values)
	e.client = messagix.NewClient(c, zerolog.Nop(), &messagix.Config{})
	e.client.SetEventHandler(e.metaEvent)
	e.wire.event("connection", map[string]any{"transport": "main", "state": "connecting"})
	account, tbl, err := e.client.LoadMessagesPage(ctx)
	if err != nil {
		return nil, upstreamError(err)
	}
	e.store.mu.Lock()
	e.store.cache.Account = &Account{ID: strconv.FormatInt(c.GetUserID(), 10), Name: account.GetName()}
	err = e.store.saveLocked()
	e.store.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if err = e.ingest(tbl, false); err != nil {
		return nil, err
	}
	if err = e.store.Import(cookieMap(c)); err != nil {
		return nil, err
	}
	if err = e.client.Connect(e.ctx); err != nil {
		return nil, upstreamError(err)
	}
	e.mainStarted = true
	if err = e.client.WaitUntilCanSendMessages(ctx, 30*time.Second); err != nil {
		return nil, upstreamError(err)
	}
	e.mainReady.Store(true)
	if err = e.connectEncrypted(ctx, c.GetUserID()); err != nil {
		return nil, err
	}
	return e.status(), nil
}

func cookieMap(c *cookies.Cookies) map[string]string {
	m := map[string]string{}
	for k, v := range c.GetAll() {
		m[string(k)] = v
	}
	return m
}

func (e *Engine) connectEncrypted(ctx context.Context, userID int64) error {
	path := filepath.Join(e.store.dir, "devices.db")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	f.Close()
	if err = os.Chmod(path, 0600); err != nil {
		return err
	}
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("_foreign_keys", "on")
	q.Set("_busy_timeout", "5000")
	u.RawQuery = q.Encode()
	e.devices, err = sqlstore.New(ctx, "sqlite3", u.String(), nil)
	if err != nil {
		return err
	}
	all, err := e.devices.GetAllDevices(ctx)
	if err != nil {
		return err
	}
	device := e.devices.NewDevice()
	for _, d := range all {
		if d.ID != nil && d.ID.User == strconv.FormatInt(userID, 10) {
			device = d
			break
		}
	}
	e.client.SetDevice(device)
	if device.ID == nil {
		if err = e.client.RegisterE2EE(ctx, userID); err != nil {
			return upstreamError(err)
		}
		if err = device.Save(ctx); err != nil {
			return err
		}
	}
	e.wa, err = e.client.PrepareE2EEClient()
	if err != nil {
		return upstreamError(err)
	}
	e.wa.EnableDecryptedEventBuffer = true
	e.wa.SynchronousAck = true
	e.wa.AddEventHandlerWithSuccessStatus(e.encryptedEvent)
	if err = e.wa.ConnectContext(e.ctx); err != nil {
		return upstreamError(err)
	}
	select {
	case <-e.e2eeSignal:
		if !e.e2eeReady.Load() {
			return rpcError("E2EE_UNAVAILABLE", "Encrypted Messenger connection failed. Restart the CLI or refresh the browser session.")
		}
	case <-ctx.Done():
		return upstreamError(ctx.Err())
	}
	return nil
}

func (e *Engine) close() {
	e.cancel()
	if e.wa != nil {
		e.wa.Disconnect()
	}
	if e.client != nil {
		if e.mainStarted {
			e.client.Disconnect()
		}
		_ = e.store.Import(cookieMap(e.client.GetCookies()))
	}
	if e.devices != nil {
		_ = e.devices.Close()
	}
}

func (e *Engine) metaEvent(ctx context.Context, event any) {
	if e.ctx.Err() != nil {
		return
	}
	switch evt := event.(type) {
	case *table.LSTable:
		if err := e.ingest(evt, true); err != nil {
			e.wire.event("engineError", rpcError("STORAGE_ERROR", "Failed to persist incoming Messenger updates."))
			return
		}
		e.client.PostHandlePublishResponse(evt)
	case *messagix.ConnectedEvent, *messagix.ReconnectedEvent:
		e.mainReady.Store(true)
		e.wire.event("connection", map[string]string{"transport": "main", "state": "connected"})
	case *messagix.TransientDisconnectEvent:
		e.mainReady.Store(false)
		e.wire.event("connection", map[string]string{"transport": "main", "state": "reconnecting"})
	case *messagix.PermanentErrorEvent:
		e.mainReady.Store(false)
		e.wire.event("authenticationRequired", rpcError("AUTH_REQUIRED", "Facebook rejected the realtime session. Refresh the browser login."))
	}
}

func (e *Engine) conversation(id string) (Conversation, error) {
	if _, err := parseID(id); err != nil {
		return Conversation{}, err
	}
	e.store.mu.Lock()
	defer e.store.mu.Unlock()
	c, ok := e.store.cache.Conversations[id]
	if !ok || c.Type == 0 {
		return c, rpcError("UNKNOWN_CONVERSATION", "Select a conversation returned by conversations.list first.")
	}
	// Only ordinary threads and the supported encrypted transports are safe to route.
	if c.Type != 1 && c.Type != 2 && c.Type != 15 && c.Type != 16 {
		return c, rpcError("UNSUPPORTED_CONVERSATION", "This conversation type is not supported yet.")
	}
	return c, nil
}

func (e *Engine) send(ctx context.Context, id, text string) (any, error) {
	c, err := e.conversation(id)
	if err != nil {
		return nil, err
	}
	if e.client == nil {
		return nil, rpcError("NOT_CONNECTED", "Connect before sending messages.")
	}
	messageID := strconv.FormatInt(methods.GenerateEpochID(), 10)
	sender := strconv.FormatInt(e.client.GetCookies().GetUserID(), 10)
	m := Message{ID: messageID, ConversationID: id, SenderID: sender, Text: text, Timestamp: time.Now().UnixMilli(), Encrypted: c.Encrypted}
	if c.Encrypted {
		if !e.e2eeReady.Load() {
			return nil, rpcError("E2EE_UNAVAILABLE", "Encrypted transport is not connected.")
		}
		jid, err := conversationJID(c)
		if err != nil {
			return nil, err
		}
		app := wrapContent(&waConsumerApplication.ConsumerApplication_Content{Content: &waConsumerApplication.ConsumerApplication_Content_MessageText{MessageText: &waCommon.MessageText{Text: proto.String(text)}}})
		resp, err := e.wa.SendFBMessage(ctx, jid, app, nil, whatsmeow.SendRequestExtra{ID: messageID})
		if err != nil {
			return nil, rpcError("SEND_UNCONFIRMED", "Message delivery is unconfirmed. Check the conversation before sending again.")
		}
		m.Timestamp = resp.Timestamp.UnixMilli()
		m.SenderJID = e.wa.Store.ID.ToNonAD().String()
	} else {
		if err = e.client.WaitUntilCanSendMessages(ctx, 15*time.Second); err != nil {
			return nil, upstreamError(err)
		}
		thread, _ := parseID(id)
		tbl, err := e.client.ExecuteTasks(ctx, &socket.SendMessageTask{ThreadId: thread, Otid: mustInt(messageID), Source: table.MESSENGER_INBOX_IN_THREAD, SendType: table.TEXT, SyncGroup: 1, Text: text})
		if err != nil {
			return nil, rpcError("SEND_UNCONFIRMED", "Message delivery is unconfirmed. Check the conversation before sending again.")
		}
		confirmed := ""
		for _, v := range tbl.LSReplaceOptimsiticMessage {
			if v.OfflineThreadingId == messageID {
				confirmed = v.MessageId
			}
		}
		if confirmed == "" {
			return nil, rpcError("SEND_UNCONFIRMED", "Facebook did not confirm a message ID. Check the conversation before retrying.")
		}
		m.ID = confirmed
		if err = e.ingest(tbl, true); err != nil {
			return nil, rpcError("SENT_NOT_SAVED", "Facebook accepted the message, but its local cache could not be saved.")
		}
	}
	changed, err := e.store.putMessage(m)
	if err != nil {
		return nil, rpcError("SENT_NOT_SAVED", "Facebook accepted the message, but its local cache could not be saved.")
	}
	if changed {
		e.wire.event("message", m)
	}
	return m, nil
}

func mustInt(s string) int64 { v, _ := strconv.ParseInt(s, 10, 64); return v }
func conversationJID(c Conversation) (waTypes.JID, error) {
	if c.JID != "" {
		j, err := waTypes.ParseJID(c.JID)
		if err == nil && !j.IsEmpty() {
			return j, nil
		}
	}
	server := waTypes.MessengerServer
	if c.Type == 16 {
		server = waTypes.GroupServer
	}
	return waTypes.JID{User: c.ID, Server: server}, nil
}

func wrapContent(content *waConsumerApplication.ConsumerApplication_Content) *waConsumerApplication.ConsumerApplication {
	return &waConsumerApplication.ConsumerApplication{Payload: &waConsumerApplication.ConsumerApplication_Payload{Payload: &waConsumerApplication.ConsumerApplication_Payload_Content{Content: content}}}
}

func (e *Engine) react(ctx context.Context, thread, id, emoji string) (any, error) {
	c, err := e.conversation(thread)
	if err != nil {
		return nil, err
	}
	if id == "" {
		return nil, rpcError("INVALID_PARAMS", "A message ID is required.")
	}
	if e.client == nil {
		return nil, rpcError("NOT_CONNECTED", "Connect before reacting.")
	}
	actor := strconv.FormatInt(e.client.GetCookies().GetUserID(), 10)
	if c.Encrypted {
		if !e.e2eeReady.Load() {
			return nil, rpcError("E2EE_UNAVAILABLE", "Encrypted transport is not connected.")
		}
		e.store.mu.Lock()
		m, ok := e.store.cache.Messages[messageKey(thread, id)]
		e.store.mu.Unlock()
		if !ok || m.SenderJID == "" {
			return nil, rpcError("MESSAGE_NOT_CACHED", "The encrypted message must be in the local cache before reacting.")
		}
		jid, _ := conversationJID(c)
		key := &waCommon.MessageKey{RemoteJID: proto.String(jid.String()), ID: proto.String(id), FromMe: proto.Bool(m.SenderID == actor)}
		if c.Type == 16 {
			key.Participant = proto.String(m.SenderJID)
		}
		app := wrapContent(&waConsumerApplication.ConsumerApplication_Content{Content: &waConsumerApplication.ConsumerApplication_Content_ReactionMessage{ReactionMessage: &waConsumerApplication.ConsumerApplication_ReactionMessage{Key: key, Text: proto.String(emoji), SenderTimestampMS: proto.Int64(time.Now().UnixMilli())}}})
		_, err = e.wa.SendFBMessage(ctx, jid, app, nil, whatsmeow.SendRequestExtra{ID: strconv.FormatInt(methods.GenerateEpochID(), 10)})
	} else {
		if err = e.client.WaitUntilCanSendMessages(ctx, 15*time.Second); err != nil {
			return nil, upstreamError(err)
		}
		var tbl *table.LSTable
		tbl, err = e.client.ExecuteTasks(ctx, &socket.SendReactionTask{ThreadKey: mustInt(thread), MessageID: id, ActorID: mustInt(actor), Reaction: emoji, SendAttribution: table.MESSENGER_INBOX_IN_THREAD})
		if err == nil {
			for _, v := range tbl.LSHandleFailedTask {
				_ = v
				return nil, rpcError("REACTION_UNCONFIRMED", "Facebook rejected the reaction task.")
			}
		}
	}
	if err != nil {
		return nil, rpcError("REACTION_UNCONFIRMED", "Reaction acceptance is unconfirmed. Check the conversation before retrying.")
	}
	if err = e.store.reaction(thread, id, actor, emoji); err != nil {
		return nil, rpcError("REACTION_NOT_SAVED", "The reaction was submitted but its local cache could not be saved.")
	}
	result := map[string]string{"conversationId": thread, "messageId": id, "actorId": actor, "emoji": emoji, "status": "submitted"}
	e.wire.event("reaction", result)
	return result, nil
}
