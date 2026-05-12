package executor

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/google/uuid"
	kiroauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/kiro"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
)

// KiroExecutor is a stateless executor for Kiro (kiro.dev), AWS's agentic IDE
// subscription service. It speaks the Amazon CodeWhisperer Streaming wire
// protocol with API key (`ksk_`) bearer auth and translates between Anthropic
// Messages format and Kiro's `conversationState` shape internally.
type KiroExecutor struct {
	cfg *config.Config
}

// NewKiroExecutor creates a new Kiro executor.
func NewKiroExecutor(cfg *config.Config) *KiroExecutor { return &KiroExecutor{cfg: cfg} }

// Identifier returns the executor identifier.
func (e *KiroExecutor) Identifier() string { return "kiro" }

// PrepareRequest injects Kiro credentials and required headers into the request.
func (e *KiroExecutor) PrepareRequest(req *http.Request, auth *cliproxyauth.Auth) error {
	if req == nil {
		return nil
	}
	apiKey := kiroCreds(auth)
	if strings.TrimSpace(apiKey) == "" {
		return fmt.Errorf("kiro executor: missing API key in auth credentials")
	}
	kiroauth.ApplyHeaders(req, apiKey, kiroauth.TargetGenerate)
	var attrs map[string]string
	if auth != nil {
		attrs = auth.Attributes
	}
	util.ApplyCustomHeadersFromAttrs(req, attrs)
	return nil
}

// HttpRequest injects Kiro credentials into the request and executes it.
func (e *KiroExecutor) HttpRequest(ctx context.Context, auth *cliproxyauth.Auth, req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("kiro executor: request is nil")
	}
	if ctx == nil {
		ctx = req.Context()
	}
	httpReq := req.WithContext(ctx)
	if err := e.PrepareRequest(httpReq, auth); err != nil {
		return nil, err
	}
	httpClient := helps.NewProxyAwareHTTPClient(ctx, e.cfg, auth, 0)
	return httpClient.Do(httpReq)
}

// Execute performs a non-streaming chat completion request to Kiro.
//
// The Anthropic Messages payload carried in req.Payload is translated to the
// Kiro conversationState shape, posted to Kiro's CodeWhispererStreaming
// endpoint, the streamed event frames are decoded and aggregated, and finally
// the aggregate is shaped back into an Anthropic Messages response.
func (e *KiroExecutor) Execute(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (resp cliproxyexecutor.Response, err error) {
	apiKey := kiroCreds(auth)
	if strings.TrimSpace(apiKey) == "" {
		return resp, fmt.Errorf("kiro executor: missing API key in auth credentials")
	}

	reporter := helps.NewUsageReporter(ctx, e.Identifier(), req.Model, auth)
	defer reporter.TrackFailure(ctx, &err)

	body, err := buildKiroRequestBody(req.Model, req.Payload)
	if err != nil {
		return resp, fmt.Errorf("kiro executor: build request: %w", err)
	}

	httpResp, err := e.doKiroPOST(ctx, auth, apiKey, kiroauth.KiroGenerateURL, kiroauth.TargetGenerate, body)
	if err != nil {
		return resp, err
	}
	defer func() {
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("kiro executor: close response body error: %v", errClose)
		}
	}()
	helps.RecordAPIResponseMetadata(ctx, e.cfg, httpResp.StatusCode, httpResp.Header.Clone())
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		b, _ := io.ReadAll(httpResp.Body)
		helps.AppendAPIResponseChunk(ctx, e.cfg, b)
		helps.LogWithRequestID(ctx).Debugf("kiro request error, status: %d, body: %s", httpResp.StatusCode, helps.SummarizeErrorBody(httpResp.Header.Get("Content-Type"), b))
		err = statusErr{code: httpResp.StatusCode, msg: string(b)}
		return resp, err
	}

	agg, errDecode := decodeKiroEventStream(httpResp.Body, ctx, e.cfg)
	if errDecode != nil {
		return resp, fmt.Errorf("kiro executor: decode stream: %w", errDecode)
	}

	out, errBuild := buildAnthropicResponse(req.Model, agg)
	if errBuild != nil {
		return resp, fmt.Errorf("kiro executor: build anthropic response: %w", errBuild)
	}

	reporter.Publish(ctx, kiroUsageDetail(agg, req.Payload))

	resp = cliproxyexecutor.Response{Payload: out, Headers: httpResp.Header.Clone()}
	return resp, nil
}

