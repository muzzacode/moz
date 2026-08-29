package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/muzzacode/moz/internal/config"
)

type Message struct {
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Model     string    `json:"model,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

type Session struct {
	ID       string    `json:"id"`
	Started  time.Time `json:"started"`
	Messages []Message `json:"messages"`
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

func (s *Store) Summary() string {
	sessions, err := s.ListSessions()
	if err != nil {
		return fmt.Sprintf("memory dir: %s (uninitialized)", s.dir())
	}
	return fmt.Sprintf("memory dir: %s | sessions: %d", s.dir(), len(sessions))
}

func NewSession() *Session {
	now := time.Now().UTC()
	return &Session{
		ID:      fmt.Sprintf("%s-%d", now.Format("20060102T150405"), now.Nanosecond()/1e6),
		Started: now,
	}
}
