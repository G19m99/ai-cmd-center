package auth

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// The parsers below mirror the management panel's quota data layer
// (panel/src/features/quota/providers/{claude,codex}/data.ts,
// panel/src/services/api/claudeResetGrants.ts, panel/src/utils/quota/*).
// Field semantics are taken from there, not from provider documentation.

const (
	// quotaWeeklySeconds is the shortest window length treated as weekly (6 days).
	quotaWeeklySeconds = 518400
	// quotaFiveHourMaxSeconds is the longest window length labelled "5-hour" (6 hours).
	quotaFiveHourMaxSeconds = 21600
)

// claudeUsageWindowKeys is CLAUDE_USAGE_WINDOW_KEYS in payload order.
var claudeUsageWindowKeys = []struct {
	key, label, scope string
	routing           bool
}{
	{key: "five_hour", label: "5-hour", routing: true},
	{key: "seven_day", label: "7-day", routing: true},
	{key: "seven_day_oauth_apps", label: "7-day OAuth apps", routing: false},
	{key: "seven_day_opus", label: "7-day Opus", scope: "opus", routing: true},
	{key: "seven_day_sonnet", label: "7-day Sonnet", scope: "sonnet", routing: true},
	{key: "seven_day_cowork", label: "7-day Cowork", routing: false},
	{key: "iguana_necktie", label: "7-day Fable", scope: "fable", routing: true},
}

// claudeResetWindows is ANTHROPIC_RESET_WINDOWS: the only windows a grant may
// clear; anything else upstream adds is dropped.
var claudeResetWindows = []string{"five_hour", "seven_day", "seven_day_overage_included"}

var claudeResetGrantIDRe = regexp.MustCompile(`^[a-z0-9_-]{1,40}$`)

// ParseClaudeUsage maps the Claude OAuth usage response to quota windows.
// Null or missing windows are omitted. Utilization is already a percent in
// this payload, matching the panel, so it is passed through unchanged.
func ParseClaudeUsage(body []byte) ([]QuotaWindow, error) {
	root, err := quotaRootObject(body)
	if err != nil {
		return nil, fmt.Errorf("claude usage: %w", err)
	}
	fableLimit := findClaudeFableLimit(root.Get("limits"))
	windows := make([]QuotaWindow, 0, len(claudeUsageWindowKeys))
	for _, meta := range claudeUsageWindowKeys {
		window := root.Get(meta.key)
		if !window.IsObject() || !window.Get("utilization").Exists() {
			continue
		}
		if meta.key == "iguana_necktie" {
			// A dollar-denominated iguana_necktie is the cloud-session credit
			// pool, not a per-model plan window; the panel drops it from the
			// plan meters. A weekly_scoped Fable limit supersedes the key.
			if isClaudeDollarWindow(window) || fableLimit.Exists() {
				continue
			}
		}
		utilization, ok := quotaNumber(window.Get("utilization"))
		if !ok {
			continue
		}
		windows = append(windows, QuotaWindow{
			Key:         meta.key,
			Label:       meta.label,
			Utilization: utilization,
			ResetsAt:    quotaTimestamp(window.Get("resets_at")),
			Weekly:      meta.key != "five_hour",
			ModelScope:  meta.scope,
			Routing:     meta.routing,
		})
	}
	if fableLimit.Exists() {
		if percent, ok := quotaNumber(fableLimit.Get("percent")); ok {
			windows = append(windows, QuotaWindow{
				Key:         "iguana_necktie",
				Label:       "7-day Fable",
				Utilization: percent,
				ResetsAt:    quotaTimestamp(fableLimit.Get("resets_at")),
				Weekly:      true,
				ModelScope:  "fable",
				Routing:     true,
			})
		}
	}
	if len(windows) == 0 {
		return nil, errors.New("claude usage: no quota windows in payload")
	}
	return windows, nil
}

