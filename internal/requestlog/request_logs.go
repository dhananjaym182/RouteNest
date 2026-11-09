package requestlog

import (
	"encoding/base64"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"routenest/internal/domain"
	"routenest/internal/platform"
	"routenest/internal/protocol/openaichat"
	"routenest/internal/providers"
	requestlogstore "routenest/internal/store/requestlog"
)

type requestLog = domain.RequestLog

const (
	requestLogMaxEntries   = 5000
	requestLogMaxAge       = 30 * 24 * time.Hour
	requestLogDefaultLimit = 50
	requestLogMaxLimit     = 100
	upstreamOpenCode       = "opencode"
)

var (
	Logs []requestLog
	Mu   sync.Mutex
	Path string
	// RecordSuccessfulGatewayRequest is installed by the application composition
	// root. Keeping persistence behind this hook avoids coupling request logging
	// to the onboarding store and keeps package tests free of filesystem effects.
	RecordSuccessfulGatewayRequest func(time.Time) error
)

// pathMu guards first-use resolution of Path. Tests that assign Path keep it.
var pathMu sync.Mutex

func ensurePath() {
	pathMu.Lock()
	defer pathMu.Unlock()
	if Path == "" {
		Path = platform.ResolveDataPath(requestlogstore.FileName, nil)
	}
}

func Load() {
	ensurePath()
	entries, err := requestlogstore.Load(Path)
	if err != nil {
		platform.BackupCorruptFile(Path, err)
		return
	}
	Mu.Lock()
	Logs = pruneRequestLogsLocked(entries)
	Mu.Unlock()
}

func pruneRequestLogsLocked(entries []requestLog) []requestLog {
	if len(entries) == 0 {
		return entries
	}
	sort.Slice(entries, func(i, j int) bool {
		if !entries[i].StartedAt.Equal(entries[j].StartedAt) {
			return entries[i].StartedAt.After(entries[j].StartedAt)
		}
		return entries[i].ID > entries[j].ID
	})

	cutoff := time.Now().Add(-requestLogMaxAge)
	pruned := entries[:0]
	for _, e := range entries {
		if e.StartedAt.Before(cutoff) {
			continue
		}
		pruned = append(pruned, e)
	}
	if len(pruned) > requestLogMaxEntries {
		pruned = pruned[:requestLogMaxEntries]
	}
	return pruned
}

func saveRequestLogsLocked() {
	ensurePath()
	if err := requestlogstore.Save(Path, Logs); err != nil {
		log.Printf("request logs save failed: %v", err)
	}
}

// POST /admin/api/request-logs/clear — drop all persisted request logs.
// Clear removes every stored request log and persists the empty list.
func Clear() {
	Mu.Lock()
	Logs = nil
	saveRequestLogsLocked()
	Mu.Unlock()
	log.Printf("admin: request logs cleared")
}

func appendRequestLog(entry requestLog) {
	if entry.ID == "" {
		entry.ID = fmt.Sprintf("req_%d", entry.StartedAt.UnixNano())
	}
	Mu.Lock()
	Logs = append(Logs, entry)
	Logs = pruneRequestLogsLocked(Logs)
	saveRequestLogsLocked()
	Mu.Unlock()
}

type requestLogPage struct {
	Items      []requestLog `json:"items"`
	NextCursor string       `json:"nextCursor"`
	HasMore    bool         `json:"hasMore"`
}

func encodeCursor(entry requestLog) string {
	key := fmt.Sprintf("%d|%s", entry.StartedAt.UnixNano(), entry.ID)
	return base64.RawURLEncoding.EncodeToString([]byte(key))
}

func decodeCursor(cursor string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("invalid cursor")
	}
	var ts int64
	var id string
	if _, err := fmt.Sscanf(string(raw), "%d|%s", &ts, &id); err != nil || id == "" {
		return time.Time{}, "", fmt.Errorf("invalid cursor")
	}
	return time.Unix(0, ts), id, nil
}

