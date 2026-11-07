package adminauth

import (
	"math"
	"sync"
	"time"
)

type bucket struct {
	tokens float64
	last   time.Time
}
type Throttler struct {
	mu         sync.Mutex
	global     bucket
	sources    map[string]bucket
	capacity   float64
	rate       float64
	maxSources int
	now        func() time.Time
}

func NewThrottler() *Throttler {
	now := time.Now()
	return &Throttler{global: bucket{20, now}, sources: map[string]bucket{}, capacity: 5, rate: 1.0 / 12, maxSources: 4096, now: time.Now}
}
func replenish(b bucket, now time.Time, capacity, rate float64) bucket {
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens = math.Min(capacity, b.tokens+elapsed*rate)
	}
	b.last = now
	return b
}

// Allow bounds both total KDF work and source/account attempts. Entries are not
// evicted while depleted: attackers cannot reset a bucket by filling the map.
func (t *Throttler) Allow(source string) (bool, time.Duration) {
	if len(source) > 256 {
		return false, time.Minute
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	t.global = replenish(t.global, now, 20, 2)
	b, ok := t.sources[source]
	if !ok {
		if len(t.sources) >= t.maxSources {
			for k, v := range t.sources {
				if now.Sub(v.last) > time.Minute*5 {
					delete(t.sources, k)
				}
			}
		}
		if len(t.sources) >= t.maxSources {
			return false, time.Minute
		}
		b = bucket{t.capacity, now}
	}
	b = replenish(b, now, t.capacity, t.rate)
	retry := time.Duration(0)
	if t.global.tokens < 1 {
		retry = time.Duration(math.Ceil((1-t.global.tokens)/2)) * time.Second
	}
	if b.tokens < 1 {
		v := time.Duration(math.Ceil((1-b.tokens)/t.rate)) * time.Second
		if v > retry {
			retry = v
		}
	}
	t.sources[source] = b
	if retry > 0 {
		return false, retry
	}
	t.global.tokens--
	b.tokens--
	t.sources[source] = b
	return true, 0
}

type RateLimitError struct{ RetryAfter time.Duration }

func (e *RateLimitError) Error() string        { return ErrRateLimited.Error() }
func (e *RateLimitError) Is(target error) bool { return target == ErrRateLimited }