// findClaudeFableLimit mirrors the panel's findFableUsageLimit: the active
// weekly_scoped limit whose model display name is Fable, else the first one.
func findClaudeFableLimit(limits gjson.Result) gjson.Result {
	if !limits.IsArray() {
		return gjson.Result{}
	}
	var first gjson.Result
	var active gjson.Result
	limits.ForEach(func(_, limit gjson.Result) bool {
		kind := strings.ToLower(strings.TrimSpace(limit.Get("kind").String()))
		model := strings.ToLower(strings.TrimSpace(limit.Get("scope.model.display_name").String()))
		if kind != "weekly_scoped" || (model != "fable" && model != "fable 5") {
			return true
		}
		if _, ok := quotaNumber(limit.Get("percent")); !ok {
			return true
		}
		if !first.Exists() {
			first = limit
		}
		if limit.Get("is_active").Type == gjson.True && !active.Exists() {
			active = limit
			return false
		}
		return true
	})
	if active.Exists() {
		return active
	}
	return first
}

func isClaudeDollarWindow(window gjson.Result) bool {
	for _, field := range []string{"limit_dollars", "used_dollars", "remaining_dollars"} {
		if _, ok := quotaNumber(window.Get(field)); ok {
			return true
		}
	}
	return false
}

// claudeResetGrant is one parsed cedar_ember grant.
type claudeResetGrant struct {
	id               string
	resetsLeft       int
	startsAt, endsAt time.Time
	clears           []string
	paused           bool
	usableNow        bool
}

// ParseClaudeBonus reads the cedar_ember block of the Claude usage response.
// Available sums resets_left over grants that are usable now: not paused,
// usable_now, resets_left > 0 and inside their starts_at/ends_at window.
// A missing or malformed block yields Known=false.
func ParseClaudeBonus(body []byte) BonusResets {
	root, err := quotaRootObject(body)
	if err != nil {
		return BonusResets{Error: "claude bonus: " + err.Error()}
	}
	block := root.Get("cedar_ember")
	if !block.Exists() || block.Type == gjson.Null {
		return BonusResets{Error: "claude bonus: cedar_ember block missing"}
	}
	eligible := block.Get("eligible")
	if !block.IsObject() || !eligible.IsBool() {
		return BonusResets{Error: "claude bonus: cedar_ember block malformed"}
	}
	rawGrants := block.Get("grants")
	if rawGrants.Exists() && rawGrants.Type != gjson.Null && !rawGrants.IsArray() {
		return BonusResets{Error: "claude bonus: grants malformed"}
	}
	grants := make([]claudeResetGrant, 0)
	seen := make(map[string]struct{})
	malformed := false
	rawGrants.ForEach(func(_, raw gjson.Result) bool {
		grant, ok := parseClaudeResetGrant(raw)
		if !ok {
			malformed = true
			return false
		}
		if _, dup := seen[grant.id]; dup {
			malformed = true
			return false
		}
		seen[grant.id] = struct{}{}
		grants = append(grants, grant)
		return true
	})
	if malformed {
		return BonusResets{Error: "claude bonus: grant malformed"}
	}
	bonus := BonusResets{Known: true}
	if !eligible.Bool() {
		return bonus
	}
	now := quotaNow()
	clears := make(map[string]struct{})
	for _, grant := range grants {
		if grant.paused || !grant.usableNow || grant.resetsLeft <= 0 {
			continue
		}
		if !grant.startsAt.IsZero() && grant.startsAt.After(now) {
			continue
		}
		if !grant.endsAt.IsZero() && !grant.endsAt.After(now) {
			continue
		}
		bonus.Available += grant.resetsLeft
		for _, window := range grant.clears {
			clears[window] = struct{}{}
		}
		if !grant.endsAt.IsZero() && (bonus.ExpiresAt.IsZero() || grant.endsAt.Before(bonus.ExpiresAt)) {
			bonus.ExpiresAt = grant.endsAt
		}
	}
	for _, window := range claudeResetWindows {
		if _, ok := clears[window]; ok {
			bonus.Clears = append(bonus.Clears, window)
		}
	}
	return bonus
}

