package auth

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

type reliabilityRefreshExecutor struct {
	schedulerProviderTestExecutor
	started chan struct{}
	release chan struct{}
}

func (e *reliabilityRefreshExecutor) Refresh(_ context.Context, auth *Auth) (*Auth, error) {
	close(e.started)
	<-e.release
	auth.Metadata["access_token"] = "refreshed-test-token"
	auth.Metadata["nested"].(map[string]any)["value"] = "refreshed"
	return auth, nil
}

func TestRefreshPreservesConcurrentOperatorChanges(t *testing.T) {
	for _, operation := range []string{"disable", "edit", "credentials", "register", "remove"} {
		t.Run(operation, func(t *testing.T) {
			ctx := context.Background()
			store := &requestPrepareStore{}
			m := NewManager(store, &RoundRobinSelector{}, nil)
			exec := &reliabilityRefreshExecutor{schedulerProviderTestExecutor: schedulerProviderTestExecutor{provider: "claude"}, started: make(chan struct{}), release: make(chan struct{})}
			m.RegisterExecutor(exec)
			_, err := m.Register(ctx, &Auth{ID: "reliability", Provider: "claude", Status: StatusActive, Metadata: map[string]any{"access_token": "original-test-token", "refresh_token": "test-refresh", "note": "original", "nested": map[string]any{"value": "original"}}})
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() { defer close(done); m.refreshAuth(ctx, "reliability") }()
			<-exec.started
			current, _ := m.GetByID("reliability")
			if current.Metadata["nested"].(map[string]any)["value"] != "original" {
				t.Fatal("refresh mutated stored metadata")
			}
			switch operation {
			case "disable":
				current.Disabled = true
				current.Status = StatusDisabled
				current.Metadata["disabled"] = true
			case "edit":
				current.ProxyURL = "http://operator-proxy"
				current.Prefix = "personal"
				current.Metadata["note"] = "operator"
				current.Metadata["nested"].(map[string]any)["value"] = "operator"
			case "credentials", "register":
				current.Metadata["access_token"] = "operator-test-token"
			}
			if operation == "remove" {
				m.Remove(ctx, current.ID)
			} else if operation == "register" {
				_, err = m.Register(ctx, current)
			} else {
				_, err = m.Update(ctx, current)
			}
			if err != nil {
				t.Fatal(err)
			}
			close(exec.release)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("refresh did not finish")
			}
			current, exists := m.GetByID("reliability")
			if operation == "remove" {
				if exists {
					t.Fatal("refresh resurrected removed credential")
				}
				return
			}
			if !exists {
				t.Fatal("auth missing")
			}
			if operation == "credentials" || operation == "register" {
				if current.Metadata["access_token"] != "operator-test-token" {
					t.Fatal("refresh overwrote replacement credentials")
				}
			} else if current.Metadata["access_token"] != "refreshed-test-token" {
				t.Fatal("fresh token lost")
			}
			if operation == "disable" && (!current.Disabled || current.Status != StatusDisabled) {
				t.Fatal("refresh re-enabled credential")
			}
			if operation == "edit" && (current.Metadata["note"] != "operator" || current.Prefix != "personal" || current.ProxyURL != "http://operator-proxy" || current.Metadata["nested"].(map[string]any)["value"] != "operator") {
				t.Fatal("concurrent operator edit lost")
			}
			persisted := store.lastAuth()
			if persisted.Metadata["access_token"] != current.Metadata["access_token"] || persisted.Disabled != current.Disabled {
				t.Fatal("persisted state differs from latest runtime")
			}
		})
	}
}

func TestOlderStreamSuccessPreservesNewCooldown(t *testing.T) {
	for _, failureModel := range []string{"model-a", ""} {
		t.Run("failure-"+failureModel, func(t *testing.T) {
			ctx := context.Background()
			m := NewManager(nil, &RoundRobinSelector{}, nil)
			registered, err := m.Register(ctx, &Auth{ID: "cooldown", Provider: "claude", Status: StatusActive})
			if err != nil {
				t.Fatal(err)
			}
			old := m.beginExecution(registered)
			chunks := make(chan cliproxyexecutor.StreamChunk)
			stream := m.wrapStreamResult(ctx, old, "claude", "model-a", nil, nil, chunks)
			newer := m.beginExecution(registered)
			delay := time.Hour
			m.MarkResult(ctx, Result{AuthID: registered.ID, Model: failureModel, Success: false, Error: &Error{HTTPStatus: http.StatusTooManyRequests, Message: "quota"}, RetryAfter: &delay, ExecutionSeq: newer.executionSeq, RegistrationEpoch: newer.RegistrationEpoch, CredentialVersion: newer.CredentialVersion})
			close(chunks)
			for range stream.Chunks {
			}
			current, _ := m.GetByID(registered.ID)
			if failureModel != "" {
				if !modelCooldownActive(current.ModelStates[failureModel], time.Now()) {
					t.Fatal("old stream success cleared model cooldown")
				}
			} else if !authCooldownActive(current, time.Now()) {
				t.Fatal("old model success cleared auth cooldown")
			}
			if current.Success != 1 || current.Failed != 1 {
				t.Fatalf("request counters lost: success=%d failed=%d", current.Success, current.Failed)
			}
		})
	}
}

func TestPreparedAuthPreservesConcurrentFieldsAndRejectsNewCredentials(t *testing.T) {
	ctx := context.Background()
	m := NewManager(nil, &RoundRobinSelector{}, nil)
	base, _ := m.Register(ctx, &Auth{ID: "prepare", Provider: "claude", Metadata: map[string]any{"access_token": "first", "note": "old"}})
	updated := base.Clone()
	updated.Metadata["project_id"] = "prepared"
	current, _ := m.GetByID(base.ID)
	current.Metadata["note"] = "operator"
	current.Disabled = true
	current.Status = StatusDisabled
	_, _ = m.Update(ctx, current)
	merged, err := m.UpdatePreparedAuth(ctx, base, updated)
	if err != nil {
		t.Fatal(err)
	}
	if merged.Metadata["project_id"] != "prepared" || merged.Metadata["note"] != "operator" || !merged.Disabled {
		t.Fatal("prepared update overwrote operator state")
	}
	base = merged.Clone()
	updated = base.Clone()
	updated.Metadata["access_token"] = "stale"
	current = base.Clone()
	current.Metadata["access_token"] = "replacement"
	_, _ = m.Update(ctx, current)
	merged, err = m.UpdatePreparedAuth(ctx, base, updated)
	if err != nil || merged.Metadata["access_token"] != "replacement" {
		t.Fatal("prepared update overwrote newer credentials")
	}
}

func TestPersistenceRejectsOutOfOrderSnapshots(t *testing.T) {
	ctx := context.Background()
	store := &requestPrepareStore{}
	m := NewManager(store, &RoundRobinSelector{}, nil)
	initial, _ := m.Register(ctx, &Auth{ID: "persist-order", Provider: "claude", Metadata: map[string]any{"note": "old"}})
	newer := initial.Clone()
	newer.Metadata["note"] = "new"
	_, _ = m.Update(ctx, newer)
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() { _ = m.persist(ctx, initial.Clone()) })
	}
	wg.Wait()
	if store.lastAuth().Metadata["note"] != "new" {
		t.Fatal("stale persistence overwrote newer snapshot")
	}
}
