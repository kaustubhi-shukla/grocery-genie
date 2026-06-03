// Session state for in-progress receipt confirmations.
//
// A "pending order" is a multi-step conversation that lives between
// the initial photo scan and the final "Saved!" confirmation. During
// this window the user can:
//   - Send additional photos that get merged into the same order
//   - Resolve ambiguous items the AI flagged
//   - Cancel the whole order
//   - Tap "Done" and pick a payment method
//
// Why in-memory? For a single-user MVP, simplicity wins. If the bot
// process restarts mid-confirmation, the user just re-sends the
// receipt — annoying but rare. A production version would persist
// pending orders to SQLite so restarts are transparent.

package telegram

import (
	"sync"
	"time"

	"github.com/kaustubhi-shukla/grocery-genie/internal/agents"
)

// sessionStage tracks where the user is in the confirmation flow.
// We use it both to route follow-up messages and to detect bugs
// (e.g., a payment-button tap arriving while the user has no order).
type sessionStage int

const (
	stageIdle              sessionStage = iota // no pending order
	stageCollectingPhotos                      // accepting more photos / awaiting Done
	stageResolvingAmbiguous                    // asking about an ambiguous item
	stageAwaitingPayment                       // showed payment buttons
)

// PendingOrder holds everything we know about an in-progress receipt.
// One per active user.
type PendingOrder struct {
	UserID    int64
	Stage     sessionStage
	Items     []agents.ReceiptItem
	Store     string
	Total     float64
	Date      string
	Ambiguous []string // names still awaiting clarification

	// CurrentAmbiguous is the name of the ambiguous item the user is
	// being asked about right now. Set when we send the question,
	// cleared when the user responds.
	CurrentAmbiguous string

	// PaymentBtnMsgID is the ID of the message that's currently
	// showing the payment buttons, so we can edit it in place.
	PaymentBtnMsgID int

	// QueuedScans holds full receipts that arrived while this order
	// was still open AND too close in time to be a multi-page upload.
	// When the current order is saved (or cancelled), we pop the
	// next queued scan and start a fresh order with it. Lets a user
	// drop several PDFs into chat at once and walk through them
	// sequentially without losing any.
	QueuedScans []*agents.Receipt

	// LastScanFinishedAt is the wall-clock time the last scan for
	// this order completed. Bulk-upload detection compares against
	// this to decide merge-vs-queue.
	LastScanFinishedAt time.Time

	UpdatedAt time.Time
}

// SessionStore is a goroutine-safe map of userID -> PendingOrder.
// Concurrent access is guarded by sync.RWMutex because Telegram
// callbacks can arrive in parallel for the same user (e.g. button
// tap while a message is still being processed).
type SessionStore struct {
	mu       sync.RWMutex
	orders   map[int64]*PendingOrder
	ttl      time.Duration
	stopChan chan struct{}
}

// NewSessionStore returns a store with the given session timeout and
// starts a background goroutine that sweeps expired sessions every
// minute. Call Close() when shutting down.
func NewSessionStore(ttl time.Duration) *SessionStore {
	s := &SessionStore{
		orders:   make(map[int64]*PendingOrder),
		ttl:      ttl,
		stopChan: make(chan struct{}),
	}
	go s.gcLoop()
	return s
}

// Get returns the pending order for a user, or nil if none.
// The returned pointer is safe to read; mutate only via Update().
func (s *SessionStore) Get(userID int64) *PendingOrder {
	s.mu.RLock()
	defer s.mu.RUnlock()
	order := s.orders[userID]
	if order == nil || time.Since(order.UpdatedAt) > s.ttl {
		return nil
	}
	return order
}

// Create starts a fresh pending order for a user, replacing any
// existing one. Used when a photo arrives with no active session.
func (s *SessionStore) Create(userID int64) *PendingOrder {
	s.mu.Lock()
	defer s.mu.Unlock()
	order := &PendingOrder{
		UserID:    userID,
		Stage:     stageCollectingPhotos,
		UpdatedAt: time.Now(),
	}
	s.orders[userID] = order
	return order
}

// Update applies a mutation function to the order and refreshes the
// timestamp. Returns the updated order, or nil if the user has none.
func (s *SessionStore) Update(userID int64, mutate func(*PendingOrder)) *PendingOrder {
	s.mu.Lock()
	defer s.mu.Unlock()
	order, ok := s.orders[userID]
	if !ok || time.Since(order.UpdatedAt) > s.ttl {
		return nil
	}
	mutate(order)
	order.UpdatedAt = time.Now()
	return order
}

// Delete drops a user's pending order — called on "Done" (success
// path, after saving to DB) and "Cancel" (user discards the order).
func (s *SessionStore) Delete(userID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.orders, userID)
}

// Close stops the background GC loop. Call from main() on shutdown.
func (s *SessionStore) Close() {
	close(s.stopChan)
}

// gcLoop periodically removes expired sessions. Runs in its own
// goroutine. We sweep every minute, which is plenty for a 10-min TTL.
func (s *SessionStore) gcLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.evictExpired()
		case <-s.stopChan:
			return
		}
	}
}

func (s *SessionStore) evictExpired() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for id, o := range s.orders {
		if now.Sub(o.UpdatedAt) > s.ttl {
			delete(s.orders, id)
		}
	}
}