func List(limit int, cursor string) (requestLogPage, error) {
	if limit <= 0 {
		limit = requestLogDefaultLimit
	}
	if limit > requestLogMaxLimit {
		limit = requestLogMaxLimit
	}

	var afterTime time.Time
	var afterID string
	if cursor != "" {
		t, id, err := decodeCursor(cursor)
		if err != nil {
			return requestLogPage{}, err
		}
		afterTime = t
		afterID = id
	}

	Mu.Lock()
	defer Mu.Unlock()

	result := make([]requestLog, 0, limit)
	var lastEntry requestLog
	for _, e := range Logs {
		if cursor != "" {
			if e.StartedAt.After(afterTime) {
				continue
			}
			if e.StartedAt.Equal(afterTime) && e.ID >= afterID {
				continue
			}
		}
		result = append(result, e)
		lastEntry = e
		if len(result) >= limit {
			break
		}
	}

	page := requestLogPage{Items: result}
	if len(result) == limit {
		page.NextCursor = encodeCursor(lastEntry)
		page.HasMore = true
	}
	return page, nil
}

func Finalize(entry *requestLog, usage openaichat.Usage, firstOutputAt time.Time, startedAt time.Time, completed bool, errMsg string) {
	entry.FinishedAt = time.Now()
	entry.DurationMs = entry.FinishedAt.Sub(startedAt).Milliseconds()
	entry.Completed = completed
	entry.Error = platform.Truncate(errMsg, 200)

	if usage.Valid {
		entry.UsageAvailable = true
		entry.InputTokens = usage.Prompt
		entry.OutputTokens = usage.Completion
		entry.CachedTokens = usage.Cached
		entry.TotalTokens = usage.Total
	}

	if !firstOutputAt.IsZero() && usage.Valid && usage.Completion > 0 {
		entry.TTFTMs = firstOutputAt.Sub(startedAt).Milliseconds()
		generationMs := entry.FinishedAt.Sub(firstOutputAt).Seconds()
		if generationMs > 0 {
			entry.OutputTPS = float64(usage.Completion) / generationMs
		}
	}

	// Provider token accounting uses the durable admission identity and its
	// captured UTC windows. Enforcement reads the provider's configured window;
	// failed requests with reported usage still count because tokens were spent.
	if entry.ProviderID != "" && entry.BudgetAttemptID != "" && entry.TotalTokens > 0 {
		eventID := entry.ID + "/usage/final"
		if entry.ID == "" {
			eventID = fmt.Sprintf("req_%d/usage/final", entry.StartedAt.UnixNano())
		}
		if err := providers.RecordTokenUsage(entry.ProviderID, entry.BudgetAttemptID, eventID, entry.TotalTokens); err != nil {
			log.Printf("provider %s token accounting failed: %v", entry.ProviderID, err)
		}
	}

	if completed && errMsg == "" && RecordSuccessfulGatewayRequest != nil {
		if err := RecordSuccessfulGatewayRequest(entry.FinishedAt); err != nil {
			log.Printf("onboarding gateway request completion could not be persisted: %v", err)
		}
	}

	appendRequestLog(*entry)
}

// OpenCodeUsageToday 从请求日志聚合今日 opencode 上游用量（后台仪表盘卡片用）。
func OpenCodeUsageToday() map[string]any {
	Mu.Lock()
	defer Mu.Unlock()

	var requests int64
	var input, output, total int64
	today := time.Now().Format("2006-01-02")
	for _, e := range Logs {
		if e.Upstream != upstreamOpenCode || e.StartedAt.Format("2006-01-02") != today {
			continue
		}
		requests++
		input += e.InputTokens
		output += e.OutputTokens
		total += e.TotalTokens
	}
	return map[string]any{
		"requests":     requests,
		"inputTokens":  input,
		"outputTokens": output,
		"totalTokens":  total,
	}
}

// Snapshot returns a copy of the in-memory request logs, newest first.
func Snapshot() []domain.RequestLog {
	Mu.Lock()
	defer Mu.Unlock()
	return append([]domain.RequestLog(nil), Logs...)
}
