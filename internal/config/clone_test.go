package config

import "testing"

func TestCloneForRuntimePreservesAndIsolatesConfig(t *testing.T) {
	cfg := &Config{
		SourceHash: "snapshot-hash", legacyMigrationPending: true,
		Home:                HomeConfig{Enabled: true, TLS: HomeTLSConfig{ClientKey: "runtime-key"}},
		OAuthExcludedModels: map[string][]string{"claude": {"hidden"}},
		ClaudeKey:           []ClaudeKey{{APIKey: "key", Headers: map[string]string{"User-Agent": "original"}}},
		Payload:             PayloadConfig{Default: []PayloadRule{{Params: map[string]interface{}{"nested": map[string]interface{}{"values": []interface{}{"original"}}}}}},
	}
	clone := cfg.CloneForRuntime()
	if clone == cfg || clone.SourceHash != cfg.SourceHash || clone.Home != cfg.Home || !clone.legacyMigrationPending {
		t.Fatal("clone lost runtime/private fields")
	}
	clone.OAuthExcludedModels["claude"][0] = "changed"
	clone.ClaudeKey[0].Headers["User-Agent"] = "changed"
	clone.Payload.Default[0].Params["nested"].(map[string]interface{})["values"].([]interface{})[0] = "changed"
	if cfg.OAuthExcludedModels["claude"][0] != "hidden" || cfg.ClaudeKey[0].Headers["User-Agent"] != "original" || cfg.Payload.Default[0].Params["nested"].(map[string]interface{})["values"].([]interface{})[0] != "original" {
		t.Fatal("clone shares reference fields")
	}
	var empty *Config
	if empty.CloneForRuntime() != nil {
		t.Fatal("nil clone should be nil")
	}
}
