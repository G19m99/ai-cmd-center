package quotapoll

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/auth/codex"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

// Request shapes mirror the management panel's quota data layer
// (panel/src/utils/quota/constants.ts, panel/src/features/quota/providers/*/data.ts).
// The poller only ever reads usage; it never redeems, consumes or resets anything.
const (
	claudeUserAgent = "claude-cli/2.1.280 (external, cli)"
	claudeBeta      = "oauth-2025-04-20"
	codexUserAgent  = "codex-tui/0.149.1 (Mac OS 26.5.2; arm64) iTerm.app/3.6.11 (codex-tui; 0.149.1)"

	// maxResponseBytes bounds how much of a usage response is read.
	maxResponseBytes = 1 << 20
)

// Endpoints holds the usage URLs; tests point them at an httptest server.
// ClaudeBonus is unused today: the Claude usage call carries the cedar_ember
// block, so one request yields both windows and bonus.
type Endpoints struct {
	ClaudeUsage  string
	ClaudeBonus  string
	CodexUsage   string
	CodexCredits string
}

// DefaultEndpoints returns the production provider URLs.
func DefaultEndpoints() Endpoints {
	return Endpoints{
		ClaudeUsage:  "https://api.anthropic.com/api/oauth/usage",
		CodexUsage:   "https://chatgpt.com/backend-api/wham/usage",
		CodexCredits: "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits",
	}
}

func (e Endpoints) withDefaults() Endpoints {
	defaults := DefaultEndpoints()
	if strings.TrimSpace(e.ClaudeUsage) == "" {
		e.ClaudeUsage = defaults.ClaudeUsage
	}
	if strings.TrimSpace(e.CodexUsage) == "" {
		e.CodexUsage = defaults.CodexUsage
	}
	if strings.TrimSpace(e.CodexCredits) == "" {
		e.CodexCredits = defaults.CodexCredits
	}
	return e
}

// StatusError reports a non-2xx usage response.
type StatusError struct {
	Provider   string
	URL        string
	StatusCode int
}

func (e *StatusError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%s usage: HTTP %d from %s", e.Provider, e.StatusCode, e.URL)
}

// fetchResult is the outcome of one account fetch.
type fetchResult struct {
	Windows []coreauth.QuotaWindow
	Bonus   coreauth.BonusResets
}

// TokenValueForAuth resolves the bearer token for an auth the same way the
// management APICall endpoint does: metadata first, then attributes.
func TokenValueForAuth(auth *coreauth.Auth) string {
	if auth == nil {
		return ""
	}
	if v := TokenValueFromMetadata(auth.Metadata); v != "" {
		return v
	}
	if auth.Attributes != nil {
		if v := strings.TrimSpace(auth.Attributes["api_key"]); v != "" {
			return v
		}
		if v := strings.TrimSpace(auth.Attributes["session_token"]); v != "" {
			return v
		}
	}
	return ""
}

