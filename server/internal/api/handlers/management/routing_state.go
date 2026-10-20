package management

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/quotapoll"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

const (
	routingStateStaleAfter      = 15 * time.Minute
	routingStatePollerMissing   = "quota poller not running (routing.strategy is not reset-aware)"
	routingStatusDraining       = "draining"
	routingStatusStandby        = "standby"
	routingStatusLimited        = "limited"
	routingStatusUnknown        = "unknown"
	routingStateResetAwareLabel = "reset-aware"
)

// routingStateProviders are the providers the routing-state view reports on.
var routingStateProviders = []string{"claude", "codex"}

type routingStateWindow struct {
	Key         string  `json:"key"`
	Label       string  `json:"label"`
	Utilization float64 `json:"utilization"`
	ResetsAt    string  `json:"resets_at"`
	Weekly      bool    `json:"weekly"`
	ModelScope  string  `json:"model_scope"`
	Routing     bool    `json:"routing"`
}

type routingStateBonus struct {
	Known     bool     `json:"known"`
	Available int      `json:"available"`
	Clears    []string `json:"clears"`
	ExpiresAt string   `json:"expires_at"`
}

type routingStateQuota struct {
	FetchedAt string               `json:"fetched_at"`
	Stale     bool                 `json:"stale"`
	LastError string               `json:"last_error"`
	Windows   []routingStateWindow `json:"windows"`
	Bonus     routingStateBonus    `json:"bonus"`
}

type routingStateAccount struct {
	AuthIndex      string            `json:"auth_index"`
	Provider       string            `json:"provider"`
	Label          string            `json:"label"`
	Status         string            `json:"status"`
	Urgent         bool              `json:"urgent"`
	Rank           int               `json:"rank"`
	Sessions       int               `json:"sessions"`
	LimitedBy      string            `json:"limited_by"`
	FreeAt         string            `json:"free_at"`
	WeeklyResetsAt string            `json:"weekly_resets_at"`
	Headroom       float64           `json:"headroom"`
	Quota          routingStateQuota `json:"quota"`

	freeAt time.Time
}

type routingStateProvider struct {
	UrgentAuthIndex string `json:"urgent_auth_index"`
	EarliestFreeAt  string `json:"earliest_free_at"`
}

type routingStateResponse struct {
	Strategy    string                          `json:"strategy"`
	GeneratedAt string                          `json:"generated_at"`
	Providers   map[string]routingStateProvider `json:"providers"`
	Accounts    []routingStateAccount           `json:"accounts"`
}

// GetRoutingState reports each Claude/Codex account's routing status, rank,
// bound session count and last quota snapshot.
func (h *Handler) GetRoutingState(c *gin.Context) {
	if h.authManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "core auth manager unavailable"})
		return
	}
	c.JSON(http.StatusOK, h.buildRoutingState(time.Now()))
}

// RefreshRoutingState synchronously re-polls quota for one auth_index (or every
// enabled Claude/Codex auth when auth_index is omitted) and returns the fresh state.
func (h *Handler) RefreshRoutingState(c *gin.Context) {
	poller := quotapoll.Default()
	if poller == nil {
		c.JSON(http.StatusConflict, gin.H{"error": routingStatePollerMissing})
		return
	}
	if h.authManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "core auth manager unavailable"})
		return
	}

	var req struct {
		AuthIndex string `json:"auth_index"`
	}
	body, errRead := io.ReadAll(c.Request.Body)
	if errRead != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if len(bytes.TrimSpace(body)) > 0 {
		if errUnmarshal := json.Unmarshal(body, &req); errUnmarshal != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
	}

	ctx := c.Request.Context()
	if authIndex := strings.TrimSpace(req.AuthIndex); authIndex != "" {
		auth := h.authByIndex(authIndex)
		if auth == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "auth not found"})
			return
		}
		if errRefresh := poller.Refresh(ctx, auth.ID); errRefresh != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("failed to refresh quota: %v", errRefresh)})
			return
		}
	} else {
		for _, auth := range h.authManager.List() {
			if auth == nil || auth.Disabled || !isRoutingStateProvider(auth.Provider) {
				continue
			}
			if errRefresh := poller.Refresh(ctx, auth.ID); errRefresh != nil {
				// The poller records the failure on the snapshot (last_error).
				log.WithField("auth_id", auth.ID).Warnf("routing-state refresh failed: %v", errRefresh)
			}
		}
	}
	c.JSON(http.StatusOK, h.buildRoutingState(time.Now()))
}

