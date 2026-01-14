// Package app provides chaos testing helpers.
package app

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"ratelimit/internal/ratelimit/config"
	"ratelimit/internal/ratelimit/core"
	"ratelimit/internal/ratelimit/store/inmemory"
)

// ChaosScenario describes a chaos test.
type ChaosScenario struct {
	Name string
	Run  func(context.Context, *ChaosHarness) error
}

// ChaosHarness holds dependencies for chaos scenarios.
type ChaosHarness struct {
	App        *Application
	Redis      *inmemory.InMemoryRedis
	Membership *core.StaticMembership
	RuleDB     *inmemory.InMemoryRuleDB
	Outbox     *inmemory.InMemoryOutbox
	PubSub     *chaosPubSub
}

// NewChaosHarness builds a harness with in memory components.
func NewChaosHarness() (*ChaosHarness, error) {
	redis := inmemory.NewInMemoryRedis(nil)
	membership := core.NewSingleInstanceMembership("node", "local")
	ruleDB := inmemory.NewInMemoryRuleDB(nil)
	outbox := inmemory.NewInMemoryOutbox()
	pubsub := newChaosPubSub(inmemory.NewInMemoryPubSub())

	cfg := &config.Config{
		Region:            "local",
		Redis:             redis,
		Membership:        membership,
		RuleDB:            ruleDB,
		Outbox:            outbox,
		PubSub:            pubsub,
		CacheSyncInterval: 20 * time.Millisecond,
		HealthInterval:    10 * time.Millisecond,
		EnableHTTP:        false,
		EnableGRPC:        false,
	}
	app, err := NewApplication(cfg)
	if err != nil {
		return nil, err
	}
	return &ChaosHarness{
		App:        app,
		Redis:      redis,
		Membership: membership,
		RuleDB:     ruleDB,
		Outbox:     outbox,
		PubSub:     pubsub,
	}, nil
}

// RunChaos executes the full chaos suite.
func RunChaos(ctx context.Context, h *ChaosHarness) error {
	return runChaosScenarios(ctx, h, defaultChaosScenarios())
}

