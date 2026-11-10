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
	return t.allow(source)
}

func (t *Throttler) allow(sources ...string) (bool, time.Duration) {
	keys := make(map[string]bucket, len(sources))
	for _, source := range sources {
		if len(source) > 256 {
			return false, time.Minute
		}
		keys[source] = bucket{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	t.global = replenish(t.global, now, 20, 2)
	missing := 0
	for source := range keys {
		if _, exists := t.sources[source]; !exists {
			missing++
		}
	}
	if len(t.sources)+missing > t.maxSources {
		for source, value := range t.sources {
			if _, needed := keys[source]; !needed && now.Sub(value.last) > 5*time.Minute {
				delete(t.sources, source)
			}
		}
	}
	if len(t.sources)+missing > t.maxSources {
		return false, time.Minute
	}
	retry := time.Duration(0)
	if t.global.tokens < 1 {
		retry = time.Duration(math.Ceil((1-t.global.tokens)/2)) * time.Second
	}
	for source := range keys {
		b, exists := t.sources[source]
		if !exists {
			b = bucket{t.capacity, now}
		}
		b = replenish(b, now, t.capacity, t.rate)
		if b.tokens < 1 {
			v := time.Duration(math.Ceil((1-b.tokens)/t.rate)) * time.Second
			if v > retry {
				retry = v
			}
		}
		keys[source] = b
		t.sources[source] = b
	}
	if retry > 0 {
		return false, retry
	}
	t.global.tokens--
	for source, b := range keys {
		b.tokens--
		t.sources[source] = b
	}
	return true, 0
}

type RateLimitError struct{ RetryAfter time.Duration }

func (e *RateLimitError) Error() string        { return ErrRateLimited.Error() }
func (e *RateLimitError) Is(target error) bool { return target == ErrRateLimited }