// kiroUsageDetail estimates token counts from the request payload and the
// generated text since Kiro reports credits, not per-token counts, on the API
// key auth path.
func kiroUsageDetail(agg *kiroEventAggregate, requestPayload []byte) usage.Detail {
	return usage.Detail{
		InputTokens:  estimateTokens(requestPayload),
		OutputTokens: estimateTokens(agg.text.Bytes()),
	}
}

// ExecuteStream performs a streaming chat completion request to Kiro.
//
// The output channel emits Anthropic Messages SSE events shaped from the
// decoded Kiro event-stream frames.
func (e *KiroExecutor) ExecuteStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (_ *cliproxyexecutor.StreamResult, err error) {
	apiKey := kiroCreds(auth)
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("kiro executor: missing API key in auth credentials")
	}

	reporter := helps.NewUsageReporter(ctx, e.Identifier(), req.Model, auth)
	defer reporter.TrackFailure(ctx, &err)

	body, errBuild := buildKiroRequestBody(req.Model, req.Payload)
	if errBuild != nil {
		return nil, fmt.Errorf("kiro executor: build request: %w", errBuild)
	}

	httpResp, errHTTP := e.doKiroPOST(ctx, auth, apiKey, kiroauth.KiroGenerateURL, kiroauth.TargetGenerate, body)
	if errHTTP != nil {
		return nil, errHTTP
	}
	helps.RecordAPIResponseMetadata(ctx, e.cfg, httpResp.StatusCode, httpResp.Header.Clone())
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		b, _ := io.ReadAll(httpResp.Body)
		helps.AppendAPIResponseChunk(ctx, e.cfg, b)
		helps.LogWithRequestID(ctx).Debugf("kiro request error, status: %d, body: %s", httpResp.StatusCode, helps.SummarizeErrorBody(httpResp.Header.Get("Content-Type"), b))
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("kiro executor: close response body error: %v", errClose)
		}
		err = statusErr{code: httpResp.StatusCode, msg: string(b)}
		return nil, err
	}

	out := make(chan cliproxyexecutor.StreamChunk)
	go func() {
		defer close(out)
		defer func() {
			if errClose := httpResp.Body.Close(); errClose != nil {
				log.Errorf("kiro executor: close response body error: %v", errClose)
			}
		}()
		emitter := newAnthropicSSEEmitter(req.Model, ctx, out)
		emitter.start()
		err := streamKiroEventStream(httpResp.Body, ctx, e.cfg, emitter)
		if err != nil && !errors.Is(err, io.EOF) {
			helps.RecordAPIResponseError(ctx, e.cfg, err)
			reporter.PublishFailure(ctx)
			select {
			case out <- cliproxyexecutor.StreamChunk{Err: err}:
			case <-ctx.Done():
			}
			return
		}
		emitter.finish()
		reporter.Publish(ctx, emitter.usageDetail(req.Payload))
	}()
	return &cliproxyexecutor.StreamResult{Headers: httpResp.Header.Clone(), Chunks: out}, nil
}

// CountTokens returns a rough token estimate. Kiro does not expose a dedicated
// counting endpoint via the API key; we approximate by character length.
func (e *KiroExecutor) CountTokens(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	tokens := estimateTokens(req.Payload)
	body := []byte(fmt.Sprintf(`{"input_tokens":%d}`, tokens))
	return cliproxyexecutor.Response{Payload: body}, nil
}

// Refresh is a no-op for Kiro: API keys are long-lived until the user rotates them.
func (e *KiroExecutor) Refresh(ctx context.Context, auth *cliproxyauth.Auth) (*cliproxyauth.Auth, error) {
	return auth, nil
}

func (e *KiroExecutor) doKiroPOST(ctx context.Context, auth *cliproxyauth.Auth, apiKey, url, target string, body []byte) (*http.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	kiroauth.ApplyHeaders(httpReq, apiKey, target)
	var attrs map[string]string
	if auth != nil {
		attrs = auth.Attributes
	}
	util.ApplyCustomHeadersFromAttrs(httpReq, attrs)

	var authID, authLabel, authType, authValue string
	if auth != nil {
		authID = auth.ID
		authLabel = auth.Label
		authType, authValue = auth.AccountInfo()
	}
	helps.RecordAPIRequest(ctx, e.cfg, helps.UpstreamRequestLog{
		URL:       url,
		Method:    http.MethodPost,
		Headers:   httpReq.Header.Clone(),
		Body:      body,
		Provider:  e.Identifier(),
		AuthID:    authID,
		AuthLabel: authLabel,
		AuthType:  authType,
		AuthValue: authValue,
	})

	httpClient := helps.NewProxyAwareHTTPClient(ctx, e.cfg, auth, 0)
	httpResp, err := httpClient.Do(httpReq)
	if err != nil {
		helps.RecordAPIResponseError(ctx, e.cfg, err)
		return nil, err
	}
	return httpResp, nil
}

