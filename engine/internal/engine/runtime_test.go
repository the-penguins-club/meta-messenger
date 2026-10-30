package engine

import (
	"encoding/json"
	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-meta/pkg/messagix"
	"go.mau.fi/mautrix-meta/pkg/messagix/cookies"
	metaTypes "go.mau.fi/mautrix-meta/pkg/messagix/types"
	"sync"
	"testing"
	"time"
)

func TestEmbeddedRuntimeLifecycle(t *testing.T) {
	dir := t.TempDir()
	r, err := OpenRuntime(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var result map[string]any
			if json.Unmarshal([]byte(r.Request(`{"id":"1","method":"hello"}`)), &result) != nil || result["result"] == nil {
				t.Error("embedded request failed")
			}
		}()
	}
	wg.Wait()
	r.Close()
	r.Close()
	var closed struct {
		Error *RPCError `json:"error"`
	}
	_ = json.Unmarshal([]byte(r.Request(`{"id":"2","method":"status"}`)), &closed)
	if closed.Error == nil || closed.Error.Code != "CLIENT_CLOSED" {
		t.Fatal("closed runtime accepted request")
	}
	reopened, err := OpenRuntime(dir, nil)
	if err != nil {
		t.Fatal("profile lock not released")
	}
	reopened.Close()
}

func TestCacheReadsDoNotWaitForNetworkRequestLock(t *testing.T) {
	r, err := OpenRuntime(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	r.mu.Lock() // Model a slow network operation without contacting Facebook.
	done := make(chan string, 1)
	go func() { done <- r.Request(`{"id":"cache","method":"messages.list","params":{"conversationId":"42"}}`) }()
	select {
	case data := <-done:
		var reply struct {
			Error *RPCError `json:"error"`
		}
		_ = json.Unmarshal([]byte(data), &reply)
		if reply.Error != nil {
			r.mu.Unlock()
			t.Fatal(reply.Error)
		}
	case <-time.After(time.Second):
		r.mu.Unlock()
		t.Fatal("cache read blocked behind network operation")
	}
	r.mu.Unlock()
}

func TestEmbeddedReauthenticationReplacesFailedClient(t *testing.T) {
	r, err := OpenRuntime(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	oldCookies := &cookies.Cookies{Platform: metaTypes.Facebook}
	oldCookies.UpdateValues(map[cookies.MetaCookieName]string{cookies.FBCookieCUser: "42", cookies.FBCookieXS: "old-session", cookies.MetaCookieDatr: "test-device"})
	if err = r.s.Import(cookieMap(oldCookies)); err != nil {
		t.Fatal(err)
	}
	r.e.client = messagix.NewClient(oldCookies, zerolog.Nop(), &messagix.Config{})
	old := r.e
	var reply struct {
		Error *RPCError `json:"error"`
	}
	_ = json.Unmarshal([]byte(r.Request(`{"id":"1","method":"session.import","params":{"cookies":{"c_user":"42","xs":"new-session","datr":"test-device"}}}`)), &reply)
	if reply.Error != nil {
		t.Fatal(reply.Error)
	}
	if r.e == old || r.e.client != nil {
		t.Fatal("failed connection was not replaced")
	}
	session, err := r.s.Session()
	if err != nil || session.Cookies["xs"] != "new-session" {
		t.Fatal("old shutdown overwrote fresh session")
	}
}
