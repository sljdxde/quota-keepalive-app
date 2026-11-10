// Package providers ports Magpie's OpenCode provider runtime into QKA and
// owns provider/account persistence, login, quota, check-in, and the daily
// activity heartbeat (persisted under the historical "keepalive" id).
package providers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"qka/internal/accounts"
	"qka/internal/capabilities"
	"qka/internal/engine"
	"qka/internal/features"
	"qka/internal/privacy"
	"qka/internal/providerruntime"
	"qka/internal/requestguard"
	"qka/internal/workbuddy"
)

// Runtime is the provider host surface used by the service.
type Runtime interface {
	Providers(context.Context) ([]providerruntime.Provider, error)
	Prompt(context.Context, string, int, map[string]string) (providerruntime.Prompt, error)
	Authorize(context.Context, string, int, map[string]string, string) (providerruntime.Authorization, error)
	Finish(context.Context, string, string) (providerruntime.Saved, error)
	APIKey(context.Context, string, int, map[string]string, string, string) (providerruntime.Saved, error)
	Usage(context.Context, string, string) (providerruntime.Usage, error)
	Checkin(context.Context, string, string) (providerruntime.Checkin, error)
	Load(context.Context, string, string) (providerruntime.Loader, error)
	Fetch(context.Context, providerruntime.FetchRequest) (providerruntime.FetchResponse, error)
	SignOut(context.Context, string, string) error
	ReadAuth() ([]byte, error)
	RemoveAuth() error
	Close() error
}

// RuntimeFactory starts one short-lived provider host.
type RuntimeFactory func(context.Context) (Runtime, error)

type runtimeDiagnostics interface {
	LoadedPlugins() []providerruntime.LoadedPlugin
}

// ErrLoginPromptRequired reports that sign-in needs more input.
var ErrLoginPromptRequired = errors.New("providers: login prompt required")

// ErrNotExecuted reports an idempotent skip or a cooldown without claiming
// that the requested provider action ran.
var ErrNotExecuted = errors.New("providers: operation not executed")

// MethodView is a generic sign-in method.
type MethodView struct {
	Type        string `json:"type"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder,omitempty"`
	Keyless     bool   `json:"keyless,omitempty"`
}

// ModelView is the stable, non-secret model metadata shown by the GUI.
type ModelView struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Free    bool    `json:"free"`
	Context int     `json:"context"`
	Rate    float64 `json:"rate"`
}

// AccountView is one provider account and today's operation state.
type AccountView struct {
	Key             string          `json:"key"`
	MaskedIdentity  string          `json:"masked_identity"`
	Type            string          `json:"type"`
	Enabled         bool            `json:"enabled"`
	Models          []string        `json:"models"`
	TaskPreferences map[string]bool `json:"task_preferences"`
	Operations      []OperationView `json:"operations"`
}

// OperationView is one automatic/manual operation for an account.
type OperationView struct {
	Operation   string   `json:"operation"`
	State       string   `json:"state"`
	TaskEnabled bool     `json:"task_enabled"`
	Credits     *float64 `json:"credits,omitempty"`
	Balance     *float64 `json:"balance,omitempty"`
	Message     string   `json:"message,omitempty"`
}

// ProviderView is one provider and its accounts.
type ProviderView struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Icon     string        `json:"icon,omitempty"`
	Enabled  bool          `json:"enabled"`
	SignedIn bool          `json:"signed_in"`
	Usage    bool          `json:"usage"`
	Checkin  bool          `json:"checkin"`
	Activity bool          `json:"activity"`
	Methods  []MethodView  `json:"methods"`
	Models   []ModelView   `json:"models"`
	Accounts []AccountView `json:"accounts"`
}

// View is the complete provider page projection.
type View struct {
	Providers    []ProviderView                 `json:"providers"`
	PluginErrors []providerruntime.LoadedPlugin `json:"plugin_errors,omitempty"`
	AccountCount int                            `json:"account_count"`
	EnabledCount int                            `json:"enabled_count"`
}

// LoginView is exposed to the generic provider login dialog.
type LoginView struct {
	Provider     string                  `json:"provider,omitempty"`
	MethodIndex  int                     `json:"method_index,omitempty"`
	Status       string                  `json:"status"`
	Session      string                  `json:"session,omitempty"`
	URL          string                  `json:"url,omitempty"`
	Instructions string                  `json:"instructions,omitempty"`
	Method       string                  `json:"method,omitempty"`
	Prompt       *providerruntime.Prompt `json:"prompt,omitempty"`
	Saved        *providerruntime.Saved  `json:"saved,omitempty"`
	Message      string                  `json:"message,omitempty"`
}

// ModelTestView is one bounded model connectivity probe. It never includes
// upstream response text, tokens, headers, or other credential-bearing data.
type ModelTestView struct {
	Provider string `json:"provider"`
	Account  string `json:"account"`
	Model    string `json:"model"`
	OK       bool   `json:"ok"`
	Status   int    `json:"status"`
	Latency  int64  `json:"latency_ms"`
	Message  string `json:"message"`
}

// RunItemView is one redacted provider/account result in run history.
type RunItemView struct {
	RunID      string   `json:"run_id"`
	Provider   string   `json:"provider"`
	Account    string   `json:"account"`
	Action     string   `json:"action"`
	State      string   `json:"state"`
	Message    string   `json:"message,omitempty"`
	Credits    *float64 `json:"credits,omitempty"`
	Balance    *float64 `json:"balance,omitempty"`
	Model      string   `json:"model,omitempty"`
	ObservedAt string   `json:"observed_at"`
}

// QuotaReadingView is one bounded quota observation for trend display.
type QuotaReadingView struct {
	ObservedAt string                        `json:"observed_at"`
	Plan       string                        `json:"plan,omitempty"`
	Until      string                        `json:"until,omitempty"`
	Balance    string                        `json:"balance,omitempty"`
	Windows    []providerruntime.UsageWindow `json:"windows"`
}

// RunSummary is the redacted aggregate of one provider execution pass.
type RunSummary struct {
	Attempted      int `json:"attempted"`
	Succeeded      int `json:"succeeded"`
	NeedsAttention int `json:"needs_attention"`
	Skipped        int `json:"skipped"`
}

type runResult struct {
	Provider string
	Account  string
	Action   string
	State    string
	Executed bool
	Message  string
	Credits  *float64
	Balance  *float64
	Model    string
	Err      error
}

// Service owns provider runtime lifecycle and SQLite projections.
type Service struct {
	db          *sql.DB
	accounts    *accounts.Service
	credentials *capabilities.CredentialService
	engine      *engine.Engine
	factory     RuntimeFactory
	now         func() time.Time

	mu      sync.Mutex
	host    Runtime
	authRef capabilities.CredentialRef
	runMu   sync.Mutex
	// listMu serializes page loads: the app opens the check-in and app pages
	// together, and each one reads/synchronizes accounts. Letting two Lists
	// overlap doubles the runtime round-trip and races their SQLite writes,
	// which made the first paint come and go.
	listMu sync.Mutex
	// viewMu guards the cached page projection served by CachedListView.
	viewMu    sync.Mutex
	viewCache *View
	viewAt    time.Time
	// viewRefreshing 表示已有一次后台刷新在跑，避免过期时重复发起。
	viewRefreshing bool

	creditMu     sync.Mutex
	creditSynced map[string]bool

	riskGuard *requestguard.Guard

	// recoverAuth rebuilds the transient runtime auth view file from the
	// durable credential store. List calls it when the runtime reports no
	// accounts for a provider that still has stored accounts, which happens
	// when that file is missing or unreadable rather than genuinely empty.
	recoverAuth func(context.Context) error
}

// NewService constructs the provider service.
func NewService(
	db *sql.DB,
	accountService *accounts.Service,
	credentials *capabilities.CredentialService,
	eng *engine.Engine,
	factory RuntimeFactory,
	now func() time.Time,
) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{
		db: db, accounts: accountService, credentials: credentials, engine: eng,
		factory: factory, now: now,
		authRef: capabilities.CredentialRef{
			Target: "qka-provider", AccountID: "runtime", SourceID: "auth",
			Kind: capabilities.Managed,
		},
	}
}

// SetRiskGuard installs the shared manual/scheduled request gate. A nil guard
// preserves the service's historical behavior for focused unit tests.
func (s *Service) SetRiskGuard(guard *requestguard.Guard) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.riskGuard = guard
}

// SetRuntimeAuthRecovery installs a hook that rewrites the runtime auth view
// file from the durable credential store. The provider runtime reads its
// accounts from that volatile file; if it goes missing (or is momentarily
// unreadable) the runtime reports every provider as signed out with zero
// accounts. List uses this hook to recover the view in place instead of
// mistaking the outage for account deletion.
func (s *Service) SetRuntimeAuthRecovery(fn func(context.Context) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recoverAuth = fn
}

func (s *Service) runtime(ctx context.Context) (Runtime, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.host != nil {
		return s.host, nil
	}
	if s.factory == nil {
		return nil, errors.New("provider runtime is unavailable")
	}
	host, err := s.factory(ctx)
	if err != nil {
		return nil, err
	}
	s.host = host
	return host, nil
}

// Warm starts the provider runtime ahead of the first page load so the UI does
// not wait for the Bun host to come up while it has nothing to show.
func (s *Service) Warm(ctx context.Context) error {
	_, err := s.runtime(ctx)
	return err
}

func (s *Service) persistAuth(ctx context.Context, host Runtime) error {
	data, err := host.ReadAuth()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var value map[string]any
	if len(data) > 0 {
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
	}
	if len(value) == 0 {
		if s.credentials != nil {
			if err := s.credentials.DeleteManaged(ctx, s.authRef.Target, s.authRef.AccountID, s.authRef.SourceID); err != nil &&
				!errors.Is(err, capabilities.ErrSecretNotFound) {
				return err
			}
		}
		return nil
	}
	if s.credentials == nil {
		return errors.New("credential service is unavailable")
	}
	if err := s.credentials.Persist(ctx, s.authRef, string(data)); err != nil {
		return err
	}
	// The host keeps using this process-private auth view. Removing it now
	// would make the next List/Usage/Checkin call in the same session look
	// signed out. Close removes it after the final snapshot is persisted.
	return nil
}

func (s *Service) runtimeProvider(
	ctx context.Context,
	host Runtime,
	provider string,
) (providerruntime.Provider, error) {
	items, err := host.Providers(ctx)
	if err != nil {
		return providerruntime.Provider{}, err
	}
	for _, item := range items {
		if item.ID == provider {
			return item, nil
		}
	}
	return providerruntime.Provider{}, fmt.Errorf("未知供应商: %s", provider)
}

func checkOperationCapability(provider providerruntime.Provider, operation string) error {
	supported := false
	switch operation {
	case "checkin":
		supported = provider.Checkin
	case "keepalive":
		supported = providerActivitySupported(provider)
	case "refresh":
		supported = provider.Usage
	default:
		return fmt.Errorf("unsupported provider operation %q", operation)
	}
	if !supported {
		if operation == features.OperationActivity {
			return fmt.Errorf("%s 不支持每日活跃", provider.Name)
		}
		return fmt.Errorf("%s 不支持操作 %s", provider.Name, operation)
	}
	return nil
}

func (s *Service) verifyLogin(
	ctx context.Context,
	host Runtime,
	saved providerruntime.Saved,
) error {
	provider, err := s.runtimeProvider(ctx, host, saved.Provider)
	if err != nil {
		return fmt.Errorf("登录验证失败: %w", err)
	}
	if !provider.Usage {
		return nil
	}
	usage, err := host.Usage(ctx, saved.Provider, saved.Account)
	if err != nil {
		return fmt.Errorf("登录验证失败: %w", err)
	}
	if strings.TrimSpace(usage.Error) != "" {
		return fmt.Errorf("登录验证失败: %s", redactMessage(usage.Error))
	}
	return nil
}

// rollbackLogin removes a newly-created temporary sign-in and shuts down the
// host when read-only verification fails. Previously persisted accounts are
// preserved only after the host confirms the failed account was signed out.
func (s *Service) rollbackLogin(
	ctx context.Context,
	host Runtime,
	saved providerruntime.Saved,
) error {
	signOutErr := host.SignOut(ctx, saved.Provider, saved.Account)
	var persistErr error
	if signOutErr == nil {
		persistErr = s.persistAuth(ctx, host)
	}
	removeErr := host.RemoveAuth()
	closeErr := host.Close()
	s.mu.Lock()
	if s.host == host {
		s.host = nil
	}
	s.mu.Unlock()
	return errors.Join(signOutErr, persistErr, removeErr, closeErr)
}