// kiroCreds extracts the API key from the auth record.
func kiroCreds(a *cliproxyauth.Auth) string {
	if a == nil {
		return ""
	}
	if a.Metadata != nil {
		if v, ok := a.Metadata["api_key"].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
		if v, ok := a.Metadata["access_token"].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	if a.Attributes != nil {
		if v := a.Attributes["api_key"]; v != "" {
			return v
		}
		if v := a.Attributes["access_token"]; v != "" {
			return v
		}
	}
	return ""
}

func estimateTokens(b []byte) int64 {
	if len(b) == 0 {
		return 0
	}
	return int64(len(b) / 4)
}

// buildKiroRequestBody converts an Anthropic Messages request payload to the
// Kiro conversationState shape. The input is expected to be a Claude/Anthropic
// Messages JSON body.
func buildKiroRequestBody(modelID string, anthropicPayload []byte) ([]byte, error) {
	if !gjson.ValidBytes(anthropicPayload) {
		return nil, fmt.Errorf("invalid anthropic payload JSON")
	}

	systemText := extractAnthropicSystemText(anthropicPayload)
	tools := convertAnthropicTools(anthropicPayload)

	rawMessages := gjson.GetBytes(anthropicPayload, "messages").Array()
	turns, currentUser, err := convertMessagesToKiroTurns(rawMessages)
	if err != nil {
		return nil, err
	}

	if currentUser == nil {
		return nil, fmt.Errorf("no user message found in request")
	}

	if systemText != "" {
		// Kiro has no first-class system role; prepend system text to the
		// content of the first user-input turn or the current message.
		if len(turns) > 0 && turns[0].userInput != nil {
			turns[0].userInput.content = systemText + "\n\n" + turns[0].userInput.content
		} else {
			currentUser.content = systemText + "\n\n" + currentUser.content
		}
	}

	// Build history JSON array; strictly alternate user/assistant entries.
	history := []map[string]any{}
	for _, t := range turns {
		switch {
		case t.userInput != nil:
			history = append(history, map[string]any{"userInputMessage": t.userInput.toMap(modelID)})
		case t.assistant != nil:
			history = append(history, map[string]any{"assistantResponseMessage": t.assistant.toMap()})
		}
	}

	currentUser.tools = tools
	current := map[string]any{
		"userInputMessage": currentUser.toMap(modelID),
	}

	conversationID := uuid.NewString()
	if v := gjson.GetBytes(anthropicPayload, "metadata.user_id").String(); v != "" {
		conversationID = stableConversationID(v, anthropicPayload)
	}

	body := map[string]any{
		"conversationState": map[string]any{
			"conversationId":  conversationID,
			"chatTriggerType": "MANUAL",
			"history":         history,
			"currentMessage":  current,
		},
	}

	return json.Marshal(body)
}

func stableConversationID(seed string, payload []byte) string {
	// Stable across requests in a conversation when the client sends a
	// metadata.user_id; otherwise fall back to a fresh UUID.
	hash := uuid.NewSHA1(uuid.NameSpaceOID, []byte(seed+"|"+gjson.GetBytes(payload, "model").String()))
	return hash.String()
}

func extractAnthropicSystemText(payload []byte) string {
	v := gjson.GetBytes(payload, "system")
	if !v.Exists() {
		return ""
	}
	if v.Type == gjson.String {
		return strings.TrimSpace(v.String())
	}
	if v.IsArray() {
		var parts []string
		for _, p := range v.Array() {
			if t := strings.TrimSpace(p.Get("text").String()); t != "" {
				parts = append(parts, t)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func convertAnthropicTools(payload []byte) []map[string]any {
	tools := gjson.GetBytes(payload, "tools")
	if !tools.IsArray() {
		return nil
	}
	out := make([]map[string]any, 0, len(tools.Array()))
	for _, t := range tools.Array() {
		name := t.Get("name").String()
		if name == "" {
			continue
		}
		spec := map[string]any{
			"name":        name,
			"description": t.Get("description").String(),
		}
		if schema := t.Get("input_schema"); schema.Exists() {
			var parsed any
			if err := json.Unmarshal([]byte(schema.Raw), &parsed); err == nil {
				spec["inputSchema"] = map[string]any{"json": parsed}
			}
		}
		out = append(out, map[string]any{"toolSpecification": spec})
	}
	return out
}

type kiroUserInput struct {
	content     string
	toolResults []map[string]any
	tools       []map[string]any
	images      []map[string]any
}

func (u *kiroUserInput) toMap(modelID string) map[string]any {
	ctx := map[string]any{}
	if len(u.tools) > 0 {
		ctx["tools"] = u.tools
	}
	if len(u.toolResults) > 0 {
		ctx["toolResults"] = u.toolResults
	}
	m := map[string]any{
		"content":                 u.content,
		"modelId":                 modelID,
		"origin":                  "AI_EDITOR",
		"userInputMessageContext": ctx,
	}
	if len(u.images) > 0 {
		m["images"] = u.images
	}
	return m
}

type kiroAssistant struct {
	content   string
	messageID string
	toolUses  []map[string]any
}

func (a *kiroAssistant) toMap() map[string]any {
	m := map[string]any{
		"content":   a.content,
		"messageId": a.messageID,
	}
	if len(a.toolUses) > 0 {
		m["toolUses"] = a.toolUses
	}
	return m
}

type kiroTurn struct {
	userInput *kiroUserInput
	assistant *kiroAssistant
}

// convertMessagesToKiroTurns converts an Anthropic messages array into
// alternating Kiro turns. Returns the history (every entry except the trailing
// user turn) and the trailing user turn separately. If the messages array does
// not end on a user turn we synthesise a fresh empty user turn so the upstream
// always has a current message to act on.
func convertMessagesToKiroTurns(msgs []gjson.Result) (history []kiroTurn, current *kiroUserInput, err error) {
	turns := make([]kiroTurn, 0, len(msgs))

	flush := func(role string, t kiroTurn) {
		if len(turns) == 0 {
			turns = append(turns, t)
			return
		}
		last := turns[len(turns)-1]
		switch role {
		case "user":
			if last.userInput != nil {
				if t.userInput != nil {
					if last.userInput.content != "" && t.userInput.content != "" {
						last.userInput.content += "\n\n" + t.userInput.content
					} else if t.userInput.content != "" {
						last.userInput.content = t.userInput.content
					}
					last.userInput.toolResults = append(last.userInput.toolResults, t.userInput.toolResults...)
					last.userInput.images = append(last.userInput.images, t.userInput.images...)
					turns[len(turns)-1] = last
					return
				}
			}
		case "assistant":
			if last.assistant != nil {
				if t.assistant != nil {
					if last.assistant.content != "" && t.assistant.content != "" {
						last.assistant.content += t.assistant.content
					} else if t.assistant.content != "" {
						last.assistant.content = t.assistant.content
					}
					last.assistant.toolUses = append(last.assistant.toolUses, t.assistant.toolUses...)
					turns[len(turns)-1] = last
					return
				}
			}
		}
		turns = append(turns, t)
	}

	for _, msg := range msgs {
		role := msg.Get("role").String()
		switch role {
		case "user":
			ui := convertAnthropicUserMessage(msg)
			if ui == nil {
				continue
			}
			flush("user", kiroTurn{userInput: ui})
		case "assistant":
			a := convertAnthropicAssistantMessage(msg)
			if a == nil {
				continue
			}
			flush("assistant", kiroTurn{assistant: a})
		default:
			// Skip non-user, non-assistant roles (system handled separately).
		}
	}

	if len(turns) == 0 {
		return nil, nil, fmt.Errorf("no convertible messages")
	}

	last := turns[len(turns)-1]
	if last.userInput == nil {
		// Trailing assistant — synthesise an empty user turn so the upstream
		// has something to respond to.
		current = &kiroUserInput{content: ""}
		return turns, current, nil
	}
	current = last.userInput
	return turns[:len(turns)-1], current, nil
}

func convertAnthropicUserMessage(msg gjson.Result) *kiroUserInput {
	out := &kiroUserInput{}
	content := msg.Get("content")
	if content.Type == gjson.String {
		out.content = content.String()
		return out
	}
	if !content.IsArray() {
		return nil
	}
	var textParts []string
	for _, part := range content.Array() {
		switch part.Get("type").String() {
		case "text":
			if t := part.Get("text").String(); t != "" {
				textParts = append(textParts, t)
			}
		case "image":
			if img := convertAnthropicImage(part); img != nil {
				out.images = append(out.images, img)
			}
		case "tool_result":
			out.toolResults = append(out.toolResults, convertAnthropicToolResult(part))
		}
	}
	out.content = strings.Join(textParts, "\n")
	return out
}

func convertAnthropicImage(part gjson.Result) map[string]any {
	source := part.Get("source")
	if !source.Exists() {
		return nil
	}
	mediaType := source.Get("media_type").String()
	data := source.Get("data").String()
	if data == "" {
		return nil
	}
	format := "png"
	switch mediaType {
	case "image/jpeg", "image/jpg":
		format = "jpeg"
	case "image/gif":
		format = "gif"
	case "image/webp":
		format = "webp"
	}
	// Validate base64 to fail fast on garbage payloads.
	if _, err := base64.StdEncoding.DecodeString(data); err != nil {
		return nil
	}
	return map[string]any{
		"format": format,
		"source": map[string]any{"bytes": data},
	}
}

func convertAnthropicToolResult(part gjson.Result) map[string]any {
	id := part.Get("tool_use_id").String()
	status := "success"
	if part.Get("is_error").Bool() {
		status = "error"
	}
	contents := []map[string]any{}
	c := part.Get("content")
	switch {
	case c.Type == gjson.String:
		contents = append(contents, map[string]any{"text": c.String()})
	case c.IsArray():
		for _, p := range c.Array() {
			if p.Get("type").String() == "text" {
				contents = append(contents, map[string]any{"text": p.Get("text").String()})
			}
		}
	}
	return map[string]any{
		"toolUseId": id,
		"content":   contents,
		"status":    status,
	}
}

func convertAnthropicAssistantMessage(msg gjson.Result) *kiroAssistant {
	out := &kiroAssistant{messageID: uuid.NewString()}
	content := msg.Get("content")
	if content.Type == gjson.String {
		out.content = content.String()
		return out
	}
	if !content.IsArray() {
		return nil
	}
	var textParts []string
	for _, part := range content.Array() {
		switch part.Get("type").String() {
		case "text":
			textParts = append(textParts, part.Get("text").String())
		case "tool_use":
			id := part.Get("id").String()
			name := part.Get("name").String()
			var input any
			if iv := part.Get("input"); iv.Exists() {
				_ = json.Unmarshal([]byte(iv.Raw), &input)
			}
			out.toolUses = append(out.toolUses, map[string]any{
				"toolUseId": id,
				"name":      name,
				"input":     input,
			})
		}
	}
	out.content = strings.Join(textParts, "")
	return out
}

// kiroEventAggregate accumulates streamed events into a final assistant message.
type kiroEventAggregate struct {
	text         bytes.Buffer
	reasoning    bytes.Buffer
	toolUses     []*pendingToolUse
	conversation string
	utterance    string
	stopReason   string
	tokenUsage   tokenUsageStats
}

// tokenUsageStats captures the per-request usage telemetry Kiro emits. Kiro
// reports credit-based billing via meteringEvent and a context-window fill
// percentage via contextUsageEvent; it does not currently surface per-token
// cache hit/miss counts to API-key clients.
type tokenUsageStats struct {
	creditsUsed     float64
	creditUnit      string
	contextUsagePct float64
	present         bool
}

type pendingToolUse struct {
	id     string
	name   string
	input  bytes.Buffer
	parsed any
	done   bool
}

func decodeKiroEventStream(body io.Reader, ctx context.Context, cfg *config.Config) (*kiroEventAggregate, error) {
	agg := &kiroEventAggregate{}
	dec := eventstream.NewDecoder()
	buf := make([]byte, 0, 32*1024)
	for {
		msg, err := dec.Decode(body, buf)
		if err != nil {
			if errors.Is(err, io.EOF) || strings.Contains(err.Error(), "EOF") {
				return agg, nil
			}
			return agg, err
		}
		processKiroEvent(msg, agg)
		helps.AppendAPIResponseChunk(ctx, cfg, msg.Payload)
	}
}

func processKiroEvent(msg eventstream.Message, agg *kiroEventAggregate) {
	eventType := headerValue(msg, ":event-type")
	switch eventType {
	case "assistantResponseEvent", "codeEvent":
		text := gjson.GetBytes(msg.Payload, "content").String()
		agg.text.WriteString(text)
	case "toolUseEvent":
		id := gjson.GetBytes(msg.Payload, "toolUseId").String()
		name := gjson.GetBytes(msg.Payload, "name").String()
		input := gjson.GetBytes(msg.Payload, "input").String()
		stop := gjson.GetBytes(msg.Payload, "stop").Bool()

		var pending *pendingToolUse
		for _, p := range agg.toolUses {
			if p.id == id {
				pending = p
				break
			}
		}
		if pending == nil {
			pending = &pendingToolUse{id: id, name: name}
			agg.toolUses = append(agg.toolUses, pending)
		}
		if name != "" && pending.name == "" {
			pending.name = name
		}
		pending.input.WriteString(input)
		if stop {
			pending.done = true
			if pending.input.Len() > 0 {
				_ = json.Unmarshal(pending.input.Bytes(), &pending.parsed)
			}
		}
	case "reasoningContentEvent":
		text := gjson.GetBytes(msg.Payload, "text").String()
		agg.reasoning.WriteString(text)
	case "messageMetadataEvent":
		agg.conversation = gjson.GetBytes(msg.Payload, "conversationId").String()
		agg.utterance = gjson.GetBytes(msg.Payload, "utteranceId").String()
	case "meteringEvent":
		agg.tokenUsage.creditsUsed = gjson.GetBytes(msg.Payload, "usage").Float()
		agg.tokenUsage.creditUnit = gjson.GetBytes(msg.Payload, "unit").String()
		agg.tokenUsage.present = true
	case "contextUsageEvent":
		// Kiro emits this as a top-level field; payload looks like
		// `{"contextUsagePercentage": 0.0772}`.
		agg.tokenUsage.contextUsagePct = gjson.GetBytes(msg.Payload, "contextUsagePercentage").Float()
	case "invalidStateEvent":
		reason := gjson.GetBytes(msg.Payload, "reason").String()
		message := gjson.GetBytes(msg.Payload, "message").String()
		agg.text.WriteString(fmt.Sprintf("[kiro:invalidState %s: %s]", reason, message))
	}
}

func headerValue(msg eventstream.Message, name string) string {
	for _, h := range msg.Headers {
		if h.Name == name {
			return h.Value.String()
		}
	}
	return ""
}

// buildAnthropicResponse converts a final aggregate into an Anthropic Messages
// non-streaming response body.
func buildAnthropicResponse(modelID string, agg *kiroEventAggregate) ([]byte, error) {
	contents := []map[string]any{}
	if t := agg.reasoning.String(); t != "" {
		contents = append(contents, map[string]any{"type": "thinking", "thinking": t})
	}
	if t := agg.text.String(); t != "" {
		contents = append(contents, map[string]any{"type": "text", "text": t})
	}
	for _, tu := range agg.toolUses {
		var input any = map[string]any{}
		if tu.parsed != nil {
			input = tu.parsed
		}
		contents = append(contents, map[string]any{
			"type":  "tool_use",
			"id":    tu.id,
			"name":  tu.name,
			"input": input,
		})
	}
	stop := agg.stopReason
	if stop == "" {
		if len(agg.toolUses) > 0 {
			stop = "tool_use"
		} else {
			stop = "end_turn"
		}
	}
	body := map[string]any{
		"id":            "msg_" + uuid.NewString(),
		"type":          "message",
		"role":          "assistant",
		"model":         modelID,
		"content":       contents,
		"stop_reason":   stop,
		"stop_sequence": nil,
		"usage":         buildAnthropicUsage(agg),
	}
	return json.Marshal(body)
}

// buildAnthropicUsage shapes the usage block. Kiro reports credits, not
// per-token counts on the API key path, so input_tokens stays 0 and
// output_tokens is estimated from the generated text. Credit cost and
// context-window fill percentage are surfaced via Kiro-specific fields.
func buildAnthropicUsage(agg *kiroEventAggregate) map[string]any {
	out := map[string]any{
		"input_tokens":  0,
		"output_tokens": estimateTokens(agg.text.Bytes()),
	}
	if agg.tokenUsage.creditsUsed > 0 {
		out["kiro_credits"] = agg.tokenUsage.creditsUsed
		if agg.tokenUsage.creditUnit != "" {
			out["kiro_credit_unit"] = agg.tokenUsage.creditUnit
		}
	}
	if agg.tokenUsage.contextUsagePct > 0 {
		out["kiro_context_usage_percentage"] = agg.tokenUsage.contextUsagePct
	}
	return out
}

// streamKiroEventStream decodes the Kiro event stream and forwards events to
// the Anthropic SSE emitter.
func streamKiroEventStream(body io.Reader, ctx context.Context, cfg *config.Config, emitter *anthropicSSEEmitter) error {
	dec := eventstream.NewDecoder()
	buf := make([]byte, 0, 32*1024)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		msg, err := dec.Decode(body, buf)
		if err != nil {
			if errors.Is(err, io.EOF) || strings.Contains(err.Error(), "EOF") {
				return nil
			}
			return err
		}
		helps.AppendAPIResponseChunk(ctx, cfg, msg.Payload)
		emitter.handle(msg)
	}
}

// anthropicSSEEmitter shapes a stream of Kiro events into an Anthropic Messages
// SSE event sequence.
type anthropicSSEEmitter struct {
	model       string
	ctx         context.Context
	out         chan<- cliproxyexecutor.StreamChunk
	messageID   string
	textIndex   int
	textOpen    bool
	textBuf     bytes.Buffer
	thinkIndex  int
	thinkOpen   bool
	thinkBuf    bytes.Buffer
	tools       map[string]*emitterTool
	toolOrder   []string
	stopReason  string
	startedAt   time.Time
	contentNext int
	finished    bool
	tokenUsage  tokenUsageStats
}

type emitterTool struct {
	id       string
	name     string
	index    int
	open     bool
	inputBuf bytes.Buffer
}

func newAnthropicSSEEmitter(model string, ctx context.Context, out chan<- cliproxyexecutor.StreamChunk) *anthropicSSEEmitter {
	return &anthropicSSEEmitter{
		model:     model,
		ctx:       ctx,
		out:       out,
		messageID: "msg_" + uuid.NewString(),
		tools:     make(map[string]*emitterTool),
		startedAt: time.Now(),
	}
}

func (e *anthropicSSEEmitter) text() []byte { return e.textBuf.Bytes() }

func (e *anthropicSSEEmitter) sendEvent(event string, data map[string]any) {
	payload, err := json.Marshal(data)
	if err != nil {
		return
	}
	chunk := []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", event, payload))
	select {
	case e.out <- cliproxyexecutor.StreamChunk{Payload: chunk}:
	case <-e.ctx.Done():
	}
}

func (e *anthropicSSEEmitter) start() {
	e.sendEvent("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            e.messageID,
			"type":          "message",
			"role":          "assistant",
			"model":         e.model,
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage":         map[string]any{"input_tokens": 0, "output_tokens": 0},
		},
	})
}

func (e *anthropicSSEEmitter) ensureTextBlock() {
	if e.textOpen {
		return
	}
	if e.thinkOpen {
		e.closeThinkBlock()
	}
	e.textIndex = e.contentNext
	e.contentNext++
	e.textOpen = true
	e.sendEvent("content_block_start", map[string]any{
		"type":          "content_block_start",
		"index":         e.textIndex,
		"content_block": map[string]any{"type": "text", "text": ""},
	})
}

func (e *anthropicSSEEmitter) ensureThinkBlock() {
	if e.thinkOpen {
		return
	}
	if e.textOpen {
		e.closeTextBlock()
	}
	e.thinkIndex = e.contentNext
	e.contentNext++
	e.thinkOpen = true
	e.sendEvent("content_block_start", map[string]any{
		"type":          "content_block_start",
		"index":         e.thinkIndex,
		"content_block": map[string]any{"type": "thinking", "thinking": ""},
	})
}

func (e *anthropicSSEEmitter) closeThinkBlock() {
	if !e.thinkOpen {
		return
	}
	e.sendEvent("content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": e.thinkIndex,
	})
	e.thinkOpen = false
}

func (e *anthropicSSEEmitter) closeTextBlock() {
	if !e.textOpen {
		return
	}
	e.sendEvent("content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": e.textIndex,
	})
	e.textOpen = false
}

