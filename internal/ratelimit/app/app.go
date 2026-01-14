// Package app wires application dependencies.
package app

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"ratelimit/internal/ratelimit/config"
	"ratelimit/internal/ratelimit/core"
	"ratelimit/internal/ratelimit/observability"
	"ratelimit/internal/ratelimit/store/inmemory"
	grpctransport "ratelimit/internal/ratelimit/transport/grpc"
	httptransport "ratelimit/internal/ratelimit/transport/http"
)

// Application holds core components for the service.
type Application struct {
	Config           *config.Config
	RuleCache        *core.RuleCache
	LimiterPool      *core.LimiterPool
	KeyBuilder       *core.KeyBuilder
	DegradeControl   *core.DegradeController
	FallbackLimiter  *core.FallbackLimiter
	LimiterFactory   *core.LimiterFactory
	RateLimitHandler *core.RateLimitHandler
	AdminHandler     *core.AdminHandler
	OutboxPublisher  *core.OutboxPublisher
	CacheInvalidator *core.CacheInvalidator
	CacheSyncWorker  *core.CacheSyncWorker
	HealthLoop       *core.HealthLoop
	ready            atomic.Bool
	httpTransport    *httptransport.HTTPTransport
	grpcTransport    *grpctransport.GRPCTransport
	transports       []core.Transport
	metrics          *observability.InMemoryMetrics
	tracer           observability.Tracer
	sampler          observability.Sampler
	cancel           context.CancelFunc
	wg               sync.WaitGroup
	inflight         *core.InFlight
	drainTimeout     time.Duration
	logger           observability.Logger
}