// ensureTestableAccount 用于「模型测试」这类只读诊断：它**不要求**供应商或
// 账号处于启用状态——用户恰恰可能在停用之后用一次测试来确认登录态和模型
// 连通性。这里只校验账号确实存在，避免对不存在的账号发起外呼。
func (s *Service) ensureTestableAccount(ctx context.Context, provider, account string) error {
	account = strings.TrimSpace(account)
	if account == "" {
		return nil
	}
	var exists int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM accounts WHERE target = ? AND id = ?`,
		provider, account).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		// 账号尚未落库时交给后续 runtime 校验（foundAccount），
		// 这里不额外拦截，保持与旧诊断路径一致。
		return nil
	}
	return err
}

func (s *Service) ensureEnabled(ctx context.Context, provider, account string) error {
	var providerEnabled int
	err := s.db.QueryRowContext(ctx,
		`SELECT enabled FROM providers WHERE id = ?`, provider).Scan(&providerEnabled)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && providerEnabled != 1 {
		return fmt.Errorf("供应商已停用: %s", provider)
	}
	if strings.TrimSpace(account) == "" {
		return nil
	}
	var accountEnabled int
	err = s.db.QueryRowContext(ctx,
		`SELECT enabled FROM accounts WHERE target = ? AND id = ?`,
		provider, account).Scan(&accountEnabled)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && accountEnabled != 1 {
		return fmt.Errorf("账号已停用: %s", account)
	}
	return nil
}

// Close persists the temporary sign-in view and stops the provider host.
func (s *Service) Close() error {
	s.mu.Lock()
	host := s.host
	s.host = nil
	s.mu.Unlock()
	if host == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	persistErr := s.persistAuth(ctx, host)
	removeErr := host.RemoveAuth()
	closeErr := host.Close()
	return errors.Join(persistErr, removeErr, closeErr)
}

// List synchronizes provider metadata and accounts, then returns the page view.
func (s *Service) List(ctx context.Context) (View, error) {
	s.listMu.Lock()
	defer s.listMu.Unlock()
	host, err := s.runtime(ctx)
	if err != nil {
		return View{}, err
	}
	items, err := host.Providers(ctx)
	if err != nil {
		return View{}, err
	}
	if s.recoverAuth != nil && s.runtimeOmitsStoredAccounts(ctx, items) {
		// The runtime account view is built from a transient auth file; a
		// missing/unreadable file reports zero accounts. Rebuild it from the
		// durable credential store and retry before treating it as truth.
		if recoverErr := s.recoverAuth(ctx); recoverErr == nil {
			if retried, retryErr := host.Providers(ctx); retryErr == nil {
				items = retried
			}
		}
	}
	for _, provider := range items {
		if err := s.upsertProvider(ctx, provider); err != nil {
			return View{}, err
		}
		if err := s.syncAccounts(ctx, provider); err != nil {
			return View{}, err
		}
	}
	// A morning claim is recorded as provisional by the shared state machine.
	// When the plugin also handed back the explicit grant amount (credits differ
	// from the balance), that is the vendor's own receipt: promote it so the
	// account stops showing "待复核" for a check-in that already succeeded.
	if err := s.confirmEvidencedCheckins(ctx); err != nil {
		return View{}, err
	}
	view, err := s.view(ctx, items)
	if err != nil {
		return View{}, err
	}
	s.reconcileMissingCheckinGrowth(ctx, host, &view)
	if diagnostics, ok := host.(runtimeDiagnostics); ok {
		for _, plugin := range diagnostics.LoadedPlugins() {
			if plugin.Error != "" {
				view.PluginErrors = append(view.PluginErrors, plugin)
			}
		}
	}
	s.storeView(view)
	return view, nil
}

// providerViewTTL 是页面投影缓存的有效期。窗口打开时先拿缓存，过期就在后台
// 刷新；这样「打开页面」永远不会等一次运行时往返。
var providerViewTTL = 20 * time.Second

// CachedListView serves the last provider projection immediately and refreshes it
// in the background when stale. Opening the panel or the main window must not
// block on a runtime round trip (measured at ~0.8s even when warm).
func (s *Service) CachedListView(ctx context.Context) (View, error) {
	s.viewMu.Lock()
	cached := s.viewCache
	stale := s.viewAt.IsZero() || s.now().Sub(s.viewAt) > providerViewTTL
	if cached != nil {
		shouldRefresh := stale && !s.viewRefreshing
		if shouldRefresh {
			s.viewRefreshing = true
		}
		view := cloneView(*cached)
		s.viewMu.Unlock()
		if shouldRefresh {
			go func() {
				defer func() {
					s.viewMu.Lock()
					s.viewRefreshing = false
					s.viewMu.Unlock()
				}()
				_, _ = s.List(context.Background())
			}()
		}
		return view, nil
	}
	s.viewMu.Unlock()
	return s.List(ctx)
}

func (s *Service) storeView(view View) {
	s.viewMu.Lock()
	s.viewCache = &view
	s.viewAt = s.now()
	s.viewMu.Unlock()
}

// invalidateView drops the cached projection so the next page load reflects a
// write the user just made (login, toggle, delete, manual run).
func (s *Service) invalidateView() {
	s.viewMu.Lock()
	s.viewCache = nil
	s.viewAt = time.Time{}
	s.viewMu.Unlock()
}

func cloneView(view View) View {
	out := view
	out.Providers = append([]ProviderView(nil), view.Providers...)
	for i := range out.Providers {
		provider := &out.Providers[i]
		provider.Methods = append([]MethodView(nil), provider.Methods...)
		provider.Models = append([]ModelView(nil), provider.Models...)
		provider.Accounts = append([]AccountView(nil), provider.Accounts...)
		for j := range provider.Accounts {
			account := &provider.Accounts[j]
			account.Models = append([]string(nil), account.Models...)
			account.Operations = append([]OperationView(nil), account.Operations...)
			prefs := make(map[string]bool, len(account.TaskPreferences))
			for k, v := range account.TaskPreferences {
				prefs[k] = v
			}
			account.TaskPreferences = prefs
		}
	}
	out.PluginErrors = append([]providerruntime.LoadedPlugin(nil), view.PluginErrors...)
	return out
}

// runtimeOmitsStoredAccounts reports whether the runtime's provider list
// silently dropped accounts that the durable projection still holds. An empty
// account list for a provider that has stored accounts is the ambiguous case
// caused by a missing/unreadable auth view file; it must not be trusted as a
// sign-out. A provider that still reports at least one account is left to the
// normal per-account prune path.
func (s *Service) runtimeOmitsStoredAccounts(
	ctx context.Context, items []providerruntime.Provider,
) bool {
	for _, provider := range items {
		if len(provider.Accounts) > 0 {
			continue
		}
		stored, err := s.accounts.List(ctx, provider.ID)
		if err != nil || len(stored) == 0 {
			continue
		}
		return true
	}
	return false
}

// reconcileMissingCheckinGrowth asks a check-in plugin for today's grant when
// an older row only knew the account balance. The plugin check-in is
// idempotent after the day is in; the in-memory cache keeps this to one read
// per account and Beijing day for each app process.
func (s *Service) reconcileMissingCheckinGrowth(
	ctx context.Context, host Runtime, view *View,
) {
	day := engine.BeijingDay(s.now())
	for pi := range view.Providers {
		provider := &view.Providers[pi]
		if !provider.Enabled || !provider.Checkin {
			continue
		}
		for ai := range provider.Accounts {
			account := &provider.Accounts[ai]
			if !account.Enabled {
				continue
			}
			for oi := range account.Operations {
				op := &account.Operations[oi]
				if !isCheckinCreditState(op.State) ||
					(op.Credits != nil && op.Balance != nil && *op.Credits != *op.Balance) {
					continue
				}
				cacheKey := day + "\x00" + provider.ID + "\x00" + account.Key
				s.creditMu.Lock()
				if s.creditSynced == nil {
					s.creditSynced = map[string]bool{}
				}
				if s.creditSynced[cacheKey] {
					s.creditMu.Unlock()
					continue
				}
				s.creditSynced[cacheKey] = true
				s.creditMu.Unlock()

				// 这里同样经过共享风控闸门：日界键保证同一天只共享一次结果，
				// 若上游返回 429/401/验证码，guard 会为该账号进入冷却，避免
				// 状态页反复刷新时持续打上游。成功结果按天缓存，不增加外呼量。
				upstream, err := requestguard.Do(ctx, s.riskGuard, provider.ID, account.Key,
					"checkin_reconcile:"+day, 5*time.Minute,
					func() (providerruntime.Checkin, error) {
						return host.Checkin(ctx, provider.ID, account.Key)
					})
				if err != nil || upstream.Outcome != "claimed" && upstream.Outcome != "done" ||
					upstream.Credit <= 0 {
					continue
				}
				credit := upstream.Credit
				key := engine.Key{
					Target: provider.ID, AccountID: account.Key, Operation: engine.Checkin,
				}
				if err := s.engine.RecordUsage(ctx, key, &credit, nil); err != nil {
					continue
				}
				message := redactMessage(upstream.Message)
				if err := s.engine.ReconcileState(
					ctx, key, engine.State(op.State), message,
				); err != nil {
					continue
				}
				op.Credits = &credit
				op.Message = message
			}
		}
	}
}

// confirmEvidencedCheckins promotes today's provisional provider check-ins that
// carry an explicit grant amount. The shared state machine marks a success
// before Beijing noon as provisional (the vendor might still roll a morning
// claim back), and a provisional row is terminal, so nothing ever promotes it —
// the account kept showing "待复核" for a check-in that had already paid out.
// A recorded grant that differs from the account balance is the plugin's own
// receipt of +N credits, so it is safe to confirm. A receipt-less claim
// (credits unknown, or equal to the balance) stays provisional.
func (s *Service) confirmEvidencedCheckins(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE daily_ops
SET state = 'signed_confirmed',
    message = CASE
      WHEN message = '' THEN '已确认到账'
      ELSE message || ' · 已确认到账'
    END,
    updated_at = ?
WHERE operation = 'checkin' AND day_key = ?
  AND state = 'signed_provisional'
  AND credits_known = 1 AND credits > 0
  AND balance_known = 1 AND credits <> balance
  AND target IN (SELECT id FROM providers)`,
		s.now().UTC().Format(time.RFC3339), engine.BeijingDay(s.now()))
	return err
}

func isCheckinCreditState(state string) bool {
	switch state {
	case "signed_confirmed", "signed_provisional", "already":
		return true
	default:
		return false
	}
}