func (e *anthropicSSEEmitter) ensureToolBlock(id, name string) *emitterTool {
	if t, ok := e.tools[id]; ok {
		return t
	}
	t := &emitterTool{id: id, name: name, index: e.contentNext, open: true}
	e.contentNext++
	e.tools[id] = t
	e.toolOrder = append(e.toolOrder, id)
	e.sendEvent("content_block_start", map[string]any{
		"type":  "content_block_start",
		"index": t.index,
		"content_block": map[string]any{
			"type":  "tool_use",
			"id":    id,
			"name":  name,
			"input": map[string]any{},
		},
	})
	return t
}

func (e *anthropicSSEEmitter) handle(msg eventstream.Message) {
	eventType := headerValue(msg, ":event-type")
	switch eventType {
	case "assistantResponseEvent", "codeEvent":
		text := gjson.GetBytes(msg.Payload, "content").String()
		if text == "" {
			return
		}
		e.ensureTextBlock()
		e.textBuf.WriteString(text)
		e.sendEvent("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": e.textIndex,
			"delta": map[string]any{"type": "text_delta", "text": text},
		})
	case "reasoningContentEvent":
		text := gjson.GetBytes(msg.Payload, "text").String()
		if text == "" {
			return
		}
		e.ensureThinkBlock()
		e.thinkBuf.WriteString(text)
		e.sendEvent("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": e.thinkIndex,
			"delta": map[string]any{"type": "thinking_delta", "thinking": text},
		})
	case "toolUseEvent":
		id := gjson.GetBytes(msg.Payload, "toolUseId").String()
		name := gjson.GetBytes(msg.Payload, "name").String()
		input := gjson.GetBytes(msg.Payload, "input").String()
		stop := gjson.GetBytes(msg.Payload, "stop").Bool()
		if id == "" {
			return
		}
		if e.textOpen {
			e.closeTextBlock()
		}
		t := e.ensureToolBlock(id, name)
		if input != "" {
			t.inputBuf.WriteString(input)
			e.sendEvent("content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": t.index,
				"delta": map[string]any{"type": "input_json_delta", "partial_json": input},
			})
		}
		if stop && t.open {
			t.open = false
			e.sendEvent("content_block_stop", map[string]any{
				"type":  "content_block_stop",
				"index": t.index,
			})
		}
	case "messageMetadataEvent":
		// no-op: metadata captured but not surfaced as an Anthropic event
	case "meteringEvent":
		e.tokenUsage.creditsUsed = gjson.GetBytes(msg.Payload, "usage").Float()
		e.tokenUsage.creditUnit = gjson.GetBytes(msg.Payload, "unit").String()
		e.tokenUsage.present = true
	case "contextUsageEvent":
		e.tokenUsage.contextUsagePct = gjson.GetBytes(msg.Payload, "contextUsagePercentage").Float()
	case "invalidStateEvent":
		reason := gjson.GetBytes(msg.Payload, "reason").String()
		message := gjson.GetBytes(msg.Payload, "message").String()
		text := fmt.Sprintf("[kiro:invalidState %s: %s]", reason, message)
		e.ensureTextBlock()
		e.textBuf.WriteString(text)
		e.sendEvent("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": e.textIndex,
			"delta": map[string]any{"type": "text_delta", "text": text},
		})
		e.stopReason = "error"
	}
}

