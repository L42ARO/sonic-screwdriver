package jobs

import (
	"sync"
	"time"
)

// Event is one entry in a job's stream: queued, started, step, succeeded, failed, cancelled.
type Event struct {
	Name string
	Data any
}

// stream holds a job's events so late subscribers can catch up, and fans new ones out.
type stream struct {
	mu      sync.Mutex
	history []Event
	subs    map[chan Event]struct{}
	done    bool
}

func newStream() *stream { return &stream{subs: map[chan Event]struct{}{}} }

func (s *stream) publish(e Event, final bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history = append(s.history, e)
	for ch := range s.subs {
		select {
		case ch <- e:
		default: // a subscriber this far behind is dropped rather than stalling the job
			close(ch)
			delete(s.subs, ch)
		}
	}
	if final {
		s.done = true
		for ch := range s.subs {
			close(ch)
		}
		s.subs = map[chan Event]struct{}{}
	}
}

// subscribe returns past events and a channel of future ones (closed when the job ends).
func (s *stream) subscribe() ([]Event, chan Event, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	history := append([]Event(nil), s.history...)
	ch := make(chan Event, 256)
	if s.done {
		close(ch)
		return history, ch, func() {}
	}
	s.subs[ch] = struct{}{}
	return history, ch, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := s.subs[ch]; ok {
			delete(s.subs, ch)
			close(ch)
		}
	}
}

// streams keeps live streams, and finished ones for a while so retries can reattach.
type streams struct {
	mu   sync.Mutex
	byID map[string]*stream
}

func (ss *streams) get(id string) *stream {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return ss.byID[id]
}

func (ss *streams) create(id string) *stream {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	s := newStream()
	ss.byID[id] = s
	return s
}

func (ss *streams) forgetLater(id string) {
	time.AfterFunc(30*time.Minute, func() {
		ss.mu.Lock()
		delete(ss.byID, id)
		ss.mu.Unlock()
	})
}