func (s *Service) upsertProvider(ctx context.Context, p providerruntime.Provider) error {
	models, _ := json.Marshal(p.Models)
	ts := s.now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO providers
  (id, name, plugin_spec, icon, enabled, usage, checkin, keepalive, models_json, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  name = excluded.name,
  plugin_spec = excluded.plugin_spec,
  icon = excluded.icon,
  usage = excluded.usage,
  checkin = excluded.checkin,
  keepalive = excluded.keepalive,
  models_json = excluded.models_json,
  updated_at = excluded.updated_at`,
		p.ID, p.Name, p.Spec, p.Icon, boolInt(defaultProviderEnabled(p.ID)),
		boolInt(p.Usage), boolInt(p.Checkin),
		boolInt(providerActivitySupported(p)), string(models), ts, ts)
	return err
}

// defaultProviderEnabled keeps bundled providers available for opt-in while
// making WorkBuddy the only provider enabled on a fresh installation.
func defaultProviderEnabled(providerID string) bool {
	return providerID == "workbuddy"
}

// providerActivitySupported reports whether the plugin exposes the signed
// runtime fetch path. Endpoint resolution is per account (loader.BaseURL is a
// valid fallback), so a missing static URL is not a reason to hide the task;
// the sender reports "no free model" with a visible reason instead.
func providerActivitySupported(p providerruntime.Provider) bool {
	return p.Fetch
}

func (s *Service) syncAccounts(ctx context.Context, p providerruntime.Provider) error {
	found := make([]accounts.DiscoveredAccount, 0, len(p.Accounts))
	runtimeKeys := make(map[string]bool, len(p.Accounts))
	for _, account := range p.Accounts {
		if err := s.reconcileAccountIdentity(ctx, p.ID, account); err != nil {
			return err
		}
		runtimeKeys[account.Key] = true
		found = append(found, accounts.DiscoveredAccount{
			ID:             account.Key,
			DisplayName:    p.Name,
			MaskedIdentity: maskIdentity(firstNonEmpty(account.AccountID, account.Hint, account.Key)),
			SourceID:       "plugin:" + account.Key,
			SourceKind:     accounts.KindManaged,
			Health:         accounts.HealthHealthy,
		})
	}
	if len(found) > 0 {
		if err := s.accounts.Discover(ctx, p.ID, found); err != nil {
			return err
		}
	}
	// An empty account list is not proof that the accounts were removed: the
	// runtime reads them from a volatile auth view file, so a missing or
	// momentarily unreadable file makes every provider report zero accounts.
	// Pruning on that would delete the durable projection and its history, so
	// only prune vanished plugin sources when the runtime actually reported at
	// least one account for this provider. List first attempts to rebuild the
	// auth view, so a genuine empty view still clears nothing already empty.
	if len(found) == 0 {
		return nil
	}
	return s.pruneMissingPluginSources(ctx, p.ID, runtimeKeys)
}

func (s *Service) reconcileAccountIdentity(
	ctx context.Context,
	provider string,
	account providerruntime.Account,
) error {
	identity := strings.TrimSpace(account.AccountID)
	if identity == "" || strings.TrimSpace(account.Key) == "" {
		return nil
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(provider+"\x00"+identity)))
	var previous string
	err := s.db.QueryRowContext(ctx, `
SELECT identity_hash FROM provider_account_identities
WHERE provider = ? AND slot_key = ?`,
		provider, account.Key).Scan(&previous)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = s.db.ExecContext(ctx, `
INSERT INTO provider_account_identities
  (provider, slot_key, identity_hash, updated_at)
VALUES (?, ?, ?, ?)`,
			provider, account.Key, hash, s.now().UTC().Format(time.RFC3339))
		return err
	}
	if err != nil || previous == hash {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range []string{
		`DELETE FROM logs WHERE target = ? AND account_id = ?`,
		`DELETE FROM quota_readings WHERE provider = ? AND account_id = ?`,
		`DELETE FROM run_items WHERE provider = ? AND account_id = ?`,
		`DELETE FROM activity_runs WHERE provider = ? AND account_id = ?`,
		`DELETE FROM daily_ops WHERE target = ? AND account_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, stmt, provider, account.Key); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
DELETE FROM runs
WHERE NOT EXISTS (SELECT 1 FROM run_items WHERE run_items.run_id = runs.id)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
DELETE FROM accounts WHERE target = ? AND id = ?`,
		provider, account.Key); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE provider_account_identities
SET identity_hash = ?, updated_at = ?
WHERE provider = ? AND slot_key = ?`,
		hash, s.now().UTC().Format(time.RFC3339), provider, account.Key); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) pruneMissingPluginSources(
	ctx context.Context,
	provider string,
	runtimeKeys map[string]bool,
) error {
	stored, err := s.accounts.List(ctx, provider)
	if err != nil {
		return err
	}
	const prefix = "plugin:"
	for _, account := range stored {
		for _, source := range account.Sources {
			if !strings.HasPrefix(source.ID, prefix) || runtimeKeys[strings.TrimPrefix(source.ID, prefix)] {
				continue
			}
			if err := s.accounts.RemoveSource(ctx, provider, account.ID, source.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) view(ctx context.Context, runtimeItems []providerruntime.Provider) (View, error) {
	states, err := s.providerPrefs(ctx)
	if err != nil {
		return View{}, err
	}
	view := View{Providers: []ProviderView{}}
	day := engine.BeijingDay(s.now())
	for _, p := range runtimeItems {
		enabled := true
		if value, ok := states[p.ID]; ok {
			enabled = value
		}
		pv := ProviderView{
			ID: p.ID, Name: p.Name, Icon: p.Icon, Enabled: enabled,
			SignedIn: p.SignedIn, Usage: p.Usage, Checkin: p.Checkin,
			Activity: providerActivitySupported(p),
			Methods:  []MethodView{}, Models: []ModelView{},
			Accounts: []AccountView{},
		}
		for _, method := range p.Methods {
			pv.Methods = append(pv.Methods, MethodView{
				Type: method.Type, Label: method.Label,
				Placeholder: method.Placeholder, Keyless: method.Keyless,
			})
		}
		for _, model := range p.Models {
			name := strings.TrimSpace(model.Name)
			if name == "" {
				name = model.ID
			}
			pv.Models = append(pv.Models, ModelView{
				ID: model.ID, Name: name, Free: model.Free,
				Context: model.Context, Rate: model.Rate,
			})
		}
		for _, account := range p.Accounts {
			stored, err := s.accounts.Get(ctx, p.ID, account.Key)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return View{}, err
			}
			av := AccountView{
				Key:            account.Key,
				MaskedIdentity: maskIdentity(firstNonEmpty(account.AccountID, account.Hint, account.Key)),
				Type:           account.Type, Enabled: stored.Enabled,
				Models:          append([]string(nil), account.Models...),
				TaskPreferences: map[string]bool{},
				Operations:      []OperationView{},
			}
			if len(av.Models) == 0 {
				for _, model := range pv.Models {
					av.Models = append(av.Models, model.ID)
				}
			}
			prefs, err := s.accounts.TaskPreferences(ctx, p.ID, account.Key)
			if err != nil {
				return View{}, err
			}
			for _, operation := range []string{"checkin", features.OperationActivity} {
				on, ok := prefs[operation]
				av.TaskPreferences[operation] = !ok || on
			}
			ops, err := s.operationViews(ctx, p.ID, account.Key, day, av.TaskPreferences)
			if err != nil {
				return View{}, err
			}
			av.Operations = ops
			pv.Accounts = append(pv.Accounts, av)
			view.AccountCount++
		}
		if enabled {
			view.EnabledCount++
		}
		view.Providers = append(view.Providers, pv)
	}
	sort.Slice(view.Providers, func(i, j int) bool {
		if view.Providers[i].SignedIn != view.Providers[j].SignedIn {
			return view.Providers[i].SignedIn
		}
		return view.Providers[i].Name < view.Providers[j].Name
	})
	return view, nil
}

func (s *Service) operationViews(
	ctx context.Context, provider, account, day string, prefs map[string]bool,
) ([]OperationView, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT operation, state, credits, balance, credits_known, balance_known, message
FROM daily_ops WHERE target = ? AND account_id = ? AND day_key = ?`,
		provider, account, day)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byOp := map[string]OperationView{}
	for rows.Next() {
		var op OperationView
		var credits, balance float64
		var creditsKnown, balanceKnown int
		if err := rows.Scan(&op.Operation, &op.State, &credits, &balance,
			&creditsKnown, &balanceKnown, &op.Message); err != nil {
			return nil, err
		}
		op.TaskEnabled = prefs[op.Operation]
		if creditsKnown == 1 {
			value := credits
			op.Credits = &value
		}
		if balanceKnown == 1 {
			value := balance
			op.Balance = &value
		}
		byOp[op.Operation] = op
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]OperationView, 0, 1)
	for _, operation := range []string{"checkin"} {
		op, ok := byOp[operation]
		if !ok {
			op = OperationView{Operation: operation, State: "", TaskEnabled: prefs[operation]}
		}
		out = append(out, op)
	}
	return out, nil
}

func (s *Service) providerPrefs(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, enabled FROM providers`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		var enabled int
		if err := rows.Scan(&id, &enabled); err != nil {
			return nil, err
		}
		out[id] = enabled == 1
	}
	return out, rows.Err()
}

// SetEnabled toggles one provider.
func (s *Service) SetEnabled(ctx context.Context, provider string, enabled bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE providers SET enabled = ?, updated_at = ? WHERE id = ?`,
		boolInt(enabled), s.now().UTC().Format(time.RFC3339), provider)
	if err != nil {
		s.writeAuditFailure(ctx, provider, "", "provider", err)
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		s.writeAuditFailure(ctx, provider, "", "provider", err)
		return err
	}
	if n == 0 {
		s.writeAuditFailure(ctx, provider, "", "provider", sql.ErrNoRows)
		return sql.ErrNoRows
	}
	s.invalidateView()
	s.writeAuditLog(ctx, provider, "", "provider", "completed", "info",
		"供应商已"+enabledLabel(enabled))
	return nil
}

// SetAccountEnabled toggles one account.
func (s *Service) SetAccountEnabled(ctx context.Context, provider, account string, enabled bool) error {
	if err := s.accounts.SetEnabled(ctx, provider, account, enabled); err != nil {
		s.writeAuditFailure(ctx, provider, account, "account", err)
		return err
	}
	s.invalidateView()
	s.writeAuditLog(ctx, provider, account, "account", "completed", "info",
		"账号已"+enabledLabel(enabled))
	return nil
}

// SetTaskEnabled toggles one account operation preference.
func (s *Service) SetTaskEnabled(ctx context.Context, provider, account, operation string, enabled bool) error {
	if err := s.accounts.SetTaskEnabled(ctx, provider, account, operation, enabled); err != nil {
		s.writeAuditFailure(ctx, provider, account, "automation", err)
		return err
	}
	s.invalidateView()
	s.writeAuditLog(ctx, provider, account, "automation", "completed", "info",
		auditOperationLabel(operation)+"自动执行已"+enabledLabel(enabled))
	return nil
}

// BeginLogin asks the plugin for its next prompt or starts OAuth.
func (s *Service) BeginLogin(
	ctx context.Context, provider string, method int, inputs map[string]string,
) (LoginView, error) {
	host, err := s.runtime(ctx)
	if err != nil {
		s.writeAuditFailure(ctx, provider, "", "login", err)
		return LoginView{}, err
	}
	prompt, err := host.Prompt(ctx, provider, method, inputs)
	if err != nil {
		s.writeAuditFailure(ctx, provider, "", "login", err)
		return LoginView{}, err
	}
	if prompt.Key != "" {
		return LoginView{Provider: provider, MethodIndex: method, Status: "prompt", Prompt: &prompt}, nil
	}
	providers, err := host.Providers(ctx)
	if err != nil {
		s.writeAuditFailure(ctx, provider, "", "login", err)
		return LoginView{}, err
	}
	methodType := ""
	for _, item := range providers {
		if item.ID != provider {
			continue
		}
		if method >= 0 && method < len(item.Methods) {
			methodType = item.Methods[method].Type
		}
		break
	}
	if methodType == "api" {
		return LoginView{
			Provider: provider, MethodIndex: method, Status: "api_key",
			Method: "api", Message: "请输入 API Key 完成登录",
		}, nil
	}
	a, err := host.Authorize(ctx, provider, method, inputs, providerruntime.NewAccount)
	if err != nil {
		s.writeAuditFailure(ctx, provider, "", "login", err)
		return LoginView{}, err
	}
	return LoginView{
		Provider: provider, MethodIndex: method, Status: "authorized", Session: a.Session, URL: a.URL,
		Instructions: a.Instructions, Method: a.Method,
	}, nil
}

// FinishLogin completes the OAuth callback and persists the sign-in.
func (s *Service) FinishLogin(ctx context.Context, session, code string) (LoginView, error) {
	host, err := s.runtime(ctx)
	if err != nil {
		s.writeAuditFailure(ctx, "", "", "login", err)
		return LoginView{}, err
	}
	saved, err := host.Finish(ctx, session, code)
	if err != nil {
		s.writeAuditFailure(ctx, saved.Provider, saved.Account, "login", err)
		return LoginView{}, err
	}
	if err := s.verifyLogin(ctx, host, saved); err != nil {
		s.writeAuditFailure(ctx, saved.Provider, saved.Account, "login", err)
		return LoginView{}, errors.Join(err, s.rollbackLogin(ctx, host, saved))
	}
	if err := s.persistAuth(ctx, host); err != nil {
		s.writeAuditFailure(ctx, saved.Provider, saved.Account, "login", err)
		return LoginView{}, err
	}
	_ = s.clearStaleFailure(ctx, saved.Provider, saved.Account)
	s.invalidateView()
	s.writeAuditLog(ctx, saved.Provider, saved.Account, "login", "completed", "info", "登录完成")
	return LoginView{Provider: saved.Provider, Status: "verified", Saved: &saved, Message: "登录完成"}, nil
}

// LoginAPIKey signs in with an API key and persists the sign-in.
func (s *Service) LoginAPIKey(
	ctx context.Context, provider string, method int, inputs map[string]string, key string,
) (LoginView, error) {
	host, err := s.runtime(ctx)
	if err != nil {
		s.writeAuditFailure(ctx, provider, "", "login", err)
		return LoginView{}, err
	}
	saved, err := host.APIKey(ctx, provider, method, inputs, key, providerruntime.NewAccount)
	if err != nil {
		s.writeAuditFailure(ctx, provider, saved.Account, "login", err)
		return LoginView{}, err
	}
	if err := s.verifyLogin(ctx, host, saved); err != nil {
		s.writeAuditFailure(ctx, saved.Provider, saved.Account, "login", err)
		return LoginView{}, errors.Join(err, s.rollbackLogin(ctx, host, saved))
	}
	if err := s.persistAuth(ctx, host); err != nil {
		s.writeAuditFailure(ctx, saved.Provider, saved.Account, "login", err)
		return LoginView{}, err
	}
	_ = s.clearStaleFailure(ctx, saved.Provider, saved.Account)
	s.invalidateView()
	s.writeAuditLog(ctx, saved.Provider, saved.Account, "login", "completed", "info", "登录完成")
	return LoginView{Provider: saved.Provider, Status: "verified", Saved: &saved, Message: "登录完成"}, nil
}

// clearStaleFailure drops today's failed projection for a freshly signed-in
// account. A check-in that failed on an expired credential leaves a
// failed_final/manual row; after the user signs in again that row is stale and
// would keep the account card red until the next successful run. Successful
// states (already/signed/…) are left untouched so real progress is kept.
func (s *Service) clearStaleFailure(ctx context.Context, provider, account string) error {
	if strings.TrimSpace(account) == "" {
		return nil
	}
	day := engine.BeijingDay(s.now())
	_, err := s.db.ExecContext(ctx, `
DELETE FROM daily_ops
WHERE target = ? AND account_id = ? AND day_key = ?
  AND state IN ('failed_retryable', 'failed_final', 'manual_required')`,
		provider, account, day)
	return err
}

// clearAuthFailure drops today's failure row only when it was an authentication
// failure (expired AK / IAM refused / sign-in lost). A successful usage refresh
// proves the sign-in works again, so that stale error is safe to clear; a
// check-in that failed for another reason (ineligible, upstream 5xx, …) is left
// so it is not silently hidden.
func (s *Service) clearAuthFailure(ctx context.Context, provider, account string) error {
	if strings.TrimSpace(account) == "" {
		return nil
	}
	day := engine.BeijingDay(s.now())
	_, err := s.db.ExecContext(ctx, `
DELETE FROM daily_ops
WHERE target = ? AND account_id = ? AND day_key = ?
  AND state IN ('failed_retryable', 'failed_final', 'manual_required')
  AND (
    message LIKE '%APIG.0301%' OR message LIKE '%Incorrect IAM authentication%'
    OR message LIKE '%AK access failed%' OR message LIKE '%401%'
    OR message LIKE '%登录已失效%' OR message LIKE '%not signed in%'
  )`,
		provider, account, day)
	return err
}

// DeleteAccount signs out and removes only QKA's account projection.
func (s *Service) DeleteAccount(ctx context.Context, provider, account string) error {
	host, err := s.runtime(ctx)
	if err != nil {
		s.writeAuditFailure(ctx, provider, account, "account", err)
		return err
	}
	if err := host.SignOut(ctx, provider, account); err != nil {
		s.writeAuditFailure(ctx, provider, account, "account", err)
		return err
	}
	if err := s.persistAuth(ctx, host); err != nil {
		s.writeAuditFailure(ctx, provider, account, "account", err)
		return err
	}
	if err := s.accounts.Delete(ctx, provider, account); err != nil {
		s.writeAuditFailure(ctx, provider, account, "account", err)
		return err
	}
	s.invalidateView()
	s.writeAuditLog(ctx, provider, account, "account", "completed", "info", "账号已删除")
	return nil
}

// RefreshUsage reads and stores one account's usage through the shared safety
// gate. Repeated manual clicks share the recent result instead of multiplying
// upstream traffic.
func (s *Service) RefreshUsage(
	ctx context.Context, provider, account string,
) (providerruntime.Usage, error) {
	return requestguard.Do(ctx, s.riskGuard, provider, account, "refresh",
		15*time.Second, func() (providerruntime.Usage, error) {
			return s.refreshUsage(ctx, provider, account)
		})
}

func (s *Service) refreshUsage(ctx context.Context, provider, account string) (providerruntime.Usage, error) {
	if err := s.ensureEnabled(ctx, provider, account); err != nil {
		return providerruntime.Usage{}, err
	}
	host, err := s.runtime(ctx)
	if err != nil {
		return providerruntime.Usage{}, err
	}
	runtimeProvider, err := s.runtimeProvider(ctx, host, provider)
	if err != nil {
		return providerruntime.Usage{}, err
	}
	if !runtimeProvider.Usage {
		return providerruntime.Usage{}, fmt.Errorf("该供应商不支持额度查询")
	}
	usage, err := host.Usage(ctx, provider, account)
	persistErr := s.persistAuth(ctx, host)
	if err != nil {
		return providerruntime.Usage{}, errors.Join(err, persistErr)
	}
	if strings.TrimSpace(usage.Error) != "" {
		return providerruntime.Usage{}, errors.Join(
			fmt.Errorf("额度查询失败: %s", redactMessage(usage.Error)),
			persistErr,
		)
	}
	if _, _, err := s.recordUsage(ctx, provider, account, usage); err != nil {
		return providerruntime.Usage{}, errors.Join(err, persistErr)
	}
	return usage, persistErr
}

// TestModel sends the smallest supported chat request to one provider model.
// It reports only connectivity metadata so the GUI never receives upstream
// response bodies, headers, credentials, or prompt echoes.
func (s *Service) TestModel(
	ctx context.Context, provider, account, modelID string,
) (view ModelTestView, err error) {
	modelID = strings.TrimSpace(modelID)
	defer func() {
		s.writeModelTestAuditLog(context.WithoutCancel(ctx), provider, account, modelID, view, err)
	}()
	// 去重键必须包含模型 ID：同一账号 30 秒内换一个模型测试是两个不同的
	// 上游请求；若只按 provider+account+operation 去重，会把上一个模型的
	// 成功结果当成新模型的结论返回（GUI 会显示"模型可用"但实际没测）。
	operation := "model_test"
	if modelID != "" {
		operation += ":" + modelID
	}
	view, err = requestguard.Do(ctx, s.riskGuard, provider, account, operation,
		30*time.Second, func() (ModelTestView, error) {
			return s.testModel(ctx, provider, account, modelID)
		})
	if err == nil && s.riskGuard != nil {
		s.riskGuard.ObserveStatus(provider, account, view.Status)
	}
	return view, err
}

func (s *Service) testModel(
	ctx context.Context, provider, account, modelID string,
) (view ModelTestView, err error) {
	account = strings.TrimSpace(account)
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return ModelTestView{}, errors.New("请选择要测试的模型")
	}
	if err := s.ensureTestableAccount(ctx, provider, account); err != nil {
		return ModelTestView{}, err
	}
	s.runMu.Lock()
	defer s.runMu.Unlock()

	host, err := s.runtime(ctx)
	if err != nil {
		return ModelTestView{}, err
	}
	runtimeProvider, err := s.runtimeProvider(ctx, host, provider)
	if err != nil {
		return ModelTestView{}, err
	}
	if strings.TrimSpace(account) == "" {
		if len(runtimeProvider.Accounts) == 0 {
			return ModelTestView{}, errors.New("该供应商尚未添加账号")
		}
		account = runtimeProvider.Accounts[0].Key
		if err := s.ensureTestableAccount(ctx, provider, account); err != nil {
			return ModelTestView{}, err
		}
	}
	foundAccount := false
	for _, item := range runtimeProvider.Accounts {
		if item.Key == account {
			foundAccount = true
			break
		}
	}
	if !foundAccount {
		return ModelTestView{}, errors.New("该供应商账号不存在，请刷新后重试")
	}
	loader, err := host.Load(ctx, provider, account)
	if err != nil {
		return ModelTestView{}, fmt.Errorf("读取供应商登录态失败: %w", err)
	}
	model, endpoint, err := modelTestTarget(runtimeProvider, account, modelID, loader.BaseURL)
	if err != nil {
		return ModelTestView{}, err
	}

	npm := strings.ToLower(firstNonEmpty(model.NPM, runtimeProvider.NPM))
	protocol := "openai"
	if strings.Contains(npm, "anthropic") {
		protocol = "anthropic"
	}
	body := map[string]any{
		"model":      firstNonEmpty(model.ID, modelID),
		"messages":   []map[string]string{{"role": "user", "content": "Reply OK."}},
		"max_tokens": 4,
		"stream":     false,
	}
	headers := map[string]string{"content-type": "application/json"}
	if protocol == "anthropic" {
		body["system"] = "You are a connectivity check."
		headers["anthropic-version"] = "2023-06-01"
	}
	// body 只包含字符串/整数/字符串切片，json.Marshal 不会失败；原来不可能
	// 走到的错误分支已删除，避免为了覆盖率硬造测试。
	encoded, _ := json.Marshal(body)

	started := time.Now()
	response, fetchErr := host.Fetch(ctx, providerruntime.FetchRequest{
		Provider: provider, Account: account, URL: endpoint, Method: "POST",
		Headers: headers, Body: string(encoded),
	})
	if fetchErr == nil && activityNeedsStream(response.Status, response.Body) {
		body["stream"] = true
		encoded, _ = json.Marshal(body)
		response, fetchErr = host.Fetch(ctx, providerruntime.FetchRequest{
			Provider: provider, Account: account, URL: endpoint, Method: "POST",
			Headers: headers, Body: string(encoded),
		})
	}
	// time.Since 使用单调时钟，不可能为负；原来的负数夹紧分支是死代码，已删除。
	latency := time.Since(started).Milliseconds()
	persistErr := s.persistAuth(ctx, host)
	view = ModelTestView{
		Provider: provider, Account: maskIdentity(account), Model: modelID,
		Status: response.Status, Latency: latency,
	}
	if fetchErr != nil {
		view.Message = "模型请求失败，请检查网络或重新登录后重试"
		return view, persistErr
	}
	if response.Status < 200 || response.Status >= 300 {
		view.Message = modelTestStatusMessage(response.Status)
		return view, persistErr
	}
	if !hasReply(response.Body) {
		view.Message = "供应商返回成功，但没有模型回复"
		return view, persistErr
	}
	view.OK = true
	view.Message = "模型可用"
	return view, persistErr
}

func modelTestStatusMessage(status int) string {
	switch status {
	case 400:
		return "请求被供应商拒绝，请检查模型参数"
	case 401, 403:
		return "鉴权失败，请重新登录该账号"
	case 404:
		return "模型或接口不存在"
	case 408:
		return "供应商响应超时"
	case 429:
		return "请求被限流，请稍后再试"
	}
	if status >= 500 {
		return "供应商服务暂时异常"
	}
	return fmt.Sprintf("模型测试返回 HTTP %d", status)
}

func modelTestTarget(
	provider providerruntime.Provider,
	account, modelID, fallbackBaseURL string,
) (providerruntime.Model, string, error) {
	var selected providerruntime.Model
	for _, model := range provider.Models {
		if model.ID == modelID {
			selected = model
			break
		}
	}
	if selected.ID == "" {
		return providerruntime.Model{}, "", errors.New("该供应商没有此模型")
	}
	for _, item := range provider.Accounts {
		if item.Key != account {
			continue
		}
		if len(item.Models) > 0 {
			allowed := false
			for _, id := range item.Models {
				if id == modelID {
					allowed = true
					break
				}
			}
			if !allowed {
				return providerruntime.Model{}, "", errors.New("当前账号不能使用该模型")
			}
		}
		break
	}
	base := strings.TrimRight(firstNonEmpty(selected.APIURL, provider.APIURL, fallbackBaseURL), "/")
	if base == "" {
		return providerruntime.Model{}, "", errors.New("该模型没有可用的接口地址")
	}
	npm := strings.ToLower(firstNonEmpty(selected.NPM, provider.NPM))
	suffix := "/chat/completions"
	if strings.Contains(npm, "anthropic") {
		suffix = "/messages"
	}
	if !strings.HasSuffix(base, suffix) {
		base += suffix
	}
	return selected, base, nil
}

func (s *Service) recordUsage(
	ctx context.Context,
	provider, account string,
	usage providerruntime.Usage,
) (*float64, *float64, error) {
	if strings.TrimSpace(usage.Error) != "" {
		return nil, nil, fmt.Errorf("额度查询失败: %s", redactMessage(usage.Error))
	}
	windows, _ := json.Marshal(usage.Windows)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO quota_readings
  (provider, account_id, observed_at, plan, until, balance, windows_json)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		provider, account, s.now().UTC().Format(time.RFC3339),
		usage.Plan, usage.Until, usage.Balance, string(windows))
	if err != nil {
		return nil, nil, err
	}
	credits, balance := usageValues(usage)
	key := engine.Key{Target: provider, AccountID: account, Operation: engine.Checkin}
	if err := s.engine.RecordUsage(ctx, key, credits, balance); err != nil {
		return nil, nil, err
	}
	return credits, balance, nil
}

// HasProvider reports whether target belongs to the provider runtime. It is
// used by legacy management entry points so provider credentials cannot be
// orphaned by falling back to a metadata-only delete.
func (s *Service) HasProvider(ctx context.Context, target string) (bool, error) {
	var found int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM providers WHERE id = ?`, target).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// Run executes checkin or keepalive through the shared daily state machine.
func (s *Service) Run(
	ctx context.Context, provider, account, operation string,
) (runErr error) {
	defer s.invalidateView()
	recorded := false
	defer func() {
		if runErr != nil && !recorded {
			s.writeAuditFailure(ctx, provider, account, operation, runErr)
		}
	}()
	s.runMu.Lock()
	defer s.runMu.Unlock()

	if err := s.ensureEnabled(ctx, provider, account); err != nil {
		return err
	}
	host, err := s.runtime(ctx)
	if err != nil {
		return err
	}
	runtimeProvider, err := s.runtimeProvider(ctx, host, provider)
	if err != nil {
		return err
	}
	if err := checkOperationCapability(runtimeProvider, operation); err != nil {
		return err
	}
	started := s.now().UTC().Format(time.RFC3339)
	runID := newRunID(s.now())
	result := s.runOne(ctx, host, provider, account, operation)
	if recordErr := s.recordRun(ctx, runID, started, operation, []runResult{result}); recordErr != nil {
		return recordErr
	}
	recorded = true
	if result.Err != nil {
		return result.Err
	}
	if !result.Executed {
		if operation == features.OperationActivity {
			switch result.State {
			case string(engine.Completed):
				return errors.New("今日已活跃，无需重复发送")
			case string(engine.FailedRetryable):
				return errors.New("每日活跃失败后的冷却中，请稍后重试")
			case string(engine.FailedFinal):
				return errors.New("今日每日活跃已失败，明天会自动重试")
			}
		}
		return fmt.Errorf("%w（当前状态：%s）", ErrNotExecuted, result.State)
	}
	// 手动「立即活跃」必须把执行失败如实返回，否则 UI 会把限流/无免费模型
	// 误报成完成。自动执行走 runOne，不经过这里，仍由 RunSummary 统计。
	if operation == features.OperationActivity && result.State != string(engine.Completed) {
		return errors.New(firstNonEmpty(result.Message, "每日活跃失败"))
	}
	return nil
}

// RunAll executes the provider-backed accounts selected by action. The action
// values are all/checkin/activity (legacy "keepalive" is normalized). One
// account failure never aborts the rest.
func (s *Service) RunAll(ctx context.Context, action string, exclude map[string]bool) (RunSummary, error) {
	if !features.ValidAction(action) {
		return RunSummary{}, fmt.Errorf("unsupported provider action %q", action)
	}
	action = features.NormalizeAction(action)
	s.runMu.Lock()
	defer s.runMu.Unlock()

	view, err := s.List(ctx)
	if err != nil {
		return RunSummary{}, err
	}
	return s.runAllViewLocked(ctx, view, action, exclude)
}

// RunAllView executes an already synchronized provider view. App uses this to
// separate adapter and provider execution domains without reading plugins
// twice in one scheduled run.
func (s *Service) RunAllView(
	ctx context.Context,
	view View,
	action string,
	exclude map[string]bool,
) (RunSummary, error) {
	if !features.ValidAction(action) {
		return RunSummary{}, fmt.Errorf("unsupported provider action %q", action)
	}
	action = features.NormalizeAction(action)
	s.runMu.Lock()
	defer s.runMu.Unlock()
	return s.runAllViewLocked(ctx, view, action, exclude)
}

func (s *Service) runAllViewLocked(
	ctx context.Context,
	view View,
	action string,
	exclude map[string]bool,
) (RunSummary, error) {
	host, err := s.runtime(ctx)
	if err != nil {
		return RunSummary{}, err
	}

	started := s.now().UTC().Format(time.RFC3339)
	runID := newRunID(s.now())
	summary := RunSummary{}
	results := []runResult{}
	var failures []error
	for _, provider := range view.Providers {
		if exclude[provider.ID] || !provider.Enabled {
			continue
		}
		for _, account := range provider.Accounts {
			if !account.Enabled {
				continue
			}
			operations := providerOperations(provider, account, action)
			if len(operations) == 0 {
				summary.Skipped++
				results = append(results, runResult{
					Provider: provider.ID, Account: account.Key, Action: operationLabel(action, provider),
					State: string(engine.None), Message: "没有启用的自动任务",
				})
				continue
			}
			for _, operation := range operations {
				result := s.runOne(ctx, host, provider.ID, account.Key, operation)
				results = append(results, result)
				if result.Err != nil {
					failures = append(failures, fmt.Errorf("%s/%s/%s: %w",
						provider.ID, account.Key, operation, result.Err))
					summary.NeedsAttention++
					continue
				}
				if !result.Executed || result.State == string(engine.FailedRetryable) ||
					result.State == string(engine.FailedFinal) ||
					result.State == string(engine.ManualRequired) {
					summary.Attempted++
					if !result.Executed {
						summary.Skipped++
						summary.Attempted--
					}
					if result.State == string(engine.FailedRetryable) ||
						result.State == string(engine.FailedFinal) ||
						result.State == string(engine.ManualRequired) {
						summary.NeedsAttention++
					}
					continue
				}
				summary.Attempted++
				summary.Succeeded++
			}
		}
	}
	if err := s.recordRun(ctx, runID, started, action, results); err != nil {
		return summary, err
	}
	return summary, errors.Join(failures...)
}

// providerOperations expands an automation action into the concrete operations
// for one account. "all" runs check-in and the daily activity heartbeat
// independently; "activity" maps to the persisted "keepalive" operation id.
func providerOperations(provider ProviderView, account AccountView, action string) []string {
	action = features.NormalizeAction(action)
	wantCheckin := action == features.ActionAll || action == features.ActionCheckin
	wantActivity := action == features.ActionAll || action == features.ActionActivity
	out := []string{}
	if wantCheckin && provider.Checkin && enabledPref(account.TaskPreferences, features.OperationCheckin) {
		out = append(out, features.OperationCheckin)
	}
	if wantActivity && provider.Activity && enabledPref(account.TaskPreferences, features.OperationActivity) {
		out = append(out, features.OperationActivity)
	}
	return out
}

// RunActivity executes the daily activity heartbeat for one account. It still
// obeys the shared Beijing-day idempotency and retry cooldown rules.
func (s *Service) RunActivity(ctx context.Context, provider, account string) error {
	return s.Run(ctx, provider, account, features.OperationActivity)
}

// SetActivityEnabled toggles the per-account daily activity task.
func (s *Service) SetActivityEnabled(
	ctx context.Context, provider, account string, enabled bool,
) error {
	return s.SetTaskEnabled(ctx, provider, account, features.OperationActivity, enabled)
}

func enabledPref(prefs map[string]bool, operation string) bool {
	value, ok := prefs[operation]
	return !ok || value
}

func operationLabel(action string, provider ProviderView) string {
	return features.NormalizeAction(action)
}

func (s *Service) runOne(
	ctx context.Context, host Runtime, provider, account, operation string,
) runResult {
	result := runResult{Provider: provider, Account: account, Action: operation}
	switch operation {
	case "checkin":
		var obs engine.Observation
		var claimedCredit float64
		obs, err := s.engine.Run(ctx,
			engine.Key{Target: provider, AccountID: account, Operation: engine.Checkin},
			func(ctx context.Context, _ engine.Key) (engine.ExternalResult, error) {
				upstream, guardErr := requestguard.Do(ctx, s.riskGuard,
					provider, account, "checkin", time.Minute,
					func() (providerruntime.Checkin, error) {
						return host.Checkin(ctx, provider, account)
					})
				if guardErr != nil {
					return engine.ExternalResult{Kind: engine.RRetryable, Message: guardErr.Error()}, nil
				}
				if s.riskGuard != nil {
					s.riskGuard.ObserveText(provider, account, upstream.Message)
					if upstream.Outcome == "captcha" {
						s.riskGuard.Record(provider, account, requestguard.ReasonChallenge)
					}
				}
				claimedCredit = upstream.Credit
				kind := kindForCheckin(upstream.Outcome)
				return engine.ExternalResult{Kind: kind, Message: upstream.Message}, nil
			})
		if err != nil {
			result.Err = err
			return result
		}
		result.State = string(obs.State)
		result.Executed = obs.Executed
		result.Message = redactMessage(obs.Message)
		// A successful check-in should immediately show the account's current
		// quota when the plugin exposes usage. Failure here never rewrites the
		// check-in result.
		if obs.State == engine.SignedConfirmed || obs.State == engine.SignedProvisional ||
			obs.State == engine.Already {
			usage, usageErr := requestguard.Do(ctx, s.riskGuard,
				provider, account, "refresh", 15*time.Second,
				func() (providerruntime.Usage, error) {
					return host.Usage(ctx, provider, account)
				})
			if usageErr == nil && strings.TrimSpace(usage.Error) == "" {
				result.Credits, result.Balance, usageErr = s.recordUsage(ctx, provider, account, usage)
			}
			if usageErr == nil {
				if claimedCredit > 0 {
					value := claimedCredit
					result.Credits = &value
				}
				_ = s.engine.RecordUsage(ctx,
					engine.Key{Target: provider, AccountID: account, Operation: engine.Checkin},
					result.Credits, result.Balance)
			} else if claimedCredit > 0 {
				value := claimedCredit
				result.Credits = &value
				_ = s.engine.RecordUsage(ctx,
					engine.Key{Target: provider, AccountID: account, Operation: engine.Checkin},
					result.Credits, nil)
			}
			if result.Balance == nil {
				message := appendMessage(result.Message,
					"当前额度暂不可读，可点击「刷新额度」重试")
				result.Message = message
				obs.Message = message
				_ = s.engine.ReconcileState(ctx, engine.Key{
					Target: provider, AccountID: account, Operation: engine.Checkin,
				}, obs.State, message)
			}
			// A morning claim lands as provisional; when the plugin also gave an
			// explicit grant, confirm it right here so the stored state is final
			// without waiting for the next GUI list.
			_ = s.confirmEvidencedCheckins(ctx)
		}
	case "keepalive":
		// 每日活跃：通过 provider 插件通道发一条真实但最小的对话消息。
		// operation id 仍是 keepalive（旧数据兼容），对外文案统一叫「每日活跃」。
		var outcome activityOutcome
		var obs engine.Observation
		obs, err := s.engine.Run(ctx,
			engine.Key{Target: provider, AccountID: account, Operation: engine.Activity},
			func(ctx context.Context, _ engine.Key) (engine.ExternalResult, error) {
				var execution engine.ExternalResult
				execution, outcome = s.activity(ctx, host, provider, account)
				return execution, nil
			})
		if err != nil {
			result.Err = err
			return result
		}
		result.State = string(obs.State)
		result.Executed = obs.Executed
		result.Message = redactMessage(obs.Message)
		result.Model = outcome.Model
		if obs.Executed {
			outcome.State = activityStateForResult(obs.State, outcome.State)
			if recordErr := s.recordActivityRun(ctx, provider, account, obs.Day, outcome); recordErr != nil {
				result.Err = recordErr
				return result
			}
		}
		if obs.Executed && obs.State == engine.Completed {
			usage, usageErr := requestguard.Do(ctx, s.riskGuard,
				provider, account, "refresh", 15*time.Second,
				func() (providerruntime.Usage, error) {
					return host.Usage(ctx, provider, account)
				})
			if usageErr == nil && strings.TrimSpace(usage.Error) == "" {
				// 每日活跃不是领取动作：只更新余额展示，不把额度记成「今日已领取」。
				_, result.Balance, _ = s.recordUsage(ctx, provider, account, usage)
			}
		}
	case "refresh":
		usage, err := s.RefreshUsage(ctx, provider, account)
		if err != nil {
			result.Err = err
			return result
		}
		result.State = "completed"
		result.Executed = true
		result.Credits, result.Balance = usageValues(usage)
		// A live usage read proves the sign-in works again; drop today's stale
		// auth failure so the card stops showing a pre-re-login error.
		_ = s.clearAuthFailure(ctx, provider, account)
	default:
		result.Err = fmt.Errorf("unsupported provider operation %q", operation)
	}
	return result
}

func usageValues(usage providerruntime.Usage) (*float64, *float64) {
	var credits, balance *float64
	for _, window := range usage.Windows {
		remaining := window.Balance
		if remaining == nil && window.Limit > 0 {
			value := window.Limit - window.Amount
			if value < 0 {
				value = 0
			}
			remaining = &value
		}
		if remaining == nil {
			continue
		}
		value := *remaining
		if balance == nil {
			balance = &value
		}
		if credits == nil {
			credits = &value
		}
	}
	if balance == nil {
		if value, ok := parseUsageBalance(usage.Balance); ok {
			balance = &value
			credits = &value
		}
	}
	return credits, balance
}

var usageBalancePattern = regexp.MustCompile(`[-+]?[0-9][0-9,]*(?:\.[0-9]+)?`)

func parseUsageBalance(value string) (float64, bool) {
	match := usageBalancePattern.FindString(strings.TrimSpace(value))
	if match == "" {
		return 0, false
	}
	number, err := strconv.ParseFloat(strings.ReplaceAll(match, ",", ""), 64)
	if err != nil {
		return 0, false
	}
	if number < 0 {
		number = 0
	}
	return number, true
}

func appendMessage(message, suffix string) string {
	message = strings.TrimSpace(message)
	suffix = strings.TrimSpace(suffix)
	if suffix == "" {
		return message
	}
	if message == "" {
		return suffix
	}
	return message + "；" + suffix
}

func newRunID(now time.Time) string {
	var suffix [6]byte
	_, _ = rand.Read(suffix[:])
	return fmt.Sprintf("%s-%x", now.UTC().Format("20060102T150405"), suffix)
}

func (s *Service) recordRun(
	ctx context.Context, runID, started, action string, results []runResult,
) error {
	if s.db == nil {
		return nil
	}
	attempted := 0
	succeeded := 0
	needsAttention := 0
	skipped := 0
	state := "completed"
	for _, result := range results {
		if result.Err != nil || result.State == string(engine.FailedRetryable) ||
			result.State == string(engine.FailedFinal) ||
			result.State == string(engine.ManualRequired) {
			needsAttention++
			state = "failed_retryable"
		}
		if result.Executed {
			attempted++
			if result.Err == nil && result.State != string(engine.FailedRetryable) &&
				result.State != string(engine.FailedFinal) &&
				result.State != string(engine.ManualRequired) {
				succeeded++
			}
		} else if result.State != "" {
			skipped++
		}
	}
	if needsAttention > 0 && attempted == 0 {
		state = "failed_final"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	finished := s.now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO runs
  (id, started_at, finished_at, action, state, attempted, succeeded, needs_attention, skipped, message)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		runID, started, finished, action, state, attempted, succeeded,
		needsAttention, skipped, ""); err != nil {
		return err
	}
	for _, result := range results {
		message := redactMessage(result.Message)
		if result.Err != nil {
			message = "执行失败，详情请查看脱敏日志"
		}
		_, err := tx.ExecContext(ctx, `
INSERT INTO run_items
  (run_id, provider, account_id, action, state, message, credits, balance, model, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			runID, result.Provider, result.Account, result.Action, result.State,
			message, nullableFloat(result.Credits), nullableFloat(result.Balance),
			result.Model, finished)
		if err != nil {
			return err
		}
		logState, level, logMessage := runResultAudit(result)
		if err := s.insertLog(ctx, tx, finished, result.Provider, result.Account,
			result.Action, logState, level, logMessage); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type sqlExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func (s *Service) writeAuditLog(
	ctx context.Context,
	target, account, operation, state, level, message string,
) {
	if s == nil || s.db == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	} else {
		ctx = context.WithoutCancel(ctx)
	}
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_ = s.insertLog(writeCtx, s.db, s.now().UTC().Format(time.RFC3339),
		target, account, operation, state, level, message)
}

func (s *Service) writeAuditFailure(
	ctx context.Context,
	target, account, operation string,
	operationErr error,
) {
	if operationErr == nil {
		return
	}
	message := auditOperationLabel(operation) + "失败"
	if detail := redactMessage(operationErr.Error()); detail != "" {
		message += " · " + detail
	}
	s.writeAuditLog(ctx, target, account, operation, "failed_final", "error", message)
}

func (s *Service) insertLog(
	ctx context.Context,
	exec sqlExecer,
	ts, target, account, operation, state, level, message string,
) error {
	if exec == nil {
		return nil
	}
	_, err := exec.ExecContext(ctx, `
INSERT INTO logs (ts, target, account_id, operation, state, level, message)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ts, target, account, operation, state, level, redactMessage(message))
	return err
}

func (s *Service) writeModelTestAuditLog(
	ctx context.Context,
	provider, account, modelID string,
	view ModelTestView,
	testErr error,
) {
	if s == nil || s.db == nil {
		return
	}
	state := "completed"
	level := "info"
	message := "模型测试"
	if modelID != "" {
		message += " · " + modelID
	}
	switch {
	case testErr != nil:
		state = "failed_final"
		level = "error"
		message += " · " + firstNonEmpty(redactMessage(testErr.Error()), "执行失败")
	case !view.OK:
		state = modelTestFailureState(view.Status)
		level = "error"
		if view.Status > 0 {
			message += fmt.Sprintf(" · HTTP %d", view.Status)
		}
		if view.Latency > 0 {
			message += fmt.Sprintf(" · %d ms", view.Latency)
		}
		message += " · " + firstNonEmpty(redactMessage(view.Message), "模型不可用")
	default:
		message += fmt.Sprintf(" · HTTP %d", view.Status)
		if view.Latency > 0 {
			message += fmt.Sprintf(" · %d ms", view.Latency)
		}
		message += " · 模型可用"
	}
	s.writeAuditLog(ctx, provider, account, "model_test", state, level, message)
}

func modelTestFailureState(status int) string {
	if status == 0 || status == 408 || status == 429 || status >= 500 {
		return "failed_retryable"
	}
	return "failed_final"
}

func runResultAudit(result runResult) (state, level, message string) {
	state = strings.TrimSpace(result.State)
	failed := result.Err != nil || state == string(engine.FailedRetryable) ||
		state == string(engine.FailedFinal)
	if state == "" {
		if result.Err != nil {
			state = string(engine.FailedRetryable)
		} else {
			state = "completed"
		}
	}
	level = "info"
	if failed {
		level = "error"
	} else if state == string(engine.ManualRequired) {
		level = "warn"
	}

	parts := []string{auditOperationLabel(result.Action)}
	if result.Credits != nil &&
		(result.Balance == nil || *result.Credits != *result.Balance) {
		parts = append(parts, "今日已领取 +"+formatAuditNumber(*result.Credits))
	}
	if result.Balance != nil {
		parts = append(parts, "当前额度 "+formatAuditNumber(*result.Balance))
	}
	if result.Model != "" {
		parts = append(parts, "模型 "+result.Model)
	}
	if failed || state == string(engine.ManualRequired) || !result.Executed {
		parts = append(parts, auditStateLabel(state))
	}
	if result.Message != "" {
		parts = append(parts, redactMessage(result.Message))
	} else if result.Err != nil {
		parts = append(parts, firstNonEmpty(redactMessage(result.Err.Error()), "执行失败"))
	}
	return state, level, redactMessage(strings.Join(parts, " · "))
}

func auditOperationLabel(operation string) string {
	switch operation {
	case "checkin":
		return "签到"
	case "refresh":
		return "额度刷新"
	case "keepalive":
		return "每日活跃"
	case "model_test":
		return "模型测试"
	case "login":
		return "登录"
	case "account":
		return "账号设置"
	case "provider":
		return "供应商设置"
	case "automation":
		return "自动执行"
	default:
		return firstNonEmpty(operation, "操作")
	}
}

func auditStateLabel(state string) string {
	switch state {
	case string(engine.SignedConfirmed):
		return "已确认到账"
	case string(engine.SignedProvisional):
		return "已领取，待确认"
	case string(engine.Already):
		return "今日已领取"
	case string(engine.ManualRequired):
		return "需要人工处理"
	case string(engine.FailedRetryable):
		return "网络或上游暂时失败"
	case string(engine.FailedFinal):
		return "执行失败"
	case "completed":
		return "已完成"
	default:
		return state
	}
}

func formatAuditNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func enabledLabel(enabled bool) string {
	if enabled {
		return "启用"
	}
	return "停用"
}

func nullableFloat(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}

// redactMessage 使用 internal/privacy 的统一规则（bearer/密钥/uid/手机号/邮箱/
// 本机用户名/长 token），保证 provider 写日志与 adapter 写日志是同一套口径。
func redactMessage(value string) string {
	return privacy.SanitizeText(value, 500)
}

// History returns recent provider run items and quota readings. Account IDs
// are masked before leaving the service.
func (s *Service) History(
	ctx context.Context, provider, account string, limit int,
) ([]RunItemView, []QuotaReadingView, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query := `
SELECT r.id, i.provider, i.account_id, i.action, i.state, i.message,
       i.credits, i.balance, i.model, i.created_at
FROM run_items i
JOIN runs r ON r.id = i.run_id
WHERE (? = '' OR i.provider = ?) AND (? = '' OR i.account_id = ?)
`
	args := []any{provider, provider, account, account}
	query += ` ORDER BY i.created_at DESC, i.id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	items := []RunItemView{}
	for rows.Next() {
		var item RunItemView
		var credits, balance sql.NullFloat64
		if err := rows.Scan(&item.RunID, &item.Provider, &item.Account, &item.Action,
			&item.State, &item.Message, &credits, &balance, &item.Model,
			&item.ObservedAt); err != nil {
			return nil, nil, err
		}
		item.Account = maskIdentity(item.Account)
		if credits.Valid {
			value := credits.Float64
			item.Credits = &value
		}
		if balance.Valid {
			value := balance.Float64
			item.Balance = &value
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	rows, err = s.db.QueryContext(ctx, `
SELECT observed_at, plan, until, balance, windows_json
FROM quota_readings
WHERE (? = '' OR provider = ?) AND (? = '' OR account_id = ?)
ORDER BY observed_at DESC, id DESC
LIMIT ?`, provider, provider, account, account, limit)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	readings := []QuotaReadingView{}
	for rows.Next() {
		var item QuotaReadingView
		var windows string
		if err := rows.Scan(&item.ObservedAt, &item.Plan, &item.Until,
			&item.Balance, &windows); err != nil {
			return nil, nil, err
		}
		if json.Unmarshal([]byte(windows), &item.Windows) != nil || item.Windows == nil {
			item.Windows = []providerruntime.UsageWindow{}
		}
		readings = append(readings, item)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return items, readings, nil
}

// activityOutcome carries the non-secret metadata of one daily activity send.
type activityOutcome struct {
	State      string
	HTTPStatus int
	LatencyMS  int64
	Cost       float64
	Model      string
	Message    string
}

// activity sends the smallest real chat message ("hi") through the provider
// plugin runtime. It never uses a local agent client. Free models are always
// preferred; subscription sign-ins (oauth/browser/device) may fall back to
// their cheapest included model, while metered API-key accounts are skipped so
// the feature never silently spends credit.
func (s *Service) activity(
	ctx context.Context, host Runtime, provider, account string,
) (engine.ExternalResult, activityOutcome) {
	loader, err := host.Load(ctx, provider, account)
	if err != nil {
		return engine.ExternalResult{Kind: engine.RRetryable, Message: "读取供应商登录态失败"},
			activityOutcome{State: "failed_retryable", Message: "读取供应商登录态失败"}
	}
	if !loader.Fetch {
		return engine.ExternalResult{Kind: engine.RFinal, Message: "该供应商不支持每日活跃对话请求"},
			activityOutcome{State: "failed_final", Message: "该供应商不支持每日活跃对话请求"}
	}
	// WorkBuddy 国内版的每日活跃是「计费域一条 chat 事件」，不是模型对话：
	// 发 hi 给免费模型不会点亮成长中心连登，旧路径因此看起来执行成功却没生效。
	// 只在 workbuddy（国内版）走专用上报；workbuddy-ai 仍走通用对话路径。
	if provider == "workbuddy" {
		return s.workBuddyDailyActivity(ctx, host, provider, account)
	}
	runtimeProvider, err := s.runtimeProvider(ctx, host, provider)
	if err != nil {
		return engine.ExternalResult{Kind: engine.RRetryable, Message: "读取供应商模型失败"},
			activityOutcome{State: "failed_retryable", Message: "读取供应商模型失败"}
	}
	model, url, subscription := activityModel(runtimeProvider, account, loader.BaseURL)
	if model.ID == "" || url == "" {
		message := "没有可用的免费模型（按量计费账号不自动发送），已跳过每日活跃"
		if isSubscriptionAuth(accountAuthType(runtimeProvider, account)) {
			message = "没有可用的模型，已跳过每日活跃"
		}
		// 可重试：失败不消耗当天次数；用户后来补上免费模型时仍能自动成功。
		return engine.ExternalResult{Kind: engine.RRetryable, Message: message},
			activityOutcome{State: "no_free_model", Message: message}
	}
	npm := firstNonEmpty(model.NPM, runtimeProvider.NPM)
	protocol := "openai"
	if strings.Contains(strings.ToLower(npm), "anthropic") {
		protocol = "anthropic"
	}
	body := map[string]any{
		"model":      model.ID,
		"messages":   []map[string]string{{"role": "user", "content": "hi"}},
		"max_tokens": 16,
		"stream":     false,
	}
	headers := map[string]string{"content-type": "application/json"}
	if protocol == "anthropic" {
		body["system"] = "You are a daily activity check."
		headers["anthropic-version"] = "2023-06-01"
	}
	encoded, _ := json.Marshal(body)

	started := time.Now()
	outcome := activityOutcome{Model: model.ID, Cost: 0}
	response, fetchErr := host.Fetch(ctx, providerruntime.FetchRequest{
		Provider: provider, Account: account, URL: url, Method: "POST",
		Headers: headers, Body: string(encoded),
	})
	if fetchErr != nil {
		outcome.LatencyMS = time.Since(started).Milliseconds()
		outcome.State = "failed_retryable"
		outcome.Message = "供应商每日活跃请求失败"
		return engine.ExternalResult{Kind: engine.RRetryable, Message: outcome.Message}, outcome
	}
	needsStream := activityNeedsStream(response.Status, response.Body)
	if needsStream {
		body["stream"] = true
		encoded, _ = json.Marshal(body)
		response, fetchErr = host.Fetch(ctx, providerruntime.FetchRequest{
			Provider: provider, Account: account, URL: url, Method: "POST",
			Headers: headers, Body: string(encoded),
		})
		if fetchErr != nil {
			outcome.LatencyMS = time.Since(started).Milliseconds()
			outcome.State = "needs_stream"
			outcome.Message = "供应商要求流式请求，自动重试仍失败"
			return engine.ExternalResult{Kind: engine.RRetryable, Message: outcome.Message}, outcome
		}
	}
	outcome.LatencyMS = time.Since(started).Milliseconds()
	outcome.HTTPStatus = response.Status
	if response.Status < 200 || response.Status >= 300 {
		state, kind := activityStatusOutcome(response.Status)
		if needsStream && (response.Status == 400 || response.Status == 415 || response.Status == 422) &&
			activityNeedsStream(response.Status, response.Body) {
			state = "needs_stream"
		}
		outcome.State = state
		outcome.Message = activityStatusMessage(response.Status)
		return engine.ExternalResult{Kind: kind, Message: outcome.Message}, outcome
	}
	if !hasReply(response.Body) {
		outcome.State = "no_reply"
		outcome.Message = "供应商返回成功但没有模型回复"
		return engine.ExternalResult{Kind: engine.RRetryable, Message: outcome.Message}, outcome
	}
	outcome.State = "completed"
	if subscription {
		outcome.Message = "每日活跃完成（订阅额度）"
	} else {
		outcome.Message = "每日活跃完成"
	}
	return engine.ExternalResult{Kind: engine.RSuccess, Message: outcome.Message}, outcome
}

// workBuddyDailyActivity reports the billing-domain chat event that lights the
// WorkBuddy growth-center streak. The provider plugin's fetch signs the request
// with the account token/uid/domain, so this path never needs the raw token.
func (s *Service) workBuddyDailyActivity(
	ctx context.Context, host Runtime, provider, account string,
) (engine.ExternalResult, activityOutcome) {
	runtimeProvider, err := s.runtimeProvider(ctx, host, provider)
	if err != nil {
		return engine.ExternalResult{Kind: engine.RRetryable, Message: "读取 WorkBuddy 账号信息失败"},
			activityOutcome{State: "failed_retryable", Message: "读取 WorkBuddy 账号信息失败"}
	}
	uid := ""
	for _, item := range runtimeProvider.Accounts {
		if item.Key == account {
			uid = item.UID
			break
		}
	}
	if uid == "" {
		message := "无法读取 WorkBuddy 账号 UID，已跳过每日活跃"
		return engine.ExternalResult{Kind: engine.RRetryable, Message: message},
			activityOutcome{State: "failed_retryable", Message: message}
	}

	now := s.now()
	event := workbuddy.DailyActivityEventFor(uid, now)
	body, err := json.Marshal([]workbuddy.DailyActivityEvent{event})
	if err != nil {
		return engine.ExternalResult{Kind: engine.RRetryable, Message: "构造 WorkBuddy 每日活跃事件失败"},
			activityOutcome{State: "failed_retryable", Message: "构造 WorkBuddy 每日活跃事件失败"}
	}

	started := time.Now()
	outcome := activityOutcome{}
	response, fetchErr := host.Fetch(ctx, providerruntime.FetchRequest{
		Provider: provider, Account: account,
		URL: workbuddy.BillingReportURL(), Method: "POST",
		Headers: workbuddy.DailyActivityHeaders(uid, now),
		Body:    string(body),
	})
	outcome.LatencyMS = time.Since(started).Milliseconds()
	if fetchErr != nil {
		outcome.State = "failed_retryable"
		outcome.Message = "WorkBuddy 每日活跃上报失败（网络）"
		return engine.ExternalResult{Kind: engine.RRetryable, Message: outcome.Message}, outcome
	}
	outcome.HTTPStatus = response.Status
	parsed := workbuddy.ParseDailyActivityResponse(response.Status, response.Body)
	upstream := parsed.Message
	switch {
	case parsed.OK:
		outcome.State = "completed"
		outcome.Message = "每日活跃已上报（WorkBuddy 连登已点亮）"
		return engine.ExternalResult{Kind: engine.RSuccess, Message: outcome.Message}, outcome
	case parsed.Restricted:
		outcome.State = "failed_final"
		outcome.Message = "WorkBuddy 拒绝了每日活跃上报（账号级风控），请稍后再试"
	case parsed.Unauthorized:
		outcome.State = "unauthorized"
		outcome.Message = fmt.Sprintf("账号鉴权失败（HTTP %d），请重新登录后再试", response.Status)
	case parsed.Retryable:
		if response.Status == 429 {
			outcome.State = "rate_limited"
			outcome.Message = "WorkBuddy 限流（HTTP 429），稍后自动重试"
		} else {
			outcome.State = "failed_retryable"
			outcome.Message = fmt.Sprintf("WorkBuddy 每日活跃上报失败（HTTP %d），稍后自动重试", response.Status)
		}
	default:
		outcome.State = "failed_retryable"
		outcome.Message = "WorkBuddy 每日活跃上报失败，稍后自动重试"
	}
	if upstream != "" {
		outcome.Message += "：" + redactMessage(upstream)
	}
	return engine.ExternalResult{Kind: resultKindForActivityState(outcome.State), Message: outcome.Message}, outcome
}

// resultKindForActivityState maps our activity states to the shared state
// machine result kinds.
func resultKindForActivityState(state string) engine.ResultKind {
	if state == "completed" {
		return engine.RSuccess
	}
	switch state {
	case "unauthorized", "failed_final":
		return engine.RFinal
	default:
		return engine.RRetryable
	}
}

// activityStatusOutcome maps one HTTP status to the persisted activity state and
// the shared state-machine result kind. Auth failures are final for the day
// (re-login required); rate limits and upstream errors are retried after the
// activity cooldown.
func activityStatusOutcome(status int) (string, engine.ResultKind) {
	switch status {
	case 401, 403:
		return "unauthorized", engine.RFinal
	case 429:
		return "rate_limited", engine.RRetryable
	case 408:
		return "failed_retryable", engine.RRetryable
	}
	if status >= 500 {
		return "failed_retryable", engine.RRetryable
	}
	return "failed_final", engine.RFinal
}

func activityStatusMessage(status int) string {
	switch status {
	case 401, 403:
		return fmt.Sprintf("账号鉴权失败（HTTP %d），请重新登录后再试", status)
	case 429:
		return fmt.Sprintf("供应商限流（HTTP %d），稍后自动重试", status)
	}
	if status >= 500 {
		return fmt.Sprintf("供应商服务异常（HTTP %d），稍后自动重试", status)
	}
	return fmt.Sprintf("供应商每日活跃返回 HTTP %d", status)
}

func activityStateForResult(state engine.State, rich string) string {
	if rich != "" {
		return rich
	}
	switch state {
	case engine.Completed:
		return "completed"
	case engine.FailedRetryable:
		return "failed_retryable"
	case engine.FailedFinal:
		return "failed_final"
	default:
		return string(state)
	}
}

func (s *Service) recordActivityRun(
	ctx context.Context, provider, account, day string, outcome activityOutcome,
) error {
	if s.db == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO activity_runs
  (day_key, provider, account_id, state, http_status, latency_ms, cost, message, ts)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		day, provider, account, outcome.State, outcome.HTTPStatus,
		outcome.LatencyMS, outcome.Cost, redactMessage(outcome.Message),
		s.now().UTC().Format(time.RFC3339))
	return err
}

// activityModel picks a model for the daily-activity message. Free models are
// preferred (lower rate first, then a larger context). When the account has no
// free model, subscription sign-ins (oauth/browser/device) fall back to their
// cheapest usable model because usage is included in the plan; metered
// API-key accounts are skipped so the feature never silently spends credit.
func activityModel(
	provider providerruntime.Provider,
	account, fallbackBaseURL string,
) (providerruntime.Model, string, bool) {
	allowed := map[string]bool{}
	for _, item := range provider.Accounts {
		if item.Key != account {
			continue
		}
		for _, id := range item.Models {
			allowed[id] = true
		}
		break
	}
	endpointFor := func(model providerruntime.Model) (providerruntime.Model, string) {
		if len(allowed) > 0 && !allowed[model.ID] {
			return providerruntime.Model{}, ""
		}
		base := strings.TrimRight(firstNonEmpty(model.APIURL, provider.APIURL, fallbackBaseURL), "/")
		if base == "" {
			return providerruntime.Model{}, ""
		}
		npm := strings.ToLower(firstNonEmpty(model.NPM, provider.NPM))
		suffix := "/chat/completions"
		if strings.Contains(npm, "anthropic") {
			suffix = "/messages"
		}
		if !strings.HasSuffix(base, suffix) {
			base += suffix
		}
		return model, base
	}
	all := make([]providerruntime.Model, 0, len(provider.Models))
	free := make([]providerruntime.Model, 0, len(provider.Models))
	for _, model := range provider.Models {
		if strings.TrimSpace(model.ID) == "" {
			continue
		}
		all = append(all, model)
		if model.Free {
			free = append(free, model)
		}
	}
	sortByCost := func(models []providerruntime.Model) {
		sort.SliceStable(models, func(i, j int) bool {
			if models[i].Rate != models[j].Rate {
				return models[i].Rate < models[j].Rate
			}
			return models[i].Context > models[j].Context
		})
	}
	sortByCost(free)
	for _, model := range free {
		if candidate, url := endpointFor(model); url != "" {
			return candidate, url, false
		}
	}
	if isSubscriptionAuth(accountAuthType(provider, account)) {
		sortByCost(all)
		for _, model := range all {
			if candidate, url := endpointFor(model); url != "" {
				return candidate, url, true
			}
		}
	}
	return providerruntime.Model{}, "", false
}

// accountAuthType returns the account's auth type (oauth/browser/device/api/key).
func accountAuthType(provider providerruntime.Provider, account string) string {
	for _, item := range provider.Accounts {
		if item.Key == account {
			return strings.ToLower(strings.TrimSpace(item.Type))
		}
	}
	return ""
}

// isSubscriptionAuth reports whether the account signs in with a subscription
// identity (usage included) rather than a metered API key.
func isSubscriptionAuth(kind string) bool {
	switch kind {
	case "oauth", "browser", "device", "subscription":
		return true
	}
	return false
}

func hasReply(data []byte) bool {
	var value map[string]any
	if json.Unmarshal(data, &value) != nil {
		text := string(data)
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "" || payload == "[DONE]" {
				continue
			}
			var event map[string]any
			if json.Unmarshal([]byte(payload), &event) == nil && jsonHasReply(event) {
				return true
			}
		}
		return len(strings.TrimSpace(text)) > 0
	}
	return jsonHasReply(value)
}

func jsonHasReply(value map[string]any) bool {
	if choices, ok := value["choices"].([]any); ok && len(choices) > 0 {
		return true
	}
	if content, ok := value["content"].([]any); ok && len(content) > 0 {
		return true
	}
	if text, ok := value["output_text"].(string); ok && strings.TrimSpace(text) != "" {
		return true
	}
	return false
}

func activityNeedsStream(status int, body []byte) bool {
	if status != 400 && status != 415 && status != 422 {
		return false
	}
	message := strings.ToLower(string(body))
	return strings.Contains(message, "non-stream") ||
		strings.Contains(message, "stream request") ||
		strings.Contains(message, "streaming is required") ||
		strings.Contains(message, "stream must")
}

func kindForCheckin(outcome string) engine.ResultKind {
	switch outcome {
	case "claimed":
		return engine.RSuccess
	case "done":
		return engine.RAlready
	case "ineligible":
		return engine.RIneligible
	case "inactive":
		return engine.RInactive
	case "captcha":
		return engine.RManualRequired
	case "failed":
		return engine.RFinal
	default:
		return engine.RRetryable
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func maskIdentity(value string) string {
	value = strings.ToValidUTF8(strings.TrimSpace(value), "�")
	chars := []rune(value)
	if len(chars) == 0 {
		return "****"
	}
	at := -1
	for index, char := range chars {
		if char == '@' {
			at = index
			break
		}
	}
	if at > 0 {
		prefix := chars[:at]
		if len(prefix) > 2 {
			prefix = prefix[:2]
		}
		return string(prefix) + "***" + string(chars[at:])
	}
	if len(chars) <= 4 {
		return "****" + string(chars)
	}
	return "****" + string(chars[len(chars)-4:])
}

func usageCredits(usage providerruntime.Usage) *float64 {
	for _, window := range usage.Windows {
		if window.Balance != nil {
			value := *window.Balance
			return &value
		}
	}
	return nil
}

// StaleAuthPath returns the temporary auth file for cleanup/test diagnostics.
func StaleAuthPath(configDir string) string {
	return filepath.Join(configDir, "provider-auth.tmp.json")
}

// ActivityAccountView is one account's「每日活跃」projection.
type ActivityAccountView struct {
	Provider     string `json:"provider"`
	ProviderName string `json:"provider_name"`
	Account      string `json:"account"`
	Masked       string `json:"masked"`
	Enabled      bool   `json:"enabled"`
	TaskEnabled  bool   `json:"task_enabled"`
	TodayState   string `json:"today_state"`
	TodayMessage string `json:"today_message"`
	LastAt       string `json:"last_at,omitempty"`
	Streak       int    `json:"streak"`
	SentToday    bool   `json:"sent_today"`
}

// ActivitySummaryView aggregates today's daily activity across accounts.
type ActivitySummaryView struct {
	Total     int `json:"total"`
	Done      int `json:"done"`
	Failed    int `json:"failed"`
	StreakMax int `json:"streak_max"`
}

// ActivityView is the complete daily activity projection used by the apps page,
// account chips, and the agent detail modal.
type ActivityView struct {
	Accounts  []ActivityAccountView `json:"accounts"`
	Summary   ActivitySummaryView   `json:"summary"`
	UpdatedAt string                `json:"updated_at"`
}

// ActivityAccountInput is the provider/account side of the projection.
type ActivityAccountInput struct {
	Provider     string
	ProviderName string
	Account      string
	Masked       string
	Enabled      bool
	TaskEnabled  bool
	TodayState   string
	TodayMessage string
}

// ActivityRecord is one persisted activity_runs row (already redacted).
type ActivityRecord struct {
	Provider   string
	Account    string
	Day        string
	State      string
	HTTPStatus int
	LatencyMS  int64
	Cost       float64
	Message    string
	TS         string
}

func activityKey(provider, account string) string {
	return provider + "\x00" + account
}

func activityFailureState(state string) bool {
	switch state {
	case "failed_retryable", "failed_final", "unauthorized", "rate_limited",
		"no_free_model", "needs_stream", "no_reply":
		return true
	default:
		return false
	}
}

func activityStateLabel(state string) string {
	if state == "" {
		return "none"
	}
	return state
}

// activityStreak counts consecutive Beijing days with a successful activity
// record. Today is not required: when today has not succeeded yet, the streak
// still counts back from yesterday (a broken day stops the count).
func activityStreak(successDays map[string]bool, today string) int {
	day, err := time.Parse("2006-01-02", today)
	if err != nil {
		return 0
	}
	if !successDays[today] {
		day = day.AddDate(0, 0, -1)
	}
	streak := 0
	for successDays[day.Format("2006-01-02")] {
		streak++
		day = day.AddDate(0, 0, -1)
	}
	return streak
}

// BuildActivityView projects provider accounts plus stored activity records
// into the UI view. It is a pure function so the projection can be tested
// without a database or provider runtime.
func BuildActivityView(
	accounts []ActivityAccountInput,
	runs []ActivityRecord,
	today string,
	now time.Time,
) ActivityView {
	type aggregate struct {
		todayRecord *ActivityRecord
		lastAt      string
		successDays map[string]bool
	}
	byAccount := map[string]*aggregate{}
	for i := range runs {
		record := runs[i]
		key := activityKey(record.Provider, record.Account)
		agg := byAccount[key]
		if agg == nil {
			agg = &aggregate{successDays: map[string]bool{}}
			byAccount[key] = agg
		}
		if record.Day == today && (agg.todayRecord == nil || record.TS >= agg.todayRecord.TS) {
			copied := record
			agg.todayRecord = &copied
		}
		if record.TS > agg.lastAt {
			agg.lastAt = record.TS
		}
		if record.State == "completed" {
			agg.successDays[record.Day] = true
		}
	}

	view := ActivityView{
		Accounts:  make([]ActivityAccountView, 0, len(accounts)),
		UpdatedAt: now.UTC().Format(time.RFC3339),
	}
	for _, input := range accounts {
		key := activityKey(input.Provider, input.Account)
		agg := byAccount[key]
		if agg == nil {
			agg = &aggregate{successDays: map[string]bool{}}
			byAccount[key] = agg
		}
		// 旧数据可能只有 daily_ops 的 completed 行而没有 activity_runs 记录；
		// 今天已成功仍然计入连续天数，避免升级后连击凭空断一天。
		if input.TodayState == string(engine.Completed) {
			agg.successDays[today] = true
		}
		item := ActivityAccountView{
			Provider:     input.Provider,
			ProviderName: input.ProviderName,
			Account:      input.Account,
			Masked:       input.Masked,
			Enabled:      input.Enabled,
			TaskEnabled:  input.TaskEnabled,
		}
		item.LastAt = agg.lastAt
		item.Streak = activityStreak(agg.successDays, today)
		switch {
		case agg.todayRecord != nil:
			item.TodayState = activityStateLabel(agg.todayRecord.State)
			item.TodayMessage = agg.todayRecord.Message
		case input.TodayState == string(engine.Completed):
			item.TodayState = "completed"
			item.TodayMessage = input.TodayMessage
		case input.TodayState == string(engine.FailedRetryable),
			input.TodayState == string(engine.FailedFinal):
			item.TodayState = input.TodayState
			item.TodayMessage = input.TodayMessage
		default:
			item.TodayState = "none"
		}
		if item.TodayState == "completed" {
			item.SentToday = true
			view.Summary.Done++
		} else if activityFailureState(item.TodayState) {
			view.Summary.Failed++
		}
		if item.Streak > view.Summary.StreakMax {
			view.Summary.StreakMax = item.Streak
		}
		view.Accounts = append(view.Accounts, item)
	}
	view.Summary.Total = len(view.Accounts)
	return view
}

// ActivityView projects all configured provider accounts that support chat
// requests through their plugin.
func (s *Service) ActivityView(ctx context.Context) (ActivityView, error) {
	view, err := s.CachedListView(ctx)
	if err != nil {
		return ActivityView{}, err
	}
	today := engine.BeijingDay(s.now())
	accounts := []ActivityAccountInput{}
	for _, provider := range view.Providers {
		if !provider.Activity {
			continue
		}
		for _, account := range provider.Accounts {
			accounts = append(accounts, ActivityAccountInput{
				Provider:     provider.ID,
				ProviderName: provider.Name,
				Account:      account.Key,
				Masked:       account.MaskedIdentity,
				Enabled:      provider.Enabled && account.Enabled,
				TaskEnabled:  enabledPref(account.TaskPreferences, features.OperationActivity),
			})
		}
	}

	states := map[string]ActivityAccountInput{}
	rows, err := s.db.QueryContext(ctx, `
SELECT target, account_id, state, message
FROM daily_ops
WHERE operation = ? AND day_key = ?`,
		features.OperationActivity, today)
	if err != nil {
		return ActivityView{}, err
	}
	for rows.Next() {
		var provider, account, state, message string
		if err := rows.Scan(&provider, &account, &state, &message); err != nil {
			rows.Close()
			return ActivityView{}, err
		}
		states[activityKey(provider, account)] = ActivityAccountInput{
			TodayState: state, TodayMessage: message,
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ActivityView{}, err
	}
	rows.Close()
	for i := range accounts {
		if state, ok := states[activityKey(accounts[i].Provider, accounts[i].Account)]; ok {
			accounts[i].TodayState = state.TodayState
			accounts[i].TodayMessage = state.TodayMessage
		}
	}

	records := []ActivityRecord{}
	rows, err = s.db.QueryContext(ctx, `
SELECT provider, account_id, day_key, state, http_status, latency_ms, cost, message, ts
FROM activity_runs
ORDER BY ts ASC, id ASC`)
	if err != nil {
		return ActivityView{}, err
	}
	for rows.Next() {
		var record ActivityRecord
		if err := rows.Scan(&record.Provider, &record.Account, &record.Day,
			&record.State, &record.HTTPStatus, &record.LatencyMS, &record.Cost,
			&record.Message, &record.TS); err != nil {
			rows.Close()
			return ActivityView{}, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ActivityView{}, err
	}
	rows.Close()

	return BuildActivityView(accounts, records, today, s.now()), nil
}
