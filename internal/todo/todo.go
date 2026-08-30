package todo

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/muzzacode/moz/internal/config"
)

type Todo struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	Done      bool      `json:"done"`
	CreatedAt time.Time `json:"created_at"`
}

type List struct {
	Items []Todo `json:"items"`
	mu    sync.Mutex
}

func New() *List {
	return &List{Items: []Todo{}}
}

func (l *List) Add(text string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	t := Todo{
		ID:        fmt.Sprintf("t%d", time.Now().UnixNano()%1e9),
		Text:      text,
		CreatedAt: time.Now().UTC(),
	}
	l.Items = append(l.Items, t)
	return t.ID
}

func (l *List) MarkDone(id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := range l.Items {
		if l.Items[i].ID == id || (len(id) >= 3 && len(l.Items[i].ID) >= 3 && l.Items[i].ID[:len(id)] == id) {
			l.Items[i].Done = true
			return true
		}
	}
	return false
}

func (l *List) Remove(id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i, t := range l.Items {
		if t.ID == id || (len(id) >= 3 && len(t.ID) >= 3 && t.ID[:len(id)] == id) {
			l.Items = append(l.Items[:i], l.Items[i+1:]...)
			return true
		}
	}
	return false
}

func (l *List) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.Items = nil
}

func (l *List) All() []Todo {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Todo, len(l.Items))
	copy(out, l.Items)
	return out
}

func (l *List) PendingCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, t := range l.Items {
		if !t.Done {
			n++
		}
	}
	return n
}

func (l *List) Render() string {
	items := l.All()
	if len(items) == 0 {
		return "No todos."
	}
	var b strings.Builder
	b.WriteString("## Todos\n")
	for _, t := range items {
		mark := "[ ]"
		if t.Done {
			mark = "[x]"
		}
		b.WriteString(fmt.Sprintf("%s %s %s\n", mark, t.ID, t.Text))
	}
	return b.String()
}

// Store persists todos to disk.
type Store struct {
	cfg *config.Config
}

func NewStore(cfg *config.Config) *Store {
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

func (s *Store) path() string {
	return filepath.Join(s.dir(), "todos.json")
}

func (s *Store) Save(l *List) error {
	if err := os.MkdirAll(s.dir(), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path(), data, 0644)
}

func (s *Store) Load() (*List, error) {
	data, err := os.ReadFile(s.path())
	if err != nil {
		if os.IsNotExist(err) {
			return New(), nil
		}
		return nil, err
	}
	var l List
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, err
	}
	if l.Items == nil {
		l.Items = []Todo{}
	}
	return &l, nil
}