// parseClaudeResetGrant mirrors the panel's parseGrant validation; any field
// that fails rejects the grant (and so the whole block).
func parseClaudeResetGrant(raw gjson.Result) (claudeResetGrant, bool) {
	if !raw.IsObject() {
		return claudeResetGrant{}, false
	}
	id := raw.Get("id")
	if id.Type != gjson.String || !claudeResetGrantIDRe.MatchString(id.String()) {
		return claudeResetGrant{}, false
	}
	total, okTotal := quotaCount(raw.Get("resets_total"))
	left, okLeft := quotaCount(raw.Get("resets_left"))
	if !okTotal || !okLeft || left > total {
		return claudeResetGrant{}, false
	}
	startsAt, okStart := quotaOptionalTimestamp(raw.Get("starts_at"))
	endsAt, okEnd := quotaOptionalTimestamp(raw.Get("ends_at"))
	if !okStart || !okEnd {
		return claudeResetGrant{}, false
	}
	clears, okClears := parseClaudeResetClears(raw.Get("clears"))
	paused, okPaused := quotaOptionalBool(raw.Get("paused"), false)
	usableNow, okUsable := quotaOptionalBool(raw.Get("usable_now"), false)
	_, okRequires := quotaOptionalBool(raw.Get("use_requires_limit"), true)
	if !okClears || !okPaused || !okUsable || !okRequires {
		return claudeResetGrant{}, false
	}
	return claudeResetGrant{
		id:         id.String(),
		resetsLeft: left,
		startsAt:   startsAt,
		endsAt:     endsAt,
		clears:     clears,
		paused:     paused,
		usableNow:  usableNow,
	}, true
}

func parseClaudeResetClears(value gjson.Result) ([]string, bool) {
	if !value.Exists() || value.Type == gjson.Null {
		return nil, true
	}
	if !value.IsArray() {
		return nil, false
	}
	present := make(map[string]struct{})
	value.ForEach(func(_, item gjson.Result) bool {
		if item.Type == gjson.String {
			present[item.String()] = struct{}{}
		}
		return true
	})
	var out []string
	for _, window := range claudeResetWindows {
		if _, ok := present[window]; ok {
			out = append(out, window)
		}
	}
	return out, true
}

// ParseCodexUsage maps the Codex wham/usage response to quota windows:
// rate_limit primary/secondary, code_review_rate_limit (Routing=false), and
// additional_rate_limits (ModelScope = lowercased limit name).
func ParseCodexUsage(body []byte) ([]QuotaWindow, error) {
	root, err := quotaRootObject(body)
	if err != nil {
		return nil, fmt.Errorf("codex usage: %w", err)
	}
	windows := make([]QuotaWindow, 0, 6)
	windows = appendCodexRateLimit(windows, quotaFirst(root, "rate_limit", "rateLimit"), "", "", "", true)
	windows = appendCodexRateLimit(windows, quotaFirst(root, "code_review_rate_limit", "codeReviewRateLimit"), "code_review_rate_limit.", "Code review ", "", false)

	additional := quotaFirst(root, "additional_rate_limits", "additionalRateLimits")
	if additional.IsArray() {
		index := 0
		additional.ForEach(func(_, item gjson.Result) bool {
			index++
			info := quotaFirst(item, "rate_limit", "rateLimit")
			if !info.IsObject() {
				return true
			}
			name := quotaString(quotaFirst(item, "limit_name", "limitName"))
			if name == "" {
				name = quotaString(quotaFirst(item, "metered_feature", "meteredFeature"))
			}
			if name == "" {
				name = fmt.Sprintf("additional-%d", index)
			}
			scope := strings.ToLower(name)
			windows = appendCodexRateLimit(windows, info, scope+":", name+" ", scope, true)
			return true
		})
	}
	if len(windows) == 0 {
		return nil, errors.New("codex usage: no quota windows in payload")
	}
	return windows, nil
}