func isRoutingStateProvider(provider string) bool {
	provider = strings.ToLower(strings.TrimSpace(provider))
	for _, candidate := range routingStateProviders {
		if provider == candidate {
			return true
		}
	}
	return false
}

// routingSelectors unwraps the manager's selector into its session-affinity
// layer and reset-aware ranker, either of which may be nil.
func routingSelectors(selector coreauth.Selector) (*coreauth.SessionAffinitySelector, *coreauth.ResetAwareSelector) {
	switch s := selector.(type) {
	case *coreauth.ResetAwareSelector:
		return nil, s
	case *coreauth.SessionAffinitySelector:
		resetAware, _ := s.Fallback().(*coreauth.ResetAwareSelector)
		return s, resetAware
	default:
		return nil, nil
	}
}

func (h *Handler) buildRoutingState(now time.Time) routingStateResponse {
	affinity, resetAware := routingSelectors(h.authManager.Selector())
	strategy := routingStateResetAwareLabel
	if resetAware == nil {
		strategy = "round-robin"
		if h.cfg != nil {
			if normalized, ok := normalizeRoutingStrategy(h.cfg.Routing.Strategy); ok && normalized != routingStateResetAwareLabel {
				strategy = normalized
			}
		}
	}
	sessions := map[string]int{}
	if affinity != nil {
		sessions = affinity.BindingCounts()
	}

	byProvider := make(map[string][]*coreauth.Auth)
	for _, auth := range h.authManager.List() {
		if auth == nil || !isRoutingStateProvider(auth.Provider) {
			continue
		}
		auth.EnsureIndex()
		provider := strings.ToLower(strings.TrimSpace(auth.Provider))
		byProvider[provider] = append(byProvider[provider], auth)
	}

	out := routingStateResponse{
		Strategy:    strategy,
		GeneratedAt: now.UTC().Format(time.RFC3339),
		Providers:   make(map[string]routingStateProvider, len(routingStateProviders)),
		Accounts:    []routingStateAccount{},
	}
	for _, provider := range routingStateProviders {
		accounts, summary := buildProviderRoutingState(provider, byProvider[provider], resetAware, sessions, now)
		out.Providers[provider] = summary
		out.Accounts = append(out.Accounts, accounts...)
	}
	return out
}

// buildProviderRoutingState evaluates one provider. With a reset-aware selector
// only upstream-available accounts are ranked; without one every account has
// rank 0 and its status comes from upstream availability alone.
func buildProviderRoutingState(provider string, auths []*coreauth.Auth, resetAware *coreauth.ResetAwareSelector, sessions map[string]int, now time.Time) ([]routingStateAccount, routingStateProvider) {
	summary := routingStateProvider{}
	if len(auths) == 0 {
		return []routingStateAccount{}, summary
	}

	available := make([]*coreauth.Auth, 0, len(auths))
	blocked := make([]*coreauth.Auth, 0)
	for _, auth := range auths {
		if reason, _ := upstreamRoutingBlock(auth, now); reason != "" {
			blocked = append(blocked, auth)
		} else {
			available = append(available, auth)
		}
	}

	// facts supplies weekly reset and headroom even without a reset-aware strategy.
	ranker := resetAware
	if ranker == nil {
		ranker = coreauth.NewResetAwareSelector(coreauth.ResetAwareConfig{})
	}
	rankedAvailable := ranker.Rank(provider, "", available)
	rankedBlocked := ranker.Rank(provider, "", blocked)

	accounts := make([]routingStateAccount, 0, len(auths))
	urgentIndex := ""
	for _, ranked := range rankedAvailable {
		account := newRoutingStateAccount(ranked, sessions, now)
		switch {
		case resetAware == nil:
			account.Status = routingStatusStandby
			account.Rank = 0
			account.LimitedBy, account.FreeAt, account.freeAt = "", "", time.Time{}
		case !ranked.Usable:
			account.Status = routingStatusLimited
		case !ranked.Known:
			account.Status = routingStatusUnknown
		case ranked.Rank == 1:
			account.Status = routingStatusDraining
		default:
			account.Status = routingStatusStandby
		}
		if resetAware != nil && ranked.Urgent {
			account.Urgent = true
			urgentIndex = account.AuthIndex
		}
		accounts = append(accounts, account)
	}
	for _, ranked := range rankedBlocked {
		account := newRoutingStateAccount(ranked, sessions, now)
		account.Status = routingStatusLimited
		account.Rank = 0
		reason, retryAt := upstreamRoutingBlock(ranked.Auth, now)
		if resetAware == nil || ranked.Usable || account.LimitedBy == "" {
			account.LimitedBy = reason
			account.freeAt = retryAt
		} else if retryAt.After(account.freeAt) {
			// Free only once both the snapshot window and the upstream cooldown clear.
			account.freeAt = retryAt
		}
		account.FreeAt = formatRoutingTime(account.freeAt)
		accounts = append(accounts, account)
	}

	// Ranked accounts first by rank, then limited ones by free time.
	sort.SliceStable(accounts, func(i, j int) bool {
		a, b := accounts[i], accounts[j]
		aLimited, bLimited := a.Status == routingStatusLimited, b.Status == routingStatusLimited
		if aLimited != bLimited {
			return !aLimited
		}
		if !aLimited {
			return a.Rank < b.Rank
		}
		if !a.freeAt.Equal(b.freeAt) {
			if a.freeAt.IsZero() || b.freeAt.IsZero() {
				return !a.freeAt.IsZero()
			}
			return a.freeAt.Before(b.freeAt)
		}
		return false
	})

	var earliest time.Time
	for _, account := range accounts {
		if account.Status != routingStatusLimited || account.freeAt.IsZero() {
			continue
		}
		if earliest.IsZero() || account.freeAt.Before(earliest) {
			earliest = account.freeAt
		}
	}
	summary.UrgentAuthIndex = urgentIndex
	summary.EarliestFreeAt = formatRoutingTime(earliest)
	return accounts, summary
}

