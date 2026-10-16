package auth

import (
	"reflect"
	"strings"
	"sync"
	"time"
)

type authPersistLock struct {
	mu         sync.Mutex
	generation uint64
}

func cloneMetadataValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(v))
		for key, item := range v {
			result[key] = cloneMetadataValue(item)
		}
		return result
	case []any:
		result := make([]any, len(v))
		for i, item := range v {
			result[i] = cloneMetadataValue(item)
		}
		return result
	case []string:
		return append([]string(nil), v...)
	case map[string]string:
		result := make(map[string]string, len(v))
		for key, item := range v {
			result[key] = item
		}
		return result
	default:
		return value
	}
}

func CredentialsChanged(base, current *Auth) bool {
	if base == nil || current == nil {
		return base != current
	}
	for key, value := range base.Metadata {
		if isCredentialKey(key) && !reflect.DeepEqual(value, current.Metadata[key]) {
			return true
		}
	}
	for key, value := range current.Metadata {
		if isCredentialKey(key) && !reflect.DeepEqual(value, base.Metadata[key]) {
			return true
		}
	}
	for _, key := range []string{"api_key", "token", "cookie"} {
		if base.Attributes[key] != current.Attributes[key] {
			return true
		}
	}
	return false
}

func isCredentialKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "access_token", "refresh_token", "id_token", "api_key", "token", "cookie", "cookies", "service_account":
		return true
	default:
		return false
	}
}

func (m *Manager) beginExecution(auth *Auth) *Auth {
	result := auth.Clone()
	result.executionSeq = m.executionSeq.Add(1)
	return result
}

func modelCooldownActive(state *ModelState, now time.Time) bool {
	return state != nil && (state.NextRetryAfter.After(now) || state.Quota.NextRecoverAt.After(now) || (state.Quota.Exceeded && state.Quota.NextRecoverAt.IsZero()))
}

func authCooldownActive(auth *Auth, now time.Time) bool {
	return auth != nil && (auth.NextRetryAfter.After(now) || auth.Quota.NextRecoverAt.After(now) || (auth.Quota.Exceeded && auth.Quota.NextRecoverAt.IsZero()))
}

func (m *Manager) syncAuthScheduler(snapshot *Auth) {
	if snapshot == nil || m.scheduler == nil {
		return
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if current := m.auths[snapshot.ID]; current != nil && current.Generation == snapshot.Generation {
		m.scheduler.upsertAuth(snapshot)
	}
}