// NewApplication validates configuration and prepares the application.
func NewApplication(cfg *config.Config) (*Application, error) {
	if cfg == nil {
		return nil, errors.New("config is required")
	}
	if cfg.Region == "" {
		return nil, errors.New("region is required")
	}
	if cfg.DegradeRequireRegionQuorum {
		if cfg.RegionQuorumFraction <= 0 || cfg.RegionQuorumFraction > 1 {
			return nil, errors.New("region quorum fraction must be between 0 and 1")
		}
	}
	if cfg.EnableHTTP && cfg.HTTPListenAddr == "" {
		return nil, errors.New("http listen address is required")
	}
	grpcAddrProvided := cfg.GRPCListenAddr != ""
	if cfg.EnableGRPC && !grpcAddrProvided {
		return nil, errors.New("grpc listen address is required")
	}
	if cfg.EnableAuth && cfg.AdminToken == "" {
		return nil, errors.New("admin token is required")
	}
	if cfg.HTTPReadTimeout < 0 {
		return nil, errors.New("http read timeout must be positive")
	}
	if cfg.HTTPWriteTimeout < 0 {
		return nil, errors.New("http write timeout must be positive")
	}
	if cfg.HTTPIdleTimeout < 0 {
		return nil, errors.New("http idle timeout must be positive")
	}
	if cfg.GRPCKeepAlive < 0 {
		return nil, errors.New("grpc keep alive must be positive")
	}
	if cfg.RequestTimeout < 0 {
		return nil, errors.New("request timeout must be positive")
	}
	if cfg.DrainTimeout < 0 {
		return nil, errors.New("drain timeout must be positive")
	}
	if cfg.HTTPReadTimeout == 0 {
		cfg.HTTPReadTimeout = 5 * time.Second
	}
	if cfg.HTTPWriteTimeout == 0 {
		cfg.HTTPWriteTimeout = 10 * time.Second
	}
	if cfg.HTTPIdleTimeout == 0 {
		cfg.HTTPIdleTimeout = 60 * time.Second
	}
	if cfg.GRPCListenAddr == "" {
		cfg.GRPCListenAddr = ":9090"
	}
	if cfg.GRPCKeepAlive == 0 {
		cfg.GRPCKeepAlive = 60 * time.Second
	}
	if cfg.RequestTimeout == 0 {
		cfg.RequestTimeout = 2 * time.Second
	}
	if cfg.DrainTimeout == 0 {
		cfg.DrainTimeout = 5 * time.Second
	}
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = 1 << 20
	}
	if cfg.Logger == nil {
		cfg.Logger = observability.NewStdLogger(os.Stdout)
	}
	if cfg.RuleDB == nil {
		cfg.RuleDB = inmemory.NewInMemoryRuleDB(nil)
	}
	if cfg.Outbox == nil {
		cfg.Outbox = inmemory.NewInMemoryOutbox()
	}
	if cfg.PubSub == nil {
		cfg.PubSub = inmemory.NewInMemoryPubSub()
	}
	if cfg.Channel == "" {
		cfg.Channel = "ratelimit_invalidation"
	}
	if cfg.TraceSampleRate == 0 {
		cfg.TraceSampleRate = 100
	}
	if !cfg.GlobalOwnershipFallback {
		cfg.GlobalOwnershipFallback = true
	}
	if !cfg.CoalesceEnabled {
		cfg.CoalesceEnabled = true
	}
	if cfg.CoalesceTTL == 0 {
		cfg.CoalesceTTL = 10 * time.Millisecond
	}
	if cfg.CoalesceShards == 0 {
		cfg.CoalesceShards = 64
	}
	if cfg.BreakerOptions.FailureThreshold == 0 {
		cfg.BreakerOptions.FailureThreshold = 10
	}
	if cfg.BreakerOptions.OpenDuration == 0 {
		cfg.BreakerOptions.OpenDuration = 200 * time.Millisecond
	}
	if cfg.BreakerOptions.HalfOpenMaxCalls == 0 {
		cfg.BreakerOptions.HalfOpenMaxCalls = 5
	}
	if cfg.CacheSyncInterval == 0 {
		cfg.CacheSyncInterval = 200 * time.Millisecond
	}
	if cfg.HealthInterval == 0 {
		cfg.HealthInterval = 100 * time.Millisecond
	}
	if cfg.LimiterPolicy.Shards == 0 {
		cfg.LimiterPolicy.Shards = 16
	}
	if cfg.LimiterPolicy.MaxEntriesShard == 0 {
		cfg.LimiterPolicy.MaxEntriesShard = 1024
	}
	if cfg.LimiterPolicy.QuiesceWindow == 0 {
		cfg.LimiterPolicy.QuiesceWindow = 50 * time.Millisecond
	}
	if cfg.LimiterPolicy.CloseTimeout == 0 {
		cfg.LimiterPolicy.CloseTimeout = 2 * time.Second
	}
	if cfg.FallbackPolicy == (core.FallbackPolicy{}) {
		cfg.FallbackPolicy = core.NormalizeFallbackPolicy(cfg.FallbackPolicy)
	}
	if cfg.DegradeThresh.RedisUnhealthyFor == 0 {
		cfg.DegradeThresh.RedisUnhealthyFor = 500 * time.Millisecond
	}
	if cfg.DegradeThresh.MembershipUnhealthy == 0 {
		cfg.DegradeThresh.MembershipUnhealthy = 2 * time.Second
	}
	if cfg.RegionGroup == "" {
		cfg.RegionGroup = cfg.Region
	}
	if cfg.RegionInstanceWeight == 0 {
		cfg.RegionInstanceWeight = 1
	}
	if cfg.RegionQuorumFraction == 0 {
		cfg.RegionQuorumFraction = 0.5
	}
	redis := cfg.Redis
	if redis == nil {
		redis = inmemory.NewInMemoryRedis(nil)
	}
	membership := cfg.Membership
	if membership == nil {
		membership = core.NewSingleInstanceMembership("local", cfg.Region)
	}
	if static, ok := membership.(*core.StaticMembership); ok {
		static.SetSelfWeight(cfg.RegionInstanceWeight)
	}
	rules := core.NewRuleCache()
	degrade := core.NewDegradeController(redis, membership, cfg.DegradeThresh, cfg.Region, cfg.DegradeRequireRegionQuorum, cfg.RegionQuorumFraction)
	degrade.SetLogger(cfg.Logger)
	ownership := core.NewRendezvousOwnership(membership, cfg.Region, cfg.EnableGlobalOwnership, cfg.GlobalOwnershipFallback)
	fallback := core.NewFallbackLimiter(ownership, membership, cfg.Region, cfg.RegionGroup, core.NormalizeFallbackPolicy(cfg.FallbackPolicy), degrade, &core.LocalLimiterStore{})
	breaker := core.NewCircuitBreaker(cfg.BreakerOptions)
	factory := core.NewLimiterFactory(redis, fallback, degrade, breaker)
	pool := core.NewLimiterPool(rules, factory, cfg.LimiterPolicy)
	keys := core.NewKeyBuilder(core.NewByteBufferPool(4096))
	respPool := core.NewResponsePool()
	bp := core.NewBatchPlanner(core.NewIndexPool(), core.NewKeyPool(), core.NewCostPool())
	metrics := cfg.Metrics
	appMetrics, ok := metrics.(*observability.InMemoryMetrics)
	if !ok || appMetrics == nil {
		appMetrics = observability.NewInMemoryMetrics()
	}
	if metrics == nil {
		metrics = appMetrics
	}
	tracer := cfg.Tracer
	if tracer == nil {
		tracer = observability.NoopTracer{}
	}
	sampler := cfg.Sampler
	if sampler == nil {
		sampler = observability.NewHashSampler(cfg.TraceSampleRate)
	}
	var coalescer *core.Coalescer
	if cfg.CoalesceEnabled {
		coalescer = core.NewCoalescer(cfg.CoalesceShards, cfg.CoalesceTTL)
	}

	inflight := core.NewInFlight()
	rate := core.NewRateLimitHandler(rules, pool, keys, cfg.Region, respPool, tracer, sampler, metrics, coalescer)
	rate.SetBatchPlanner(bp)
	rate.SetInFlight(inflight)
	rate.SetRequestTimeout(cfg.RequestTimeout)

	var outboxWriter core.OutboxWriter
	if writer, ok := cfg.Outbox.(core.OutboxWriter); ok {
		outboxWriter = writer
	}

	admin := core.NewAdminHandler(cfg.RuleDB, rules, outboxWriter, tracer, metrics)
	pub := core.NewOutboxPublisher(cfg.Outbox, cfg.PubSub, cfg.Channel, 0)
	invalid := core.NewCacheInvalidator(cfg.RuleDB, rules, pool, cfg.PubSub, cfg.Channel)
	syncer := core.NewCacheSyncWorker(cfg.RuleDB, rules, cfg.CacheSyncInterval)
	health := core.NewHealthLoop(degrade, cfg.HealthInterval)

	app := &Application{
		Config:           cfg,
		RuleCache:        rules,
		LimiterFactory:   factory,
		LimiterPool:      pool,
		KeyBuilder:       keys,
		RateLimitHandler: rate,
		DegradeControl:   degrade,
		FallbackLimiter:  fallback,
		AdminHandler:     admin,
		OutboxPublisher:  pub,
		CacheInvalidator: invalid,
		CacheSyncWorker:  syncer,
		HealthLoop:       health,
		metrics:          appMetrics,
		tracer:           tracer,
		sampler:          sampler,
		inflight:         inflight,
		drainTimeout:     cfg.DrainTimeout,
		logger:           cfg.Logger,
	}

	if cfg.EnableHTTP {
		transport := httptransport.NewHTTPTransport(cfg.HTTPListenAddr, app.Ready)
		if err := transport.ServeRateLimit(app.RateLimitHandler); err != nil {
			return nil, err
		}
		if err := transport.ServeAdmin(app.AdminHandler); err != nil {
			return nil, err
		}
		transport.Configure(httptransport.HTTPTransportConfig{
			ReadTimeout:  cfg.HTTPReadTimeout,
			WriteTimeout: cfg.HTTPWriteTimeout,
			IdleTimeout:  cfg.HTTPIdleTimeout,
			MaxBodyBytes: cfg.MaxBodyBytes,
			EnableAuth:   cfg.EnableAuth,
			AdminToken:   cfg.AdminToken,
			Logger:       cfg.Logger,
			Metrics:      app.metrics,
			Region:       cfg.Region,
			Mode:         app.Mode,
		})
		app.httpTransport = transport
		app.transports = append(app.transports, transport)
	}

	if cfg.EnableGRPC {
		transport := grpctransport.NewGRPCTransport(cfg.GRPCListenAddr, app.Ready, app.Mode, grpctransport.GRPCTransportConfig{
			EnableAuth: cfg.EnableAuth,
			AdminToken: cfg.AdminToken,
			KeepAlive:  cfg.GRPCKeepAlive,
			Tracer:     app.tracer,
			Sampler:    app.sampler,
			Metrics:    metrics,
			Region:     cfg.Region,
			Logger:     cfg.Logger,
		})
		if err := transport.ServeRateLimit(app.RateLimitHandler); err != nil {
			return nil, err
		}
		if err := transport.ServeAdmin(app.AdminHandler); err != nil {
			return nil, err
		}
		app.grpcTransport = transport
		app.transports = append(app.transports, transport)
	}

	return app, nil
}