// appendCodexRateLimit adds the primary and secondary windows of one
// rate-limit block. limitReached/allowed=false read as 100% when the window
// carries no used_percent, matching the panel.
func appendCodexRateLimit(windows []QuotaWindow, info gjson.Result, keyPrefix, labelPrefix, scope string, routing bool) []QuotaWindow {
	if !info.IsObject() {
		return windows
	}
	limitReached := quotaFirst(info, "limit_reached", "limitReached").Type == gjson.True
	allowed := info.Get("allowed")
	if allowed.Type == gjson.False {
		limitReached = true
	}
	for _, slot := range []struct {
		key        string
		camel      string
		fallbackWk bool
	}{
		{key: "primary_window", camel: "primaryWindow", fallbackWk: false},
		{key: "secondary_window", camel: "secondaryWindow", fallbackWk: true},
	} {
		window := quotaFirst(info, slot.key, slot.camel)
		if !window.IsObject() {
			continue
		}
		resetsAt := quotaTimestamp(quotaFirst(window, "reset_at", "resetAt"))
		if resetsAt.IsZero() {
			if after, ok := quotaNumber(quotaFirst(window, "reset_after_seconds", "resetAfterSeconds")); ok && after > 0 {
				resetsAt = quotaNow().Add(time.Duration(after * float64(time.Second)))
			}
		}
		utilization, ok := quotaNumber(quotaFirst(window, "used_percent", "usedPercent"))
		if !ok {
			if !limitReached || resetsAt.IsZero() {
				continue
			}
			utilization = 100
		}
		weekly := slot.fallbackWk
		label := "Weekly"
		if !weekly {
			label = "5-hour"
		}
		if seconds, okSeconds := quotaNumber(quotaFirst(window, "limit_window_seconds", "limitWindowSeconds")); okSeconds && seconds > 0 {
			weekly = seconds >= quotaWeeklySeconds
			if seconds <= quotaFiveHourMaxSeconds {
				label = "5-hour"
			} else {
				label = "Weekly"
			}
		}
		windows = append(windows, QuotaWindow{
			Key:         keyPrefix + slot.key,
			Label:       labelPrefix + label,
			Utilization: utilization,
			ResetsAt:    resetsAt,
			Weekly:      weekly,
			ModelScope:  scope,
			Routing:     routing,
		})
	}
	return windows
}

// ParseCodexResetCredits reads the wham/rate-limit-reset-credits response.
// Available is applicable_available_count, else available_count, else the
// number of available credits. ExpiresAt is the earliest expiry among
// available codex_rate_limits credits.
func ParseCodexResetCredits(body []byte) BonusResets {
	root, err := quotaRootObject(body)
	if err != nil {
		return BonusResets{Error: "codex reset credits: " + err.Error()}
	}
	expectedShape := false
	for _, key := range []string{"credits", "available_count", "availableCount", "applicable_available_count", "applicableAvailableCount"} {
		if root.Get(key).Exists() {
			expectedShape = true
			break
		}
	}
	if !expectedShape {
		return BonusResets{Error: "codex reset credits: unexpected payload shape"}
	}
	bonus := BonusResets{Known: true}
	availableCredits := 0
	credits := root.Get("credits")
	if credits.IsArray() {
		credits.ForEach(func(_, credit gjson.Result) bool {
			if !credit.IsObject() {
				return true
			}
			if quotaString(quotaFirst(credit, "reset_type", "resetType")) != "codex_rate_limits" {
				return true
			}
			if quotaString(credit.Get("status")) != "available" {
				return true
			}
			expiresAt := quotaTimestamp(quotaFirst(credit, "expires_at", "expiresAt"))
			if expiresAt.IsZero() {
				return true
			}
			availableCredits++
			if bonus.ExpiresAt.IsZero() || expiresAt.Before(bonus.ExpiresAt) {
				bonus.ExpiresAt = expiresAt
			}
			return true
		})
	}
	if count, ok := quotaNumber(quotaFirst(root, "applicable_available_count", "applicableAvailableCount")); ok {
		bonus.Available = int(count)
	} else if count, ok := quotaNumber(quotaFirst(root, "available_count", "availableCount")); ok {
		bonus.Available = int(count)
	} else {
		bonus.Available = availableCredits
	}
	if bonus.Available < 0 {
		bonus.Available = 0
	}
	if bonus.Available > 0 {
		// A Codex reset credit clears the account's rate limits as a whole.
		bonus.Clears = []string{"primary_window", "secondary_window"}
	}
	return bonus
}

