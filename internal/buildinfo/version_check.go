package buildinfo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GitHub Release 更新检查：结果缓存 6 小时，?force=1 强制刷新。
// 「关于」页读取本接口显示 current/latest/是否可更新。

var versionCheckURL = "https://api.github.com/repos/dhananjaym182/RouteNest/releases/latest"

const versionCheckTTL = 6 * time.Hour

// HTTPClient 是更新检查使用的出站客户端。main 侧在 http.go 的 init 中注入共享的
// 代理感知 httpClient（与其它出站请求一致，走 cline 出口代理池）；未注入时回退到 http.DefaultClient。
var HTTPClient *http.Client

func outbound() *http.Client {
	if HTTPClient != nil {
		return HTTPClient
	}
	return http.DefaultClient
}

type versionCheckCache struct {
	mu        sync.Mutex
	latest    string
	htmlURL   string
	published string
	err       string
	checkedAt time.Time
}

var versionState = &versionCheckCache{}

type githubReleaseJSON struct {
	TagName     string `json:"tag_name"`
	HTMLURL     string `json:"html_url"`
	PublishedAt string `json:"published_at"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
}

// refresh 拉取最新版本信息并更新缓存；force=false 且缓存新鲜时直接返回。
func (v *versionCheckCache) refresh(force bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !force && !v.checkedAt.IsZero() && time.Since(v.checkedAt) < versionCheckTTL {
		return
	}
	v.checkedAt = time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, versionCheckURL, nil)
	if err != nil {
		v.recordFetchError(err)
		return
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := outbound().Do(req)
	if err != nil {
		v.recordFetchError(err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		v.recordFetchError(errStatus(resp.StatusCode))
		return
	}
	var rel githubReleaseJSON
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		v.recordFetchError(err)
		return
	}
	latest := strings.TrimSpace(rel.TagName)
	releaseVersion, ok := parseSemver(latest)
	if !ok {
		v.recordFetchError(errors.New("version check upstream returned an invalid release version"))
		return
	}
	if rel.Draft || rel.Prerelease || len(releaseVersion.prerelease) != 0 {
		v.recordFetchError(errors.New("version check upstream did not return a stable release"))
		return
	}
	v.err = ""
	v.latest = latest
	v.htmlURL = rel.HTMLURL
	v.published = rel.PublishedAt
}

// 失败也做短缓存语义：保留旧 latest，仅记录错误，避免错误信息覆盖历史结果。
func (v *versionCheckCache) recordFetchError(err error) {
	v.err = err.Error()
}

type statusError int

func errStatus(code int) error { return statusError(code) }

func (e statusError) Error() string { return "version check upstream status " + strconv.Itoa(int(e)) }

type semver struct {
	core       [3]string
	prerelease []string
}

func parseSemver(s string) (semver, bool) {
	var out semver
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if core, build, found := strings.Cut(s, "+"); found {
		if !validSemverIdentifiers(build, false) {
			return semver{}, false
		}
		s = core
	}
	if core, prerelease, found := strings.Cut(s, "-"); found {
		if !validSemverIdentifiers(prerelease, true) {
			return semver{}, false
		}
		out.prerelease = strings.Split(prerelease, ".")
		s = core
	}
	parts := strings.Split(s, ".")
	if len(parts) != len(out.core) {
		return semver{}, false
	}
	for i, part := range parts {
		if !numericSemverIdentifier(part) || (len(part) > 1 && part[0] == '0') {
			return semver{}, false
		}
		out.core[i] = part
	}
	return out, true
}

func validSemverIdentifiers(s string, prerelease bool) bool {
	for _, part := range strings.Split(s, ".") {
		if part == "" {
			return false
		}
		for i := 0; i < len(part); i++ {
			c := part[i]
			if !('0' <= c && c <= '9' || 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z' || c == '-') {
				return false
			}
		}
		if prerelease && len(part) > 1 && part[0] == '0' && numericSemverIdentifier(part) {
			return false
		}
	}
	return true
}

func numericSemverIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func compareSemverNumbers(a, b string) int {
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return strings.Compare(a, b)
}

func versionNewer(current, latest string) bool {
	c, ok := parseSemver(current)
	if !ok {
		return false
	}
	l, ok := parseSemver(latest)
	if !ok {
		return false
	}
	for i := range c.core {
		if cmp := compareSemverNumbers(l.core[i], c.core[i]); cmp != 0 {
			return cmp > 0
		}
	}
	if len(c.prerelease) == 0 {
		return false
	}
	if len(l.prerelease) == 0 {
		return true
	}
	for i := 0; i < len(c.prerelease) && i < len(l.prerelease); i++ {
		currentID, latestID := c.prerelease[i], l.prerelease[i]
		currentNumeric, latestNumeric := numericSemverIdentifier(currentID), numericSemverIdentifier(latestID)
		if currentNumeric != latestNumeric {
			return currentNumeric
		}
		cmp := strings.Compare(latestID, currentID)
		if currentNumeric {
			cmp = compareSemverNumbers(latestID, currentID)
		}
		if cmp != 0 {
			return cmp > 0
		}
	}
	return len(l.prerelease) > len(c.prerelease)
}

// VersionStatus is the cached release check, ready for the admin console.
type VersionStatus struct {
	Current         string
	Latest          string
	ReleaseURL      string
	PublishedAt     string
	CheckedAtMillis int64
	UpdateAvailable bool
	Error           string
}

// CurrentVersionStatus refreshes the cached release check when force is set or
// the cache is stale, and reports it.
func CurrentVersionStatus(force bool) VersionStatus {
	versionState.refresh(force)
	versionState.mu.Lock()
	defer versionState.mu.Unlock()
	return VersionStatus{
		Current:         AppVersion,
		Latest:          versionState.latest,
		ReleaseURL:      versionState.htmlURL,
		PublishedAt:     versionState.published,
		CheckedAtMillis: versionState.checkedAt.UnixMilli(),
		UpdateAvailable: versionState.latest != "" && versionNewer(AppVersion, versionState.latest),
		Error:           versionState.err,
	}
}