// Start begins background work for the application.
func (app *Application) Start(ctx context.Context) error {
	if app == nil {
		return errors.New("application is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	app.cancel = cancel

	if app.Config != nil && app.Config.RuleDB != nil {
		rules, err := app.Config.RuleDB.LoadAll(ctx)
		if err != nil {
			return err
		}
		if app.RuleCache != nil {
			app.RuleCache.ReplaceAll(rules)
		}
	}

	if app.OutboxPublisher != nil {
		app.wg.Add(1)
		go func() {
			defer app.wg.Done()
			_ = app.OutboxPublisher.Start(ctx)
		}()
	}
	if app.CacheInvalidator != nil {
		app.wg.Add(1)
		go func() {
			defer app.wg.Done()
			_ = app.CacheInvalidator.Subscribe(ctx)
		}()
	}
	if app.CacheSyncWorker != nil {
		app.wg.Add(1)
		go func() {
			defer app.wg.Done()
			_ = app.CacheSyncWorker.Start(ctx)
		}()
	}
	if app.HealthLoop != nil {
		app.wg.Add(1)
		go func() {
			defer app.wg.Done()
			_ = app.HealthLoop.Start(ctx)
		}()
	}
	if app.httpTransport != nil {
		app.wg.Add(1)
		go func() {
			defer app.wg.Done()
			_ = app.httpTransport.Start()
		}()
	}
	if app.grpcTransport != nil {
		app.wg.Add(1)
		go func() {
			defer app.wg.Done()
			_ = app.grpcTransport.Start()
		}()
	}

	app.ready.Store(true)
	if app.logger != nil && app.Config != nil {
		app.logger.Info("application started", map[string]any{
			"region":       app.Config.Region,
			"http_enabled": app.Config.EnableHTTP,
			"grpc_enabled": app.Config.EnableGRPC,
		})
	}

	return nil
}

// Shutdown stops background work for the application.
func (app *Application) Shutdown(ctx context.Context) error {
	if app == nil {
		return errors.New("application is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	app.ready.Store(false)
	if app.logger != nil && app.Config != nil {
		app.logger.Info("application shutdown", map[string]any{
			"region":       app.Config.Region,
			"http_enabled": app.Config.EnableHTTP,
			"grpc_enabled": app.Config.EnableGRPC,
		})
	}
	if app.inflight != nil {
		app.inflight.Close()
	}
	var drainErr error
	if app.inflight != nil {
		drainCtx := ctx
		if app.drainTimeout > 0 {
			var cancel context.CancelFunc
			drainCtx, cancel = context.WithTimeout(ctx, app.drainTimeout)
			defer cancel()
		}
		drainErr = app.inflight.Wait(drainCtx)
	}
	for _, transport := range app.transports {
		if transport == nil {
			continue
		}
		_ = transport.Shutdown(ctx)
	}
	if app.cancel != nil {
		app.cancel()
	}
	done := make(chan struct{})
	go func() {
		app.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		if drainErr != nil {
			return drainErr
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Ready reports whether the application has completed startup.
func (app *Application) Ready() bool {
	if app == nil {
		return false
	}
	return app.ready.Load()
}

// Mode returns the current operating mode.
func (app *Application) Mode() core.OperatingMode {
	if app == nil || app.DegradeControl == nil {
		return core.ModeNormal
	}
	return app.DegradeControl.Mode()
}