// quotaRootObject validates that body is a JSON object.
func quotaRootObject(body []byte) (gjson.Result, error) {
	if !gjson.ValidBytes(body) {
		return gjson.Result{}, errors.New("invalid JSON")
	}
	root := gjson.ParseBytes(body)
	if !root.IsObject() {
		return gjson.Result{}, errors.New("payload is not a JSON object")
	}
	return root, nil
}

// quotaFirst returns the first existing, non-null key among the candidates.
func quotaFirst(parent gjson.Result, keys ...string) gjson.Result {
	for _, key := range keys {
		value := parent.Get(key)
		if value.Exists() && value.Type != gjson.Null {
			return value
		}
	}
	return gjson.Result{}
}

// quotaNumber mirrors normalizeNumberValue: a finite number, or a numeric
// string.
func quotaNumber(value gjson.Result) (float64, bool) {
	switch value.Type {
	case gjson.Number:
		return value.Num, true
	case gjson.String:
		trimmed := strings.TrimSpace(value.Str)
		if trimmed == "" {
			return 0, false
		}
		parsed, err := strconv.ParseFloat(trimmed, 64)
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

// quotaCount mirrors isCount: a non-negative integer number.
func quotaCount(value gjson.Result) (int, bool) {
	if value.Type != gjson.Number {
		return 0, false
	}
	if value.Num < 0 || value.Num != float64(int64(value.Num)) {
		return 0, false
	}
	return int(value.Num), true
}

// quotaString mirrors normalizeStringValue: a trimmed non-empty string, or a
// number rendered as text.
func quotaString(value gjson.Result) string {
	switch value.Type {
	case gjson.String:
		return strings.TrimSpace(value.Str)
	case gjson.Number:
		return strconv.FormatFloat(value.Num, 'f', -1, 64)
	default:
		return ""
	}
}

// quotaOptionalBool mirrors optionalBoolean: absent/null → fallback, a bool →
// itself, anything else is malformed.
func quotaOptionalBool(value gjson.Result, fallback bool) (bool, bool) {
	switch value.Type {
	case gjson.Null:
		return fallback, true
	case gjson.True:
		return true, true
	case gjson.False:
		return false, true
	}
	if !value.Exists() {
		return fallback, true
	}
	return false, false
}

// quotaOptionalTimestamp mirrors optionalTimestamp: absent/null → zero,
// an unparsable string is malformed.
func quotaOptionalTimestamp(value gjson.Result) (time.Time, bool) {
	if !value.Exists() || value.Type == gjson.Null {
		return time.Time{}, true
	}
	if value.Type != gjson.String {
		return time.Time{}, false
	}
	parsed := quotaTimestamp(value)
	if parsed.IsZero() {
		return time.Time{}, false
	}
	return parsed, true
}

var quotaFractionRe = regexp.MustCompile(`(\.\d{9})\d+`)

// quotaTimestamp mirrors resolveResetMs: an ISO-8601 string first (tolerating
// over-precise fractional seconds), then a Unix value in seconds or
// milliseconds, disambiguated by magnitude. Returns zero when unparsable.
func quotaTimestamp(value gjson.Result) time.Time {
	switch value.Type {
	case gjson.String:
		trimmed := strings.TrimSpace(value.Str)
		if trimmed == "" {
			return time.Time{}
		}
		normalized := quotaFractionRe.ReplaceAllString(trimmed, "$1")
		if parsed, err := time.Parse(time.RFC3339Nano, normalized); err == nil {
			return parsed
		}
		numeric, err := strconv.ParseFloat(trimmed, 64)
		if err != nil {
			return time.Time{}
		}
		return quotaUnixTime(numeric)
	case gjson.Number:
		return quotaUnixTime(value.Num)
	default:
		return time.Time{}
	}
}

func quotaUnixTime(numeric float64) time.Time {
	if numeric <= 0 {
		return time.Time{}
	}
	if numeric < 1e11 {
		seconds := int64(numeric)
		nanos := int64((numeric - float64(seconds)) * float64(time.Second))
		return time.Unix(seconds, nanos)
	}
	return time.UnixMilli(int64(numeric))
}
