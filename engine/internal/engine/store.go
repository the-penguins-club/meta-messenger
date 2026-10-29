package engine

import (
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/gofrs/flock"
)

type Account struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Session struct {
	Cookies map[string]string `json:"cookies"`
}

type Conversation struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Encrypted bool   `json:"encrypted"`
	UpdatedAt int64  `json:"updatedAt"`
	Snippet   string `json:"snippet"`
	Type      int64  `json:"type"`
	JID       string `json:"jid,omitempty"`
}

type Message struct {
	ID             string            `json:"id"`
	ConversationID string            `json:"conversationId"`
	SenderID       string            `json:"senderId"`
	SenderJID      string            `json:"senderJid,omitempty"`
	Text           string            `json:"text"`
	Timestamp      int64             `json:"timestamp"`
	Encrypted      bool              `json:"encrypted"`
	Reactions      map[string]string `json:"reactions,omitempty"`
}

type Cache struct {
	Account       *Account                `json:"account,omitempty"`
	Conversations map[string]Conversation `json:"conversations"`
	Messages      map[string]Message      `json:"messages"`
	Mappings      map[string]string       `json:"mappings"`
}

type Store struct {
	mu    sync.Mutex
	dir   string
	lock  *flock.Flock
	cache Cache
}

func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	lock := flock.New(filepath.Join(dir, "profile.lock"), flock.SetPermissions(0600))
	ok, err := lock.TryLock()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, rpcError("PROFILE_LOCKED", "This profile is already open in another Messenger process.")
	}
	s := &Store{dir: dir, lock: lock}
	s.cache = Cache{Conversations: map[string]Conversation{}, Messages: map[string]Message{}, Mappings: map[string]string{}}
	if err = readJSON(filepath.Join(dir, "cache.json"), &s.cache); err != nil && !errors.Is(err, os.ErrNotExist) {
		lock.Unlock()
		return nil, err
	}
	if s.cache.Conversations == nil {
		s.cache.Conversations = map[string]Conversation{}
	}
	if s.cache.Messages == nil {
		s.cache.Messages = map[string]Message{}
	}
	if s.cache.Mappings == nil {
		s.cache.Mappings = map[string]string{}
	}
	return s, nil
}

func readJSON(path string, into any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, into)
}

func writeJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".write-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Store) saveLocked() error { return writeJSON(filepath.Join(s.dir, "cache.json"), &s.cache) }
func (s *Store) Close() error      { return s.lock.Unlock() }

func (s *Store) Import(cookies map[string]string) error {
	if cookies["xs"] == "" || cookies["c_user"] == "" || cookies["datr"] == "" {
		return rpcError("INVALID_COOKIES", "Cookies must include xs, c_user, and datr.")
	}
	if _, err := parseID(cookies["c_user"]); err != nil {
		return rpcError("INVALID_COOKIES", "c_user must be a positive numeric Facebook ID.")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var old Session
	err := readJSON(filepath.Join(s.dir, "session.json"), &old)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if old.Cookies != nil && old.Cookies["c_user"] != cookies["c_user"] {
		return rpcError("ACCOUNT_MISMATCH", "Use a different profile directory for a different Facebook account.")
	}
	return writeJSON(filepath.Join(s.dir, "session.json"), Session{Cookies: cookies})
}

func (s *Store) Session() (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var session Session
	if err := readJSON(filepath.Join(s.dir, "session.json"), &session); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, rpcError("AUTH_REQUIRED", "Import a Facebook browser session with messenger login --cookies FILE.")
		}
		return nil, err
	}
	return &session, nil
}

func messageKey(thread, id string) string { return thread + "\x00" + id }

func (s *Store) conversations() []Conversation {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]Conversation, 0, len(s.cache.Conversations))
	for _, c := range s.cache.Conversations {
		items = append(items, c)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].UpdatedAt == items[j].UpdatedAt {
			return items[i].ID < items[j].ID
		}
		return items[i].UpdatedAt > items[j].UpdatedAt
	})
	return items
}

func (s *Store) messages(thread string, limit int) []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]Message, 0)
	for _, m := range s.cache.Messages {
		if m.ConversationID == thread {
			items = append(items, m)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Timestamp == items[j].Timestamp {
			return items[i].ID < items[j].ID
		}
		return items[i].Timestamp < items[j].Timestamp
	})
	if len(items) > limit {
		items = items[len(items)-limit:]
	}
	return items
}

func (s *Store) putMessage(m Message) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := messageKey(m.ConversationID, m.ID)
	old, exists := s.cache.Messages[key]
	if m.Reactions == nil {
		m.Reactions = maps.Clone(old.Reactions)
	}
	changed := !exists || old.Text != m.Text
	s.cache.Messages[key] = m
	c := s.cache.Conversations[m.ConversationID]
	c.ID = m.ConversationID
	if m.Encrypted {
		c.Encrypted = true
		if c.Type == 0 {
			c.Type = 15
		}
	}
	if m.Timestamp >= c.UpdatedAt {
		c.UpdatedAt = m.Timestamp
		c.Snippet = m.Text
	}
	s.cache.Conversations[c.ID] = c
	// Bound the plaintext cache per conversation. Crypto state lives in SQLite.
	var keys []string
	for k, v := range s.cache.Messages {
		if v.ConversationID == m.ConversationID {
			keys = append(keys, k)
		}
	}
	if len(keys) > 500 {
		sort.Slice(keys, func(i, j int) bool { return s.cache.Messages[keys[i]].Timestamp < s.cache.Messages[keys[j]].Timestamp })
		for _, k := range keys[:len(keys)-500] {
			delete(s.cache.Messages, k)
		}
	}
	return changed, s.saveLocked()
}

func (s *Store) reaction(thread, id, actor, emoji string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := messageKey(thread, id)
	m, ok := s.cache.Messages[key]
	if !ok {
		return nil
	}
	m.Reactions = maps.Clone(m.Reactions)
	if m.Reactions == nil {
		m.Reactions = map[string]string{}
	}
	if emoji == "" {
		delete(m.Reactions, actor)
	} else {
		m.Reactions[actor] = emoji
	}
	s.cache.Messages[key] = m
	return s.saveLocked()
}
