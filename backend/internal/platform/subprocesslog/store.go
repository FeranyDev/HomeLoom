package subprocesslog

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/feranydev/homeloom/backend/internal/platform/safelog"
)

const DefaultCapacity = 2000

// SubscriberBuffer bounds a single consumer's pending live log entries. A
// slow browser must never be able to delay a process that is writing logs.
const SubscriberBuffer = 256

type Entry struct {
	Sequence  uint64    `json:"sequence"`
	Time      time.Time `json:"time"`
	Process   string    `json:"process"`
	Instance  string    `json:"instance"`
	Component string    `json:"component,omitempty"`
	Module    string    `json:"module,omitempty"`
	Facility  string    `json:"facility,omitempty"`
	Subsystem string    `json:"subsystem,omitempty"`
	Level     string    `json:"level,omitempty"`
	Message   string    `json:"message"`
	Error     string    `json:"error,omitempty"`
}

type Store struct {
	mu          sync.RWMutex
	capacity    int
	next        uint64
	entries     []Entry
	now         func() time.Time
	subscribers map[uint64]chan Entry
	nextSubID   uint64
}

func New(capacity int) *Store {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	return &Store{capacity: capacity, entries: make([]Entry, 0, capacity), now: time.Now, subscribers: make(map[uint64]chan Entry)}
}

func (s *Store) Writer(process, instance string) io.Writer {
	return &lineWriter{store: s, process: process, instance: instance}
}

func (s *Store) Append(process, instance string, payload []byte) {
	message := strings.TrimSpace(safelog.RedactText(string(payload)))
	if message == "" {
		return
	}
	entry := Entry{Time: s.now().UTC(), Process: process, Instance: instance, Message: message}
	var structured struct {
		Time      any    `json:"time"`
		Level     string `json:"level"`
		Msg       string `json:"msg"`
		Message   string `json:"message"`
		Component string `json:"component"`
		Module    string `json:"module"`
		Facility  string `json:"facility"`
		Error     string `json:"error"`
	}
	if json.Unmarshal([]byte(message), &structured) == nil {
		entry.Level = strings.ToLower(structured.Level)
		entry.Component = structured.Component
		entry.Module = strings.ToLower(structured.Module)
		entry.Facility = structured.Facility
		entry.Error = safelog.RedactText(structured.Error)
		structuredMessage := structured.Msg
		if structuredMessage == "" {
			structuredMessage = structured.Message
		}
		if structuredMessage != "" {
			entry.Message = safelog.RedactText(structuredMessage)
		}
	}
	entry.Subsystem = logSubsystem(entry.Process, entry.Module, entry.Message)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	entry.Sequence = s.next
	if len(s.entries) == s.capacity {
		copy(s.entries, s.entries[1:])
		s.entries[len(s.entries)-1] = entry
	} else {
		s.entries = append(s.entries, entry)
	}
	for _, subscriber := range s.subscribers {
		select {
		case subscriber <- entry:
		default:
			// Live delivery is deliberately lossy for a slow client. The
			// bounded snapshot remains available for cursor-based recovery.
		}
	}
}

func logSubsystem(process, module, message string) string {
	if module != "" {
		return module
	}
	for _, subsystem := range []string{"ffmpeg", "homekit"} {
		if strings.HasPrefix(strings.ToLower(message), "["+subsystem+"]") {
			return subsystem
		}
	}
	return process
}

func (s *Store) Snapshot(after uint64, limit int) []Entry {
	if limit <= 0 || limit > s.capacity {
		limit = s.capacity
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	start := 0
	for start < len(s.entries) && s.entries[start].Sequence <= after {
		start++
	}
	if remaining := len(s.entries) - start; remaining > limit {
		start = len(s.entries) - limit
	}
	result := make([]Entry, len(s.entries)-start)
	copy(result, s.entries[start:])
	return result
}

// Subscribe returns a bounded stream of new entries and an idempotent
// unsubscriber. Consumers must use Snapshot with their latest sequence after
// reconnecting, because a full subscriber buffer intentionally drops entries.
func (s *Store) Subscribe() (<-chan Entry, func()) {
	s.mu.Lock()
	s.nextSubID++
	id := s.nextSubID
	channel := make(chan Entry, SubscriberBuffer)
	s.subscribers[id] = channel
	s.mu.Unlock()

	var once sync.Once
	return channel, func() {
		once.Do(func() {
			s.mu.Lock()
			if current, exists := s.subscribers[id]; exists {
				delete(s.subscribers, id)
				close(current)
			}
			s.mu.Unlock()
		})
	}
}

type lineWriter struct {
	mu       sync.Mutex
	store    *Store
	process  string
	instance string
	pending  []byte
}

func (w *lineWriter) Write(payload []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = append(w.pending, payload...)
	for {
		index := bytes.IndexByte(w.pending, '\n')
		if index < 0 {
			break
		}
		w.store.Append(w.process, w.instance, w.pending[:index])
		w.pending = w.pending[index+1:]
	}
	// Prevent a child without newlines from retaining unbounded memory.
	if len(w.pending) > 64<<10 {
		w.store.Append(w.process, w.instance, w.pending[:64<<10])
		w.pending = w.pending[64<<10:]
	}
	return len(payload), nil
}
