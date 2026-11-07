package adminapi

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"sync"
	"time"
)

type ListenerOperation struct {
	ID          string    `json:"id"`
	Host        string    `json:"host"`
	Status      string    `json:"status"`
	Error       string    `json:"error,omitempty"`
	StartedAt   time.Time `json:"startedAt"`
	CompletedAt time.Time `json:"completedAt,omitempty"`
}

type ListenerOperationStore struct {
	mu  sync.Mutex
	ops map[string]ListenerOperation
	now func() time.Time
}

func NewListenerOperationStore() *ListenerOperationStore {
	return &ListenerOperationStore{ops: make(map[string]ListenerOperation), now: time.Now}
}

func (s *ListenerOperationStore) Start(host string) (ListenerOperation, error) {
	if s == nil {
		return ListenerOperation{}, fmt.Errorf("listener operation store unavailable")
	}
	random := make([]byte, 18)
	if _, err := rand.Read(random); err != nil {
		return ListenerOperation{}, fmt.Errorf("generate listener operation ID: %w", err)
	}
	operation := ListenerOperation{
		ID:        base64.RawURLEncoding.EncodeToString(random),
		Host:      host,
		Status:    "pending",
		StartedAt: s.now().UTC(),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	if len(s.ops) >= 128 {
		return ListenerOperation{}, fmt.Errorf("too many listener operations")
	}
	s.ops[operation.ID] = operation
	return operation, nil
}

func (s *ListenerOperationStore) Succeed(id string) {
	s.complete(id, "succeeded", "")
}

func (s *ListenerOperationStore) Fail(id string, err error) {
	message := "listener change failed"
	if err != nil {
		message = err.Error()
	}
	s.complete(id, "failed", message)
}

func (s *ListenerOperationStore) complete(id, status, message string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	operation, ok := s.ops[id]
	if !ok || operation.Status != "pending" {
		return
	}
	operation.Status = status
	operation.Error = message
	operation.CompletedAt = s.now().UTC()
	s.ops[id] = operation
}

func (s *ListenerOperationStore) Get(id string) (ListenerOperation, bool) {
	if s == nil {
		return ListenerOperation{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	operation, ok := s.ops[id]
	return operation, ok
}

func (s *ListenerOperationStore) pruneLocked() {
	cutoff := s.now().Add(-time.Hour)
	for id, operation := range s.ops {
		if operation.Status != "pending" && operation.CompletedAt.Before(cutoff) {
			delete(s.ops, id)
		}
	}
}
