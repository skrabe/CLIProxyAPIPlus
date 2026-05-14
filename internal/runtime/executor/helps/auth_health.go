package helps

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
)

const codexAuthHealthVersion = 1

var codexAuthHealthMu sync.Mutex

type CodexAuthHealthState struct {
	Status     string `json:"status"`
	Source     string `json:"source"`
	Reason     string `json:"reason"`
	ObservedAt int64  `json:"observed_at"`
	ExpiresAt  int64  `json:"expires_at,omitempty"`
	Provider   string `json:"provider,omitempty"`
	AuthID     string `json:"auth_id,omitempty"`
	Label      string `json:"label,omitempty"`
}

type codexAuthHealthFile struct {
	Version   int                             `json:"version"`
	UpdatedAt int64                           `json:"updated_at"`
	Auths     map[string]CodexAuthHealthState `json:"auths"`
}

func RecordCodexAuthHealthResponse(cfg *config.Config, auth *cliproxyauth.Auth, provider string, statusCode int, headers http.Header, body []byte) {
	if cfg == nil || !cfg.CodexAuthHealth.Enabled || auth == nil {
		return
	}
	name := codexAuthHealthName(auth)
	if name == "" {
		return
	}
	state := classifyCodexAuthHealth(provider, auth, statusCode, headers, body)
	if state.Status == "" {
		return
	}
	writeCodexAuthHealthState(cfg, name, state)
}

func codexAuthHealthName(auth *cliproxyauth.Auth) string {
	if auth == nil {
		return ""
	}
	if fileName := strings.TrimSpace(auth.FileName); fileName != "" {
		return filepath.Base(fileName)
	}
	if auth.Attributes != nil {
		if source := strings.TrimSpace(auth.Attributes["source"]); source != "" {
			return filepath.Base(source)
		}
		if path := strings.TrimSpace(auth.Attributes["path"]); path != "" {
			return filepath.Base(path)
		}
	}
	return strings.TrimSpace(auth.ID)
}

func classifyCodexAuthHealth(provider string, auth *cliproxyauth.Auth, statusCode int, headers http.Header, body []byte) CodexAuthHealthState {
	now := time.Now().Unix()
	state := CodexAuthHealthState{
		Status:     "healthy",
		Source:     "proxy_health",
		Reason:     "http_" + strconv.Itoa(statusCode),
		ObservedAt: now,
		Provider:   strings.TrimSpace(provider),
		AuthID:     strings.TrimSpace(auth.ID),
		Label:      strings.TrimSpace(auth.Label),
	}

	primaryUsed := headerInt(headers, "X-Codex-Primary-Used-Percent")
	primaryReset := headerInt64(headers, "X-Codex-Primary-Reset-At")
	if primaryUsed >= 90 {
		state.Status = "quota"
		state.Reason = "primary_" + strconv.Itoa(primaryUsed) + "pct"
		if primaryReset > now {
			state.ExpiresAt = primaryReset
		}
	}

	lower := strings.ToLower(strings.TrimSpace(string(body)))
	errorType := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "error.type").String()))
	errorCode := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "error.code").String()))
	errorMessage := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "error.message").String()))
	if errorMessage == "" {
		errorMessage = strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "message").String()))
	}
	combined := strings.Join([]string{lower, errorType, errorCode, errorMessage}, " ")

	if statusCode == http.StatusTooManyRequests || containsAny(combined, "usage_limit_reached", "usage limit", "rate limit", "quota", "insufficient_quota", "too many requests") {
		state.Status = "quota"
		state.Reason = firstReason(combined, "usage_limit_reached", "usage limit", "rate limit", "quota", "insufficient_quota", "too many requests", "http_429")
		if reset := codexResetAt(body, now); reset > now {
			state.ExpiresAt = reset
		}
		return state
	}

	if statusCode == http.StatusUnauthorized || statusCode == http.StatusPaymentRequired || statusCode == http.StatusForbidden ||
		containsAny(combined, "token_invalidated", "invalid or expired token", "expired token", "account disabled", "account deactivated", "account banned", "account suspended", "user banned", "user suspended", "subscription expired", "subscription inactive", "subscription cancelled", "no active subscription", "payment required", "billing", "plan expired") {
		state.Status = "dead"
		state.Reason = firstReason(combined, "token_invalidated", "invalid or expired token", "expired token", "account disabled", "account deactivated", "account banned", "account suspended", "user banned", "user suspended", "subscription expired", "subscription inactive", "subscription cancelled", "no active subscription", "payment required", "billing", "plan expired", "http_"+strconv.Itoa(statusCode))
		return state
	}

	return state
}

