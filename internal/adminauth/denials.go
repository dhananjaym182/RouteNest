package adminauth

import (
	"context"
	"net"
	"sync"
	"time"
)

// DenialBuffer bounds attacker-controlled denial audit work. Requests enqueue in
// memory; the host periodically flushes at most one event per minute. Denials
// continue to deny even if the audit sink is unavailable.
type DenialSummary struct {
	Kind   AuditKind
	Source string
	Count  uint64
}
type denialBuffer struct {
	mu        sync.Mutex
	entries   map[string]DenialSummary
	lastFlush time.Time
}

func newDenialBuffer() *denialBuffer { return &denialBuffer{entries: map[string]DenialSummary{}} }
func (s *Service) RecordDenial(kind AuditKind, source string) {
	switch kind {
	case AuditLoginDenied, AuditRecoveryCodeDenied, AuditTrustedDeviceDenied, AuditAccessDenied, AuditCSRFDenied:
	default:
		return
	}
	if ip := net.ParseIP(source); ip != nil {
		source = ip.String()
	} else {
		source = ""
	}
	key := string(kind) + ":" + source
	s.denials.mu.Lock()
	defer s.denials.mu.Unlock()
	v, ok := s.denials.entries[key]
	if !ok && len(s.denials.entries) >= 128 {
		key = string(kind) + ":other"
		source = ""
		v = s.denials.entries[key]
	}
	v.Kind = kind
	v.Source = source
	v.Count++
	s.denials.entries[key] = v
}
func (s *Service) Denials() []DenialSummary {
	s.denials.mu.Lock()
	defer s.denials.mu.Unlock()
	out := make([]DenialSummary, 0, len(s.denials.entries))
	for _, v := range s.denials.entries {
		out = append(out, v)
	}
	return out
}
func (s *Service) FlushDenials(ctx context.Context) error {
	if e := s.check(); e != nil {
		return e
	}
	s.denials.mu.Lock()
	if s.now().Sub(s.denials.lastFlush) < time.Minute {
		s.denials.mu.Unlock()
		return nil
	}
	var key string
	var v DenialSummary
	for k, entry := range s.denials.entries {
		key = k
		v = entry
		break
	}
	if key == "" {
		s.denials.mu.Unlock()
		return nil
	}
	s.denials.lastFlush = s.now()
	delete(s.denials.entries, key)
	s.denials.mu.Unlock()
	e := s.applyOutcome(ctx, v.Kind, "", "", v.Source, "denied", func(*State) error { return nil })
	if e != nil {
		s.denials.mu.Lock()
		old := s.denials.entries[key]
		v.Count += old.Count
		s.denials.entries[key] = v
		s.denials.mu.Unlock()
	}
	return e
}
