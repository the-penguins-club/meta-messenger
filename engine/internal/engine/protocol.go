package engine

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"sync"
	"time"
)

type RPCError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string             { return e.Message }
func rpcError(code, message string) *RPCError { return &RPCError{code, message} }

type Request struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type Wire struct {
	mu  sync.Mutex
	out io.Writer
}

func (w *Wire) send(value any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	// A closed stdout means the parent exited; the stdin loop will terminate.
	_ = json.NewEncoder(w.out).Encode(value)
}
func (w *Wire) event(kind string, data any) { w.send(map[string]any{"event": kind, "data": data}) }

func Serve(ctx context.Context, input io.Reader, output io.Writer, dir string) error {
	wire := &Wire{out: output}
	s, err := OpenStore(dir)
	if err != nil {
		var rpc *RPCError
		if !errors.As(err, &rpc) {
			rpc = rpcError("STORAGE_ERROR", "Cannot open the local profile directory.")
		}
		wire.event("fatal", rpc)
		return err
	}
	defer s.Close()
	e := newEngine(ctx, s, wire)
	defer e.close()
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return nil
		}
		var req Request
		if json.Unmarshal(scanner.Bytes(), &req) != nil || req.ID == "" {
			wire.send(map[string]any{"id": req.ID, "error": rpcError("INVALID_REQUEST", "Expected an object with string id, method, and params.")})
			continue
		}
		result, err := e.handle(req)
		if err != nil {
			var rpc *RPCError
			if !errors.As(err, &rpc) {
				rpc = rpcError("ENGINE_ERROR", "The operation failed. Local profile data may be unavailable.")
			}
			wire.send(map[string]any{"id": req.ID, "error": rpc})
		} else {
			wire.send(map[string]any{"id": req.ID, "result": result})
		}
	}
	return scanner.Err()
}

func parseID(id string) (int64, error) {
	v, err := strconv.ParseInt(id, 10, 64)
	if err != nil || v <= 0 {
		return 0, rpcError("INVALID_ID", "Expected a positive numeric Facebook ID as a string.")
	}
	return v, nil
}

func (e *Engine) handle(req Request) (result any, err error) {
	defer func() {
		if recover() != nil {
			result = nil
			err = rpcError("INTERNAL_ERROR", "The engine encountered an unexpected protocol response.")
		}
	}()
	ctx, cancel := context.WithTimeout(e.ctx, 50*time.Second)
	defer cancel()
	var p struct {
		Cookies        map[string]string `json:"cookies"`
		ConversationID string            `json:"conversationId"`
		MessageID      string            `json:"messageId"`
		Text           string            `json:"text"`
		Emoji          string            `json:"emoji"`
		Limit          int               `json:"limit"`
	}
	if len(req.Params) > 0 && json.Unmarshal(req.Params, &p) != nil {
		return nil, rpcError("INVALID_PARAMS", "Invalid request parameters.")
	}
	switch req.Method {
	case "hello":
		return map[string]any{"protocolVersion": 1, "engine": "mautrix-meta", "upstreamCommit": "a7ee921f93452a9e9e296e270dd910ffc46edc32"}, nil
	case "session.import":
		if e.client != nil {
			return nil, rpcError("ALREADY_CONNECTED", "Import a session before connecting.")
		}
		return map[string]bool{"imported": true}, e.store.Import(p.Cookies)
	case "status":
		return e.status(), nil
	case "connect":
		return e.connect(ctx)
	case "conversations.list":
		return e.store.conversations(), nil
	case "messages.list":
		if _, err = parseID(p.ConversationID); err != nil {
			return nil, err
		}
		if p.Limit == 0 {
			p.Limit = 50
		}
		if p.Limit < 1 || p.Limit > 500 {
			return nil, rpcError("INVALID_PARAMS", "limit must be between 1 and 500.")
		}
		return e.store.messages(p.ConversationID, p.Limit), nil
	case "messages.send":
		if p.Text == "" {
			return nil, rpcError("INVALID_PARAMS", "Message text must not be empty.")
		}
		return e.send(ctx, p.ConversationID, p.Text)
	case "messages.react":
		return e.react(ctx, p.ConversationID, p.MessageID, p.Emoji)
	default:
		return nil, rpcError("METHOD_NOT_FOUND", "Unknown engine method.")
	}
}
