package admin

import (
	"context"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/cesarpetrescu/ledger/internal/store"
	"github.com/coder/websocket"
)

const heartbeatEvent = `{"type":"heartbeat"}`
const changeEvent = `{"type":"change","entity":"*"}`

type eventStream struct {
	db          *store.DB
	mu          sync.Mutex
	subscribers map[chan struct{}]struct{}
}

func newEventStream(db *store.DB) *eventStream {
	return &eventStream{db: db, subscribers: map[chan struct{}]struct{}{}}
}

func (s *eventStream) subscribe() (<-chan struct{}, func()) {
	updates := make(chan struct{}, 1)
	s.mu.Lock()
	s.subscribers[updates] = struct{}{}
	s.mu.Unlock()
	return updates, func() {
		s.mu.Lock()
		delete(s.subscribers, updates)
		s.mu.Unlock()
	}
}

func (s *eventStream) broadcast() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for subscriber := range s.subscribers {
		select {
		case subscriber <- struct{}{}:
		default:
			// ponytail: one generic invalidation covers every mounted resource.
		}
	}
}

func (s *eventStream) run(ctx context.Context) error {
	backoff := 250 * time.Millisecond
	for {
		err := s.listen(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		log.Printf("admin: event listener disconnected: %v; reconnecting", err)
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		backoff = min(backoff*2, 5*time.Second)
	}
}

func (s *eventStream) listen(ctx context.Context) error {
	if s.db == nil {
		return errors.New("database is unavailable")
	}
	conn, err := s.db.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `LISTEN ledger_admin_event`); err != nil {
		return err
	}
	for {
		_, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return err
		}
		s.broadcast()
	}
}

func (s *eventStream) serve(origin string, w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != origin {
		writeError(w, http.StatusForbidden, "origin not allowed")
		return
	}
	// Origin is checked above against the configured public URL, including scheme and port.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{origin}})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	ctx := conn.CloseRead(r.Context())
	updates, unsubscribe := s.subscribe()
	defer unsubscribe()
	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()

	for {
		payload := heartbeatEvent
		select {
		case <-ctx.Done():
			return
		case <-updates:
			payload = changeEvent
		case <-heartbeat.C:
		}
		writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := conn.Write(writeCtx, websocket.MessageText, []byte(payload))
		cancel()
		if err != nil {
			return
		}
	}
}
