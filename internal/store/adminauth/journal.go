package adminauth

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	auth "routenest/internal/adminauth"
)

type record struct {
	Sequence         uint64           `json:"sequence"`
	Type             string           `json:"type"`
	PreviousHash     string           `json:"previousHash"`
	PreviousRevision uint64           `json:"previousRevision"`
	NextRevision     uint64           `json:"nextRevision"`
	PrepareHash      string           `json:"prepareHash,omitempty"`
	Event            *auth.AuditEvent `json:"event,omitempty"`
	State            *auth.State      `json:"state,omitempty"`
	StateHash        string           `json:"stateHash"`
	Hash             string           `json:"hash"`
}

func (r record) digest() string {
	r.Hash = ""
	b, _ := json.Marshal(r)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (r *record) seal() { r.Hash = r.digest() }
func (s *Store) appendRecord(r record) error {
	b, e := json.Marshal(r)
	if e != nil {
		return e
	}
	if len(b) > 16<<20 {
		return fmt.Errorf("journal record exceeds limit")
	}
	b = append(b, '\n')
	if _, e = s.journal.Seek(0, io.SeekEnd); e != nil {
		return e
	}
	n, e := s.journal.Write(b)
	if e != nil {
		return e
	}
	if n != len(b) {
		return io.ErrShortWrite
	}
	return nil
}
func (s *Store) readRecords() ([]record, error) {
	if e := checkSecurePath(filepath.Join(s.dir, JournalName), false); e != nil {
		return nil, e
	}
	f, e := os.Open(filepath.Join(s.dir, JournalName))
	if e != nil {
		return nil, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if info.Size() > 512<<20 {
		return nil, fmt.Errorf("%w: journal limit", ErrCorrupt)
	}
	reader := bufio.NewReader(f)
	var records []record
	head := ""
	revision := uint64(0)
	var pending *record
	seen := map[string]bool{}
	for {
		line, e := reader.ReadBytes('\n')
		if e == io.EOF && len(line) == 0 {
			break
		}
		if e != nil {
			return nil, fmt.Errorf("%w: truncated journal record", ErrCorrupt)
		}
		if len(line) > 16<<20 {
			return nil, fmt.Errorf("%w: oversized journal record", ErrCorrupt)
		}
		if len(bytes.TrimSpace(line)) == 0 {
			return nil, fmt.Errorf("%w: empty journal record", ErrCorrupt)
		}
		var r record
		if e = strictJSON(line, &r); e != nil {
			return nil, fmt.Errorf("%w: journal encoding", ErrCorrupt)
		}
		if r.Sequence != uint64(len(records))+1 || r.PreviousHash != head || r.Hash != r.digest() {
			return nil, fmt.Errorf("%w: journal chain", ErrCorrupt)
		}
		switch r.Type {
		case "prepare":
			if pending != nil || r.PreviousRevision != revision || r.NextRevision != revision+1 || r.State == nil || r.Event == nil || r.PrepareHash != "" || r.State.Revision != r.NextRevision || r.StateHash != stateHash(*r.State) || auth.ValidateState(*r.State) != nil || validateEvent(*r.Event) != nil || seen[r.Event.ID] {
				return nil, fmt.Errorf("%w: prepare record", ErrCorrupt)
			}
			if revision > 0 && records[len(records)-2].State.Initialized && !r.State.Initialized {
				return nil, fmt.Errorf("%w: initialized downgrade", ErrCorrupt)
			}
			seen[r.Event.ID] = true
			pending = &r
		case "commit":
			if pending == nil || r.State != nil || r.Event != nil || r.PrepareHash != pending.Hash || r.PreviousRevision != pending.PreviousRevision || r.NextRevision != pending.NextRevision || r.StateHash != pending.StateHash {
				return nil, fmt.Errorf("%w: commit record", ErrCorrupt)
			}
			revision = r.NextRevision
			pending = nil
		default:
			return nil, fmt.Errorf("%w: unknown journal record", ErrCorrupt)
		}
		records = append(records, r)
		head = r.Hash
	}
	return records, nil
}
func (s *Store) recover() error {
	disk, present, e := s.readSnapshot()
	if e != nil {
		return e
	}
	records, e := s.readRecords()
	if e != nil {
		return e
	}
	committed := auth.EmptyState()
	var pending *record
	for i := range records {
		r := records[i]
		if r.Type == "prepare" {
			pending = &records[i]
		} else {
			committed = auth.CloneState(*pending.State)
			s.events = append(s.events, *pending.Event)
			pending = nil
		}
	}
	if pending == nil {
		if len(records) == 0 {
			if present && (disk.Revision != 0 || disk.StateHash != stateHash(committed)) {
				return fmt.Errorf("%w: snapshot without journal", ErrCorrupt)
			}
		} else if !present || disk.Revision != committed.Revision || disk.StateHash != stateHash(committed) {
			return fmt.Errorf("%w: snapshot differs from committed journal", ErrCorrupt)
		}
	} else {
		if present && !(disk.Revision == committed.Revision && disk.StateHash == stateHash(committed)) && !(disk.Revision == pending.NextRevision && disk.StateHash == pending.StateHash) {
			return fmt.Errorf("%w: snapshot differs from prepared state", ErrCorrupt)
		}
		if !present && committed.Revision != 0 {
			return fmt.Errorf("%w: missing committed snapshot", ErrCorrupt)
		}
		if !present || disk.Revision != pending.NextRevision {
			if e = s.writeSnapshot(*pending.State, false); e != nil {
				return e
			}
		}
		commit := record{Sequence: pending.Sequence + 1, Type: "commit", PreviousHash: pending.Hash, PreviousRevision: pending.PreviousRevision, NextRevision: pending.NextRevision, PrepareHash: pending.Hash, StateHash: pending.StateHash}
		commit.seal()
		if e = s.appendRecord(commit); e != nil {
			return e
		}
		if e = s.journal.Sync(); e != nil {
			return e
		}
		records = append(records, commit)
		committed = auth.CloneState(*pending.State)
		s.events = append(s.events, *pending.Event)
	}
	s.state = committed
	s.sequence = uint64(len(records))
	if len(records) > 0 {
		s.head = records[len(records)-1].Hash
	}
	return nil
}
