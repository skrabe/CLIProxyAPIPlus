// Package kiro provides authentication and request helpers for Kiro
// (kiro.dev), AWS's agentic IDE/CLI subscription product. Kiro is backed by
// Amazon CodeWhisperer / Q Developer infrastructure; this package only
// implements API-key based authentication (`ksk_...`).
package kiro

import (
	"net/http"

	"github.com/google/uuid"
)

const (
	// KiroAPIBaseURL is the upstream host shared by both OAuth and API key auth.
	KiroAPIBaseURL = "https://q.us-east-1.amazonaws.com"

	// KiroGenerateURL is the URL used for chat (streaming) generations.
	KiroGenerateURL = KiroAPIBaseURL + "/"

	// KiroListModelsURL is the URL used to enumerate available models.
	KiroListModelsURL = KiroAPIBaseURL + "/?origin=KIRO_CLI"

	// TargetGenerate is the Smithy operation name for streamed assistant responses.
	TargetGenerate = "AmazonCodeWhispererStreamingService.GenerateAssistantResponse"

	// TargetListModels is the Smithy operation name for listing models.
	TargetListModels = "AmazonCodeWhispererService.ListAvailableModels"

	// userAgentValue mirrors the kiro-cli aws-sdk-rust user agent string.
	userAgentValue = "aws-sdk-rust/1.3.14 ua/2.1 api/codewhispererstreaming/0.1.14474 os/macos lang/rust/1.92.0 md/appVersion-2.2.2 app/AmazonQ-For-CLI"

	// xAmzUserAgentValue mirrors the kiro-cli x-amz-user-agent string.
	xAmzUserAgentValue = "aws-sdk-rust/1.3.14 ua/2.1 api/codewhispererstreaming/0.1.14474 os/macos lang/rust/1.92.0 m/F app/AmazonQ-For-CLI"
)

// ApplyHeaders sets the headers required for a Kiro API request authenticated
// with a `ksk_` API key. The required `tokentype: API_KEY` header is what
// distinguishes API key auth from OAuth at the wire level.
func ApplyHeaders(r *http.Request, apiKey, target string) {
	if r == nil {
		return
	}
	r.Header.Set("Content-Type", "application/x-amz-json-1.0")
	r.Header.Set("Authorization", "Bearer "+apiKey)
	r.Header.Set("tokentype", "API_KEY")
	r.Header.Set("x-amz-target", target)
	r.Header.Set("x-amzn-codewhisperer-optout", "false")
	r.Header.Set("User-Agent", userAgentValue)
	r.Header.Set("x-amz-user-agent", xAmzUserAgentValue)
	r.Header.Set("amz-sdk-request", "attempt=1; max=3")
	r.Header.Set("amz-sdk-invocation-id", uuid.NewString())
}