// TokenValueFromMetadata picks the first usable token out of auth metadata:
// accessToken, access_token, token (string or nested map), id_token, api_key,
// session_token, cookie.
func TokenValueFromMetadata(metadata map[string]any) string {
	if len(metadata) == 0 {
		return ""
	}
	if v, ok := metadata["accessToken"].(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	if v, ok := metadata["access_token"].(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	if tokenRaw, ok := metadata["token"]; ok && tokenRaw != nil {
		switch typed := tokenRaw.(type) {
		case string:
			if v := strings.TrimSpace(typed); v != "" {
				return v
			}
		case map[string]any:
			if v, ok := typed["access_token"].(string); ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
			if v, ok := typed["accessToken"].(string); ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		case map[string]string:
			if v := strings.TrimSpace(typed["access_token"]); v != "" {
				return v
			}
			if v := strings.TrimSpace(typed["accessToken"]); v != "" {
				return v
			}
		}
	}
	if v, ok := metadata["token"].(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	if v, ok := metadata["id_token"].(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	if v, ok := metadata["api_key"].(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	if v, ok := metadata["session_token"].(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	if v, ok := metadata["cookie"].(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return ""
}

// codexAccountID mirrors the panel's resolveCodexChatgptAccountId: explicit
// chatgpt_account_id in metadata or attributes, then the id_token claim
// (which is what the auth-files endpoint exposes as chatgpt_account_id).
// metadata.account_id, the key the Codex executor itself sends, is the last
// fallback.
func codexAccountID(auth *coreauth.Auth) string {
	if auth == nil {
		return ""
	}
	for _, key := range []string{"chatgpt_account_id", "chatgptAccountId"} {
		if v, ok := auth.Metadata[key].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	for _, key := range []string{"chatgpt_account_id", "chatgptAccountId"} {
		if v := strings.TrimSpace(auth.Attributes[key]); v != "" {
			return v
		}
	}
	for _, idToken := range []string{metadataString(auth.Metadata, "id_token"), strings.TrimSpace(auth.Attributes["id_token"])} {
		if idToken == "" {
			continue
		}
		if claims, err := codex.ParseJWTToken(idToken); err == nil && claims != nil {
			if v := strings.TrimSpace(claims.CodexAuthInfo.ChatgptAccountID); v != "" {
				return v
			}
		}
	}
	return metadataString(auth.Metadata, "account_id")
}

func metadataString(metadata map[string]any, key string) string {
	if len(metadata) == 0 {
		return ""
	}
	if v, ok := metadata[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// fetchClaude performs the single usage call that returns both windows and
// the cedar_ember bonus block.
func fetchClaude(ctx context.Context, client *http.Client, endpoints Endpoints, auth *coreauth.Auth) (fetchResult, error) {
	token := TokenValueForAuth(auth)
	if token == "" {
		return fetchResult{}, fmt.Errorf("claude usage: no access token for auth %s", auth.ID)
	}
	url := endpoints.ClaudeUsage
	if strings.Contains(url, "?") {
		url += "&cedar_ember=1&skip_spend=1"
	} else {
		url += "?cedar_ember=1&skip_spend=1"
	}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+token)
	headers.Set("Content-Type", "application/json")
	headers.Set("anthropic-beta", claudeBeta)
	headers.Set("User-Agent", claudeUserAgent)
	body, err := getJSON(ctx, client, "claude", url, headers)
	if err != nil {
		return fetchResult{}, err
	}
	windows, errParse := coreauth.ParseClaudeUsage(body)
	if errParse != nil {
		return fetchResult{}, errParse
	}
	return fetchResult{Windows: windows, Bonus: coreauth.ParseClaudeBonus(body)}, nil
}

// fetchCodex reads wham/usage for the windows, then rate-limit-reset-credits
// for the bonus. A failed credits call keeps the windows and reports
// Bonus.Known=false.
func fetchCodex(ctx context.Context, client *http.Client, endpoints Endpoints, auth *coreauth.Auth) (fetchResult, error) {
	token := TokenValueForAuth(auth)
	if token == "" {
		return fetchResult{}, fmt.Errorf("codex usage: no access token for auth %s", auth.ID)
	}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+token)
	headers.Set("Content-Type", "application/json")
	headers.Set("User-Agent", codexUserAgent)
	if accountID := codexAccountID(auth); accountID != "" {
		headers.Set("Chatgpt-Account-Id", accountID)
	}
	body, err := getJSON(ctx, client, "codex", endpoints.CodexUsage, headers)
	if err != nil {
		return fetchResult{}, err
	}
	windows, errParse := coreauth.ParseCodexUsage(body)
	if errParse != nil {
		return fetchResult{}, errParse
	}
	result := fetchResult{Windows: windows}

	creditHeaders := headers.Clone()
	creditHeaders.Set("Accept", "application/json")
	creditHeaders.Set("OpenAI-Beta", "codex-1")
	creditHeaders.Set("Originator", "Codex Desktop")
	creditsBody, errCredits := getJSON(ctx, client, "codex", endpoints.CodexCredits, creditHeaders)
	if errCredits != nil {
		result.Bonus = coreauth.BonusResets{Error: "codex reset credits: " + errCredits.Error()}
		return result, nil
	}
	result.Bonus = coreauth.ParseCodexResetCredits(creditsBody)
	return result, nil
}

// getJSON issues a GET and returns the body for 2xx responses, or a
// *StatusError for anything else. It refuses any URL that looks like a
// redeem/consume/reset endpoint as a last line of defence.
func getJSON(ctx context.Context, client *http.Client, provider, url string, headers http.Header) ([]byte, error) {
	if isMutatingQuotaURL(url) {
		return nil, fmt.Errorf("%s usage: refusing to call mutating endpoint %s", provider, url)
	}
	req, errReq := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if errReq != nil {
		return nil, fmt.Errorf("%s usage: build request: %w", provider, errReq)
	}
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	resp, errDo := client.Do(req)
	if errDo != nil {
		return nil, fmt.Errorf("%s usage: %w", provider, errDo)
	}
	defer func() { _ = resp.Body.Close() }()
	body, errRead := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if errRead != nil {
		return nil, fmt.Errorf("%s usage: read response: %w", provider, errRead)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &StatusError{Provider: provider, URL: url, StatusCode: resp.StatusCode}
	}
	return body, nil
}

// isMutatingQuotaURL recognises the reset-grant redeem and reset-credit
// consume endpoints the poller must never touch.
func isMutatingQuotaURL(url string) bool {
	lower := strings.ToLower(url)
	for _, marker := range []string{"reset_rate_limits", "/consume", "redeem"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
