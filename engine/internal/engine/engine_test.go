package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"go.mau.fi/mautrix-meta/pkg/messagix/table"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waConsumerApplication"
	waTypes "go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "profile"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func testEngine(t *testing.T) (*Engine, *bytes.Buffer) {
	t.Helper()
	s := testStore(t)
	out := &bytes.Buffer{}
	e := newEngine(context.Background(), s, &Wire{out: out})
	t.Cleanup(e.cancel)
	return e, out
}
func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	var rpc *RPCError
	if !errors.As(err, &rpc) || rpc.Code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
}

func TestProfileLockAndSessionPersistence(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "profile")
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = OpenStore(dir)
	assertCode(t, err, "PROFILE_LOCKED")
	_, err = s.Session()
	assertCode(t, err, "AUTH_REQUIRED")
	creds := map[string]string{"c_user": "9007199254740993", "xs": "test-session", "datr": "test-device"}
	if err = s.Import(creds); err != nil {
		t.Fatal(err)
	}
	if err = s.Import(map[string]string{"c_user": "2", "xs": "x", "datr": "d"}); err == nil {
		t.Fatal("account switch accepted")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	session, err := s.Session()
	if err != nil || session.Cookies["c_user"] != creds["c_user"] {
		t.Fatal("session did not persist")
	}
	for _, name := range []string{"", "session.json", "profile.lock"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0077 != 0 {
			t.Errorf("%s has non-owner permissions", name)
		}
	}
}

func TestMalformedRequestsDoNotBreakFraming(t *testing.T) {
	in := bytes.NewBufferString("invalid\n{\"id\":\"one\",\"method\":\"hello\"}\n{\"id\":\"two\",\"method\":\"messages.list\",\"params\":{\"conversationId\":\"not-a-number\"}}\n")
	out := &bytes.Buffer{}
	if err := Serve(context.Background(), in, out, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(out)
	for i := 0; i < 3; i++ {
		var response map[string]json.RawMessage
		if err := decoder.Decode(&response); err != nil {
			t.Fatal(err)
		}
		if i == 1 && response["result"] == nil {
			t.Fatal("hello failed after malformed input")
		}
		if i != 1 && response["error"] == nil {
			t.Fatal("expected error response")
		}
	}
}

func TestIngestAndEncryptedEvents(t *testing.T) {
	e, _ := testEngine(t)
	tbl := &table.LSTable{
		LSDeleteThenInsertThread:   []*table.LSDeleteThenInsertThread{{ThreadKey: 9007199254740993, ThreadName: "Large ID", ThreadType: table.ENCRYPTED_OVER_WA_ONE_TO_ONE}},
		LSVerifyHybridThreadExists: []*table.LSVerifyHybridThreadExists{{ThreadKey: 9007199254740993, ThreadJID: 9007199254740995}},
	}
	if err := e.ingest(tbl, false); err != nil {
		t.Fatal(err)
	}
	threads := e.store.conversations()
	if len(threads) != 1 || threads[0].ID != "9007199254740995" || !threads[0].Encrypted {
		t.Fatalf("bad mapping: %+v", threads)
	}
	msg := &events.FBMessage{Info: waTypes.MessageInfo{MessageSource: waTypes.MessageSource{Chat: waTypes.JID{User: threads[0].ID, Server: waTypes.MessengerServer}, Sender: waTypes.JID{User: "42", Server: waTypes.MessengerServer}}, ID: "12345", Timestamp: time.Now()}}
	// Unsupported encrypted payloads remain visible as placeholders, without a panic.
	if !e.encryptedEvent(msg) {
		t.Fatal("event not persisted")
	}
	if !e.encryptedEvent(msg) {
		t.Fatal("duplicate not acknowledged")
	}
	messages := e.store.messages(threads[0].ID, 50)
	if len(messages) != 1 || !messages[0].Encrypted {
		t.Fatal("encrypted message missing or duplicated")
	}
	if messages[0].SenderJID == "" {
		t.Fatal("reaction sender metadata missing")
	}
}

func TestMessageDedupReactionsAndRetention(t *testing.T) {
	s := testStore(t)
	m := Message{ID: "m", ConversationID: "1", Text: "hello", Timestamp: 1}
	changed, err := s.putMessage(m)
	if !changed || err != nil {
		t.Fatal(err)
	}
	changed, err = s.putMessage(m)
	if changed || err != nil {
		t.Fatal("duplicate emitted")
	}
	if err = s.reaction("1", "m", "42", "👍"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.putMessage(m); err != nil {
		t.Fatal(err)
	}
	if s.messages("1", 50)[0].Reactions["42"] != "👍" {
		t.Fatal("replay erased reaction")
	}
	if err = s.reaction("1", "m", "42", ""); err != nil {
		t.Fatal(err)
	}
	if len(s.messages("1", 50)[0].Reactions) != 0 {
		t.Fatal("reaction not removed")
	}
	// Test the retention boundary without writing hundreds of fixtures to disk.
	s.mu.Lock()
	for i := 0; i < 501; i++ {
		id := idString(int64(i + 1))
		s.cache.Messages[messageKey("1", id)] = Message{ID: id, ConversationID: "1", Timestamp: int64(i + 1)}
	}
	s.mu.Unlock()
	_, err = s.putMessage(Message{ID: "latest", ConversationID: "1", Timestamp: 999})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.messages("1", 1000)) != 500 {
		t.Fatal("cache not bounded")
	}
}

func TestConcurrentCacheUpdates(t *testing.T) {
	s := testStore(t)
	_, _ = s.putMessage(Message{ID: "m", ConversationID: "1", Text: "hello"})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				_ = s.reaction("1", "m", "42", "👍")
				items := s.messages("1", 50)
				_, _ = json.Marshal(items)
			}
		}()
	}
	wg.Wait()
}