func newRoutingStateAccount(ranked coreauth.RankedAuth, sessions map[string]int, now time.Time) routingStateAccount {
	auth := ranked.Auth
	account := routingStateAccount{
		AuthIndex:      auth.EnsureIndex(),
		Provider:       strings.ToLower(strings.TrimSpace(auth.Provider)),
		Label:          routingStateLabel(auth),
		Rank:           ranked.Rank,
		Sessions:       sessions[auth.ID],
		LimitedBy:      ranked.LimitedBy,
		FreeAt:         formatRoutingTime(ranked.FreeAt),
		WeeklyResetsAt: formatRoutingTime(ranked.WeeklyResetAt),
		Headroom:       ranked.Headroom,
		Quota:          routingStateQuotaFor(auth.ID, now),
		freeAt:         ranked.FreeAt,
	}
	return account
}

func routingStateQuotaFor(authID string, now time.Time) routingStateQuota {
	quota := routingStateQuota{
		Stale:   true,
		Windows: []routingStateWindow{},
		Bonus:   routingStateBonus{Clears: []string{}},
	}
	snap, ok := coreauth.DefaultQuotaSnapshots().Get(authID)
	if !ok {
		return quota
	}
	quota.FetchedAt = formatRoutingTime(snap.FetchedAt)
	quota.Stale = snap.Stale(now, routingStateStaleAfter)
	quota.LastError = snap.LastError
	for _, window := range snap.Windows {
		quota.Windows = append(quota.Windows, routingStateWindow{
			Key:         window.Key,
			Label:       window.Label,
			Utilization: window.Utilization,
			ResetsAt:    formatRoutingTime(window.ResetsAt),
			Weekly:      window.Weekly,
			ModelScope:  window.ModelScope,
			Routing:     window.Routing,
		})
	}
	quota.Bonus.Known = snap.Bonus.Known
	quota.Bonus.Available = snap.Bonus.Available
	quota.Bonus.ExpiresAt = formatRoutingTime(snap.Bonus.ExpiresAt)
	if len(snap.Bonus.Clears) > 0 {
		quota.Bonus.Clears = append(quota.Bonus.Clears, snap.Bonus.Clears...)
	}
	return quota
}

// upstreamRoutingBlock reports why upstream availability excludes auth from
// provider-wide routing ("disabled" or "cooldown"), and when a cooldown ends.
// It returns "" when the account is available.
func upstreamRoutingBlock(auth *coreauth.Auth, now time.Time) (string, time.Time) {
	if auth == nil {
		return "", time.Time{}
	}
	if auth.Disabled || auth.Status == coreauth.StatusDisabled {
		return "disabled", time.Time{}
	}
	var retryAt time.Time
	for _, view := range coreauth.CooldownSnapshotForAuth(auth, now) {
		if view.Scope != "credential" {
			continue
		}
		if view.RetryAt.After(retryAt) {
			retryAt = view.RetryAt
		}
	}
	if !retryAt.IsZero() {
		return "cooldown", retryAt
	}
	return "", time.Time{}
}

func routingStateLabel(auth *coreauth.Auth) string {
	if label := strings.TrimSpace(auth.Label); label != "" {
		return label
	}
	if name := strings.TrimSpace(auth.FileName); name != "" {
		return name
	}
	return auth.ID
}

func formatRoutingTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
