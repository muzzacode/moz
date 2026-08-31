package memory

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/muzzacode/moz/internal/config"
)

type ToolCall struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	Model      string     `json:"model,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Timestamp  time.Time  `json:"timestamp"`
}

type Session struct {
	ID       string    `json:"id"`
	Started  time.Time `json:"started"`
	Messages []Message `json:"messages"`
}

type SessionInfo struct {
	ID       string
	Started  time.Time
	Messages int
	Preview  string
}

type Store struct {
	cfg *config.Config
}

func New(cfg *config.Config) *Store {
	return &Store{cfg: cfg}
}

func (s *Store) dir() string {
	dir := s.cfg.ExpandMemoryDir()
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config", "moz", "memory")
	}
	return dir
}

func (s *Store) sessionsDir() string {
	return filepath.Join(s.dir(), "sessions")
}

func (s *Store) Ensure() error {
	return os.MkdirAll(s.sessionsDir(), 0755)
}

func (s *Store) SaveSession(sess *Session) error {
	if err := s.Ensure(); err != nil {
		return err
	}
	path := filepath.Join(s.sessionsDir(), sess.ID+".json")
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func (s *Store) LoadSession(id string) (*Session, error) {
	path := filepath.Join(s.sessionsDir(), id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, err
	}
	return &sess, nil
}

func (s *Store) ListSessions() ([]string, error) {
	if err := s.Ensure(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.sessionsDir())
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			ids = append(ids, e.Name()[:len(e.Name())-5])
		}
	}
	return ids, nil
}

func (s *Store) SessionInfos() ([]SessionInfo, error) {
	ids, err := s.ListSessions()
	if err != nil {
		return nil, err
	}
	infos := make([]SessionInfo, 0, len(ids))
	for _, id := range ids {
		sess, err := s.LoadSession(id)
		if err != nil {
			continue
		}
		preview := ""
		for _, msg := range sess.Messages {
			if msg.Role == "user" && strings.TrimSpace(msg.Content) != "" {
				preview = strings.Join(strings.Fields(msg.Content), " ")
				if len(preview) > 60 {
					preview = preview[:57] + "..."
				}
				break
			}
		}
		infos = append(infos, SessionInfo{ID: id, Started: sess.Started, Messages: len(sess.Messages), Preview: preview})
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Started.After(infos[j].Started) })
	return infos, nil
}

func (s *Store) LatestSession() (*Session, error) {
	infos, err := s.SessionInfos()
	if err != nil {
		return nil, err
	}
	if len(infos) == 0 {
		return nil, fmt.Errorf("no saved sessions")
	}
	return s.LoadSession(infos[0].ID)
}

func (s *Store) Summary() string {
	sessions, err := s.ListSessions()
	if err != nil {
		return fmt.Sprintf("memory dir: %s (uninitialized)", s.dir())
	}
	return fmt.Sprintf("memory dir: %s | sessions: %d", s.dir(), len(sessions))
}

// NewSession creates an empty session with a sortable, collision-resistant ID.
//
// The ID must be unique even for two sessions created in the same millisecond,
// otherwise starting a new session immediately after another would overwrite the
// earlier session file on disk and lose the conversation.
func NewSession() *Session {
	now := time.Now().UTC()
	return &Session{
		ID:      fmt.Sprintf("%s-%09d-%s", now.Format("20060102T150405"), now.Nanosecond(), randomSuffix()),
		Started: now,
	}
}

// randomSuffix guards against collisions when the clock is coarse or two
// sessions are created in the same nanosecond tick.
func randomSuffix() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "0000"
	}
	return hex.EncodeToString(b[:])
}