func (e *anthropicSSEEmitter) finish() {
	if e.finished {
		return
	}
	e.finished = true
	if e.thinkOpen {
		e.closeThinkBlock()
	}
	if e.textOpen {
		e.closeTextBlock()
	}
	for _, id := range e.toolOrder {
		t := e.tools[id]
		if t.open {
			t.open = false
			e.sendEvent("content_block_stop", map[string]any{
				"type":  "content_block_stop",
				"index": t.index,
			})
		}
	}
	stop := e.stopReason
	if stop == "" {
		if len(e.toolOrder) > 0 {
			stop = "tool_use"
		} else {
			stop = "end_turn"
		}
	}
	usageBlock := map[string]any{
		"output_tokens": estimateTokens(e.textBuf.Bytes()),
	}
	if e.tokenUsage.creditsUsed > 0 {
		usageBlock["kiro_credits"] = e.tokenUsage.creditsUsed
		if e.tokenUsage.creditUnit != "" {
			usageBlock["kiro_credit_unit"] = e.tokenUsage.creditUnit
		}
	}
	if e.tokenUsage.contextUsagePct > 0 {
		usageBlock["kiro_context_usage_percentage"] = e.tokenUsage.contextUsagePct
	}
	e.sendEvent("message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": stop, "stop_sequence": nil},
		"usage": usageBlock,
	})
	e.sendEvent("message_stop", map[string]any{"type": "message_stop"})
}

func (e *anthropicSSEEmitter) usageDetail(requestPayload []byte) usage.Detail {
	return usage.Detail{
		InputTokens:  estimateTokens(requestPayload),
		OutputTokens: estimateTokens(e.textBuf.Bytes()),
	}
}