func TestUnknownConversationCannotFallBackToPlaintext(t *testing.T) {
	e, _ := testEngine(t)
	_, err := e.send(context.Background(), "123", "hello")
	assertCode(t, err, "UNKNOWN_CONVERSATION")
	e.store.cache.Conversations["123"] = Conversation{ID: "123", Type: 7}
	_, err = e.send(context.Background(), "123", "hello")
	assertCode(t, err, "UNSUPPORTED_CONVERSATION")
}

func TestEncryptedTextAndReactionDecode(t *testing.T) {
	e, _ := testEngine(t)
	info := waTypes.MessageInfo{MessageSource: waTypes.MessageSource{Chat: waTypes.JID{User: "123", Server: waTypes.MessengerServer}, Sender: waTypes.JID{User: "42", Server: waTypes.MessengerServer}}, ID: "m1", Timestamp: time.Now()}
	text := wrapContent(&waConsumerApplication.ConsumerApplication_Content{Content: &waConsumerApplication.ConsumerApplication_Content_MessageText{MessageText: &waCommon.MessageText{Text: proto.String("Hello encrypted world")}}})
	if !e.encryptedEvent(&events.FBMessage{Info: info, Message: text}) {
		t.Fatal("text not acknowledged")
	}
	if got := e.store.messages("123", 50); len(got) != 1 || got[0].Text != "Hello encrypted world" {
		t.Fatalf("bad text: %+v", got)
	}
	reaction := wrapContent(&waConsumerApplication.ConsumerApplication_Content{Content: &waConsumerApplication.ConsumerApplication_Content_ReactionMessage{ReactionMessage: &waConsumerApplication.ConsumerApplication_ReactionMessage{Key: &waCommon.MessageKey{ID: proto.String("m1")}, Text: proto.String("👍")}}})
	if !e.encryptedEvent(&events.FBMessage{Info: info, Message: reaction}) {
		t.Fatal("reaction not acknowledged")
	}
	if e.store.messages("123", 50)[0].Reactions["42"] != "👍" {
		t.Fatal("reaction missing")
	}
}
