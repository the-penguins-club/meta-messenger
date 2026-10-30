package service

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

type fakeCaller struct{}

func (fakeCaller) Request(string) string { return `{"id":"1","result":{"protocolVersion":1}}` }
func testHandler() *Handler              { h := New(token); h.Attach(fakeCaller{}); return h }

func TestAuthorizationAndOrigin(t *testing.T) {
	h := testHandler()
	for _, tc := range []struct {
		auth, origin, path string
		want               int
	}{
		{"", "", "/v1/rpc", 401}, {"Bearer wrong", "", "/v1/rpc", 401},
		{"", "", "/v1/rpc?token=" + token, 401},
		{"Bearer " + token, "https://attacker.test", "/v1/rpc", 403},
		{"Bearer " + token, "", "/v1/rpc", 200},
	} {
		r := httptest.NewRequest("POST", tc.path, strings.NewReader(`{"id":"1","method":"hello"}`))
		r.Header.Set("Authorization", tc.auth)
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("got %d, expected %d", w.Code, tc.want)
		}
		if w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("unexpected CORS permission")
		}
	}
}

func TestRequestSizeAndJSON(t *testing.T) {
	for _, tc := range []struct {
		body, content string
		want          int
	}{{"bad", "application/json", 400}, {`{}`, "text/plain", 415}, {strings.Repeat("x", 1024*1024+1), "application/json", 413}} {
		r := httptest.NewRequest("POST", "/v1/rpc", strings.NewReader(tc.body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", tc.content)
		w := httptest.NewRecorder()
		testHandler().ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("got %d, want %d", w.Code, tc.want)
		}
	}
}

func TestStreamResyncAndConcurrentPublish(t *testing.T) {
	h := testHandler()
	server := httptest.NewServer(h)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/v1/events", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	line, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(line, `"resync"`) {
		t.Fatal("missing resync")
	}
	_, _ = reader.ReadString('\n')
	h.Publish(`{"event":"message","data":{"id":"large-id-as-string"}}`)
	line, err = reader.ReadString('\n')
	if err != nil || !strings.Contains(line, "large-id-as-string") {
		t.Fatal("missing event")
	}
	cancel()
	_, _ = io.Copy(io.Discard, reader)
}

func TestSlowSubscribersCannotBlockEngine(t *testing.T) {
	h := testHandler()
	ch := make(chan string, 1)
	h.subscribers[ch] = struct{}{}
	h.Publish("one")
	h.Publish("two")
	if len(h.subscribers) != 0 {
		t.Fatal("slow subscriber retained")
	}
	if <-ch != "one" {
		t.Fatal("queued event lost")
	}
	if _, ok := <-ch; ok {
		t.Fatal("slow stream not closed")
	}
}
