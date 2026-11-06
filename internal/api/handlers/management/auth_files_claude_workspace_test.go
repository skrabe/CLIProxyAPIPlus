package management

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func buildClaudeEntryForWorkspaceTest(t *testing.T, provider string, metadata map[string]any) gin.H {
	t.Helper()
	authDir := t.TempDir()
	fileName := provider + "-user@example.com.json"
	filePath := filepath.Join(authDir, fileName)
	if errWrite := os.WriteFile(filePath, []byte(`{"type":"`+provider+`","email":"user@example.com"}`), 0o600); errWrite != nil {
		t.Fatalf("write auth file: %v", errWrite)
	}
	auth := &coreauth.Auth{
		ID:         fileName,
		FileName:   fileName,
		Provider:   provider,
		Status:     coreauth.StatusActive,
		Metadata:   metadata,
		Attributes: map[string]string{"path": filePath},
	}
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, nil)
	return h.buildAuthFileEntry(auth, authFileQuotaContext{})
}

func TestBuildAuthFileEntryExposesClaudeOrganizationAsWorkspace(t *testing.T) {
	entry := buildClaudeEntryForWorkspaceTest(t, "claude", map[string]any{
		"type":              "claude",
		"email":             "user@example.com",
		"organization_uuid": " 360d972a-7eb6-4f30-a6b3-0f5458aa2ca8 ",
	})
	claims, ok := entry["id_token"].(gin.H)
	if !ok {
		t.Fatalf("expected id_token claims, got %#v", entry["id_token"])
	}
	if got := claims["chatgpt_account_id"]; got != "360d972a-7eb6-4f30-a6b3-0f5458aa2ca8" {
		t.Fatalf("chatgpt_account_id = %#v, want trimmed organization uuid", got)
	}
	if len(claims) != 1 {
		t.Fatalf("expected only the workspace identity, got %#v", claims)
	}
	if got := entry["email"]; got != "user@example.com" {
		t.Fatalf("email = %#v, want unchanged", got)
	}
}

func TestBuildAuthFileEntryDistinguishesClaudeSeatsSharingEmail(t *testing.T) {
	personal := buildClaudeEntryForWorkspaceTest(t, "claude", map[string]any{
		"email":             "user@example.com",
		"organization_uuid": "org-personal",
	})
	team := buildClaudeEntryForWorkspaceTest(t, "claude", map[string]any{
		"email":             "user@example.com",
		"organization_uuid": "org-team",
	})
	personalID := personal["id_token"].(gin.H)["chatgpt_account_id"]
	teamID := team["id_token"].(gin.H)["chatgpt_account_id"]
	if personalID == teamID {
		t.Fatalf("seats sharing an email must expose distinct workspace ids, both got %#v", personalID)
	}
}

func TestBuildAuthFileEntryOmitsClaudeWorkspaceWithoutOrganization(t *testing.T) {
	entry := buildClaudeEntryForWorkspaceTest(t, "claude", map[string]any{
		"email": "user@example.com",
	})
	if _, exists := entry["id_token"]; exists {
		t.Fatalf("expected no id_token without an organization, got %#v", entry["id_token"])
	}
}

func TestBuildAuthFileEntryIgnoresOrganizationForOtherProviders(t *testing.T) {
	entry := buildClaudeEntryForWorkspaceTest(t, "gemini", map[string]any{
		"email":             "user@example.com",
		"organization_uuid": "org-should-not-leak",
	})
	if _, exists := entry["id_token"]; exists {
		t.Fatalf("expected no id_token for non-Claude provider, got %#v", entry["id_token"])
	}
}
