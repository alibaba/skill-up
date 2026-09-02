package evalevent

import (
	"context"
	"sync"
)

type recordingSink struct {
	mu sync.Mutex

	events     []Event
	failAt     int
	publishErr error
	closeErr   error
	closeCount int
	notify     chan struct{}
}

func newRecordingSink() *recordingSink {
	return &recordingSink{notify: make(chan struct{}, 256)}
}

func (s *recordingSink) Publish(_ context.Context, event Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failAt > 0 && len(s.events)+1 == s.failAt {
		return s.publishErr
	}
	s.events = append(s.events, event)
	select {
	case s.notify <- struct{}{}:
	default:
	}
	return nil
}

func (s *recordingSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeCount++
	return s.closeErr
}

func (s *recordingSink) snapshot() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Event(nil), s.events...)
}

func (s *recordingSink) closes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeCount
}