func runChaosScenarios(ctx context.Context, h *ChaosHarness, scenarios []ChaosScenario) error {
	if h == nil {
		return errors.New("harness is required")
	}
	if h.App == nil {
		return errors.New("app is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	appCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := h.App.Start(appCtx); err != nil {
		return err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = h.App.Shutdown(shutdownCtx)
	}()

	for _, scenario := range scenarios {
		if scenario.Run == nil {
			continue
		}
		if err := scenario.Run(ctx, h); err != nil {
			return err
		}
	}
	return nil
}

func defaultChaosScenarios() []ChaosScenario {
	return []ChaosScenario{
		scenarioRedisRecover(),
		scenarioRuleUpdateStorm(),
		scenarioPubSubRepair(),
	}
}

func scenarioRedisRecover() ChaosScenario {
	return ChaosScenario{
		Name: "redis recover",
		Run: func(ctx context.Context, h *ChaosHarness) error {
			if h == nil || h.App == nil || h.Redis == nil {
				return errors.New("redis scenario requires app and redis")
			}
			if h.Membership != nil {
				h.Membership.SetHealthy(true)
			}
			if err := ensureRule(ctx, h.App, "tenant", "resource"); err != nil {
				return err
			}
			h.Redis.SetHealthy(false)
			request := &core.CheckLimitRequest{
				TenantID: "tenant",
				Resource: "resource",
				UserID:   "user",
				Cost:     1,
			}
			for i := 0; i < 50; i++ {
				resp, err := h.App.RateLimitHandler.CheckLimit(ctx, request)
				if err != nil {
					return err
				}
				h.App.RateLimitHandler.ReleaseResponse(resp)
			}
			h.Redis.SetHealthy(true)
			return waitForMode(ctx, h.App, core.ModeNormal, 2*time.Second)
		},
	}
}

func scenarioRuleUpdateStorm() ChaosScenario {
	return ChaosScenario{
		Name: "rule update storm",
		Run: func(ctx context.Context, h *ChaosHarness) error {
			if h == nil || h.App == nil || h.RuleDB == nil {
				return errors.New("rule storm requires app and db")
			}
			const tenantCount = 5
			const ruleCount = 20
			rules := make([]*core.Rule, 0, ruleCount)
			for i := 0; i < ruleCount; i++ {
				tenantID := fmt.Sprintf("tenant_%d", i%tenantCount)
				resource := fmt.Sprintf("resource_%d", i)
				rule, err := h.App.AdminHandler.CreateRule(ctx, &core.CreateRuleRequest{
					TenantID:  tenantID,
					Resource:  resource,
					Algorithm: "token_bucket",
					Limit:     100 + int64(i),
					Window:    time.Second,
					BurstSize: 0,
				})
				if err != nil {
					return err
				}
				rules = append(rules, rule)
			}

			for round := 0; round < 5; round++ {
				for i, rule := range rules {
					updated, err := h.App.AdminHandler.UpdateRule(ctx, &core.UpdateRuleRequest{
						TenantID:        rule.TenantID,
						Resource:        rule.Resource,
						Algorithm:       rule.Algorithm,
						Limit:           rule.Limit + 1,
						Window:          rule.Window,
						BurstSize:       rule.BurstSize,
						ExpectedVersion: rule.Version,
					})
					if err != nil {
						return err
					}
					rules[i] = updated
				}
			}
			return waitForRuleCache(ctx, h.App, h.RuleDB, 2*time.Second)
		},
	}
}

func scenarioPubSubRepair() ChaosScenario {
	return ChaosScenario{
		Name: "pubsub repair",
		Run: func(ctx context.Context, h *ChaosHarness) error {
			if h == nil || h.App == nil || h.RuleDB == nil || h.PubSub == nil {
				return errors.New("pubsub scenario requires app db and pubsub")
			}
			if err := ensureRule(ctx, h.App, "tenant_drop", "resource_drop"); err != nil {
				return err
			}
			current, err := h.RuleDB.Get(ctx, "tenant_drop", "resource_drop")
			if err != nil {
				return err
			}
			h.PubSub.SetDrop(true)
			updated, err := h.RuleDB.Update(ctx, &core.UpdateRuleRequest{
				TenantID:        current.TenantID,
				Resource:        current.Resource,
				Algorithm:       current.Algorithm,
				Limit:           current.Limit + 5,
				Window:          current.Window,
				BurstSize:       current.BurstSize,
				ExpectedVersion: current.Version,
			})
			if err != nil {
				h.PubSub.SetDrop(false)
				return err
			}
			event := core.InvalidationEvent{
				TenantID:  updated.TenantID,
				Resource:  updated.Resource,
				Action:    "upsert",
				Version:   updated.Version,
				Timestamp: time.Now(),
			}
			data, err := core.MarshalInvalidationEvent(event)
			if err != nil {
				h.PubSub.SetDrop(false)
				return err
			}
			_ = h.PubSub.Publish(ctx, "ratelimit_invalidation", data)
			err = waitForRuleCache(ctx, h.App, h.RuleDB, 2*time.Second)
			h.PubSub.SetDrop(false)
			return err
		},
	}
}

func ensureRule(ctx context.Context, app *Application, tenantID, resource string) error {
	if app == nil || app.AdminHandler == nil {
		return errors.New("admin handler is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	_, err := app.AdminHandler.CreateRule(ctx, &core.CreateRuleRequest{
		TenantID:  tenantID,
		Resource:  resource,
		Algorithm: "token_bucket",
		Limit:     100,
		Window:    time.Second,
		BurstSize: 0,
	})
	if err == nil {
		return nil
	}
	if errors.Is(err, core.ErrConflict) {
		return nil
	}
	return err
}

func waitForMode(ctx context.Context, app *Application, target core.OperatingMode, timeout time.Duration) error {
	if app == nil {
		return errors.New("app is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	for {
		if app.Mode() == target {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("mode did not recover")
		case <-ticker.C:
		}
	}
}

func waitForRuleCache(ctx context.Context, app *Application, db core.RuleDB, timeout time.Duration) error {
	if app == nil || app.RuleCache == nil {
		return errors.New("rule cache is required")
	}
	if db == nil {
		return errors.New("rule db is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	for {
		if ruleCacheMatches(ctx, app.RuleCache, db) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("rule cache did not converge")
		case <-ticker.C:
		}
	}
}

func ruleCacheMatches(ctx context.Context, cache *core.RuleCache, db core.RuleDB) bool {
	if cache == nil || db == nil {
		return false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	rules, err := db.LoadAll(ctx)
	if err != nil {
		return false
	}
	for _, rule := range rules {
		if rule == nil {
			continue
		}
		cached, ok := cache.Get(rule.TenantID, rule.Resource)
		if !ok || cached == nil {
			return false
		}
		if cached.Version != rule.Version || cached.Limit != rule.Limit {
			return false
		}
	}
	return true
}

type chaosPubSub struct {
	inner *inmemory.InMemoryPubSub
	drop  atomic.Bool
}

func newChaosPubSub(inner *inmemory.InMemoryPubSub) *chaosPubSub {
	if inner == nil {
		inner = inmemory.NewInMemoryPubSub()
	}
	return &chaosPubSub{inner: inner}
}

func (ps *chaosPubSub) SetDrop(v bool) {
	if ps == nil {
		return
	}
	ps.drop.Store(v)
}

func (ps *chaosPubSub) Subscribe(ctx context.Context, channel string, handler func(context.Context, []byte)) error {
	if ps == nil || ps.inner == nil {
		return errors.New("pubsub is required")
	}
	return ps.inner.Subscribe(ctx, channel, handler)
}

func (ps *chaosPubSub) Publish(ctx context.Context, channel string, payload []byte) error {
	if ps == nil || ps.inner == nil {
		return errors.New("pubsub is required")
	}
	if ps.drop.Load() {
		return nil
	}
	return ps.inner.Publish(ctx, channel, payload)
}
