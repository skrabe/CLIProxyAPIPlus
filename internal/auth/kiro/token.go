package kiro

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/misc"
)

// KiroTokenStorage stores the credentials for a Kiro API key auth entry.
// Kiro API keys are long-lived; there is no refresh flow.
type KiroTokenStorage struct {
	// APIKey is the `ksk_...` value created at https://app.kiro.dev/ → API Keys.
	APIKey string `json:"api_key"`
	// Type indicates the authentication provider type, always "kiro".
	Type string `json:"type"`
	// Label is an optional human readable label for logging.
	Label string `json:"label,omitempty"`

	// Metadata holds arbitrary key-value pairs injected via hooks.
	Metadata map[string]any `json:"-"`
}

// SetMetadata allows external callers to inject metadata into the storage before saving.
func (ts *KiroTokenStorage) SetMetadata(meta map[string]any) {
	ts.Metadata = meta
}

// SaveTokenToFile serializes the Kiro token storage to a JSON file.
func (ts *KiroTokenStorage) SaveTokenToFile(authFilePath string) error {
	misc.LogSavingCredentials(authFilePath)
	ts.Type = "kiro"

	if err := os.MkdirAll(filepath.Dir(authFilePath), 0700); err != nil {
		return fmt.Errorf("failed to create directory: %v", err)
	}

	f, err := os.Create(authFilePath)
	if err != nil {
		return fmt.Errorf("failed to create token file: %w", err)
	}
	defer func() {
		_ = f.Close()
	}()

	data, errMerge := misc.MergeMetadata(ts, ts.Metadata)
	if errMerge != nil {
		return fmt.Errorf("failed to merge metadata: %w", errMerge)
	}

	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	if err = encoder.Encode(data); err != nil {
		return fmt.Errorf("failed to write token to file: %w", err)
	}
	return nil
}