func headerInt(headers http.Header, name string) int {
	value, _ := strconv.Atoi(strings.TrimSpace(headers.Get(name)))
	return value
}

func headerInt64(headers http.Header, name string) int64 {
	value, _ := strconv.ParseInt(strings.TrimSpace(headers.Get(name)), 10, 64)
	return value
}

func codexResetAt(body []byte, now int64) int64 {
	if resetAt := gjson.GetBytes(body, "error.resets_at").Int(); resetAt > 0 {
		return resetAt
	}
	if resetIn := gjson.GetBytes(body, "error.resets_in_seconds").Int(); resetIn > 0 {
		return now + resetIn
	}
	return 0
}

func containsAny(text string, markers ...string) bool {
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func firstReason(text string, markers ...string) string {
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			return strings.ReplaceAll(marker, " ", "_")
		}
	}
	if len(markers) > 0 {
		return markers[len(markers)-1]
	}
	return "unknown"
}

func writeCodexAuthHealthState(cfg *config.Config, name string, state CodexAuthHealthState) {
	path := codexAuthHealthPath(cfg)
	if path == "" {
		return
	}
	codexAuthHealthMu.Lock()
	defer codexAuthHealthMu.Unlock()

	data := codexAuthHealthFile{Version: codexAuthHealthVersion, Auths: map[string]CodexAuthHealthState{}}
	if raw, errRead := os.ReadFile(path); errRead == nil {
		_ = json.Unmarshal(raw, &data)
	}
	if data.Auths == nil {
		data.Auths = map[string]CodexAuthHealthState{}
	}
	data.Version = codexAuthHealthVersion
	data.UpdatedAt = time.Now().Unix()
	data.Auths[name] = state

	// Drop entries whose auth file no longer exists. The current observation
	// (name) is preserved unconditionally — it's the freshest signal.
	authDir := codexAuthHealthAuthDir(cfg)
	if authDir != "" {
		for k := range data.Auths {
			if k == name {
				continue
			}
			if _, errStat := os.Stat(filepath.Join(authDir, k)); os.IsNotExist(errStat) {
				delete(data.Auths, k)
			}
		}
	}

	raw, errMarshal := json.MarshalIndent(data, "", "  ")
	if errMarshal != nil {
		log.WithError(errMarshal).Debug("codex auth health: marshal failed")
		return
	}
	if errMkdir := os.MkdirAll(filepath.Dir(path), 0o755); errMkdir != nil {
		log.WithError(errMkdir).Debug("codex auth health: mkdir failed")
		return
	}
	tmp := path + ".tmp"
	if errWrite := os.WriteFile(tmp, raw, 0o600); errWrite != nil {
		log.WithError(errWrite).Debug("codex auth health: write failed")
		return
	}
	if errRename := os.Rename(tmp, path); errRename != nil {
		_ = os.Remove(tmp)
		log.WithError(errRename).Debug("codex auth health: rename failed")
	}
}

func codexAuthHealthPath(cfg *config.Config) string {
	path := strings.TrimSpace(cfg.CodexAuthHealth.Path)
	if path == "" {
		path = filepath.Join(codexAuthHealthAuthDir(cfg), "codex-auth-health.json")
	}
	if strings.HasPrefix(path, "~/") {
		if home, errHome := os.UserHomeDir(); errHome == nil {
			path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return filepath.Clean(path)
}

func codexAuthHealthAuthDir(cfg *config.Config) string {
	authDir := strings.TrimSpace(cfg.AuthDir)
	if authDir == "" {
		authDir = config.DefaultAuthDir
	}
	if strings.HasPrefix(authDir, "~/") {
		if home, errHome := os.UserHomeDir(); errHome == nil {
			authDir = filepath.Join(home, strings.TrimPrefix(authDir, "~/"))
		}
	}
	return filepath.Clean(authDir)
}
