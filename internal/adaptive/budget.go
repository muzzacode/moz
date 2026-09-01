package adaptive

import (
	"sync"

	"github.com/muzzacode/moz/internal/cost"
	openai "github.com/sashabaranov/go-openai"
)

// Budget tracks cumulative spend so routing can refuse to escalate once a
// session has cost enough.
//
// Displaying cost is not the same as controlling it. Without a ceiling a long
// session on a frontier model can run up a bill with nothing to stop it.
type Budget struct {
	mu    sync.Mutex
	spent float64
	// Limit is the session ceiling in USD. Zero means unlimited.
	limit float64
}

func NewBudget(limit float64) *Budget {
	if limit < 0 {
		limit = 0
	}
	return &Budget{limit: limit}
}

// Add records the cost of a completed request.
func (b *Budget) Add(profileID string, u openai.Usage) {
	c := cost.Estimate(profileID, u)
	if c <= 0 {
		return
	}
	b.mu.Lock()
	b.spent += c
	b.mu.Unlock()
}

// AddCost records an already-computed cost.
func (b *Budget) AddCost(c float64) {
	if c <= 0 {
		return
	}
	b.mu.Lock()
	b.spent += c
	b.mu.Unlock()
}

func (b *Budget) Spent() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.spent
}

func (b *Budget) Limit() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.limit
}

// Exhausted reports whether the ceiling has been reached.
func (b *Budget) Exhausted() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.limit > 0 && b.spent >= b.limit
}

// Remaining returns how much of the ceiling is left. Zero limit means unlimited,
// reported as a negative value so callers can distinguish it.
func (b *Budget) Remaining() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.limit <= 0 {
		return -1
	}
	r := b.limit - b.spent
	if r < 0 {
		return 0
	}
	return r
}

// Reset clears accumulated spend, for a new session.
func (b *Budget) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.spent = 0
}
