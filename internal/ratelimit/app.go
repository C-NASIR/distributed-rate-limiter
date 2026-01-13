// Package ratelimit wires application dependencies.
package ratelimit

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// Application holds core components for the service.
type Application struct {
	Config           *Config
	RuleCache        *RuleCache
	LimiterPool      *LimiterPool
	KeyBuilder       *KeyBuilder
	DegradeControl   *DegradeController
	FallbackLimiter  *FallbackLimiter
	LimiterFactory   *LimiterFactory
	RateLimitHandler *RateLimitHandler
	AdminHandler     *AdminHandler
	OutboxPublisher  *OutboxPublisher
	CacheInvalidator *CacheInvalidator
	CacheSyncWorker  *CacheSyncWorker
	HealthLoop       *HealthLoop
	ready            atomic.Bool
	httpTransport    *HTTPTransport
	grpcTransport    *GRPCTransport
	transports       []Transport
	metrics          *InMemoryMetrics
	tracer           Tracer
	sampler          Sampler
	cancel           context.CancelFunc
	wg               sync.WaitGroup
	inflight         *InFlight
	drainTimeout     time.Duration
	logger           Logger
}

// NewApplication validates configuration and prepares the application.
func NewApplication(cfg *Config) (*Application, error) {
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
		cfg.Logger = NewStdLogger(os.Stdout)
	}
	if cfg.RuleDB == nil {
		cfg.RuleDB = NewInMemoryRuleDB(nil)
	}
	if cfg.Outbox == nil {
		cfg.Outbox = NewInMemoryOutbox()
	}
	if cfg.PubSub == nil {
		cfg.PubSub = NewInMemoryPubSub()
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
	if cfg.FallbackPolicy == (FallbackPolicy{}) {
		cfg.FallbackPolicy = normalizeFallbackPolicy(cfg.FallbackPolicy)
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
		redis = NewInMemoryRedis(nil)
	}
	membership := cfg.Membership
	if membership == nil {
		membership = NewSingleInstanceMembership("local", cfg.Region)
	}
	if static, ok := membership.(*StaticMembership); ok {
		static.SetSelfWeight(cfg.RegionInstanceWeight)
	}
	rules := NewRuleCache()
	degrade := NewDegradeController(redis, membership, cfg.DegradeThresh, cfg.Region, cfg.DegradeRequireRegionQuorum, cfg.RegionQuorumFraction)
	degrade.SetLogger(cfg.Logger)
	ownership := NewRendezvousOwnership(membership, cfg.Region, cfg.EnableGlobalOwnership, cfg.GlobalOwnershipFallback)
	fallback := &FallbackLimiter{
		ownership:   ownership,
		mship:       membership,
		region:      cfg.Region,
		regionGroup: cfg.RegionGroup,
		policy:      normalizeFallbackPolicy(cfg.FallbackPolicy),
		mode:        degrade,
		local:       &LocalLimiterStore{},
	}
	breaker := NewCircuitBreaker(cfg.BreakerOptions)
	factory := &LimiterFactory{redis: redis, fallback: fallback, mode: degrade, breaker: breaker}
	pool := NewLimiterPool(rules, factory, cfg.LimiterPolicy)
	keys := &KeyBuilder{bufPool: NewByteBufferPool(4096)}
	respPool := NewResponsePool()
	bp := &BatchPlanner{
		indexPool: NewIndexPool(),
		keyPool:   NewKeyPool(),
		costPool:  NewCostPool(),
	}
	metrics := NewInMemoryMetrics()
	tracer := Tracer(NoopTracer{})
	sampler := Sampler(HashSampler{rate: cfg.TraceSampleRate})
	var coalescer *Coalescer
	if cfg.CoalesceEnabled {
		coalescer = NewCoalescer(cfg.CoalesceShards, cfg.CoalesceTTL)
	}

	inflight := NewInFlight()
	rate := NewRateLimitHandler(rules, pool, keys, cfg.Region, respPool, tracer, sampler, metrics, coalescer)
	rate.batch = bp
	rate.inflight = inflight
	rate.requestTimeout = cfg.RequestTimeout

	var outboxWriter OutboxWriter
	if writer, ok := cfg.Outbox.(OutboxWriter); ok {
		outboxWriter = writer
	}

	admin := &AdminHandler{db: cfg.RuleDB, rules: rules, outboxWriter: outboxWriter, tracer: cfg.Tracer, metrics: cfg.Metrics}
	pub := &OutboxPublisher{outbox: cfg.Outbox, pubsub: cfg.PubSub, channel: cfg.Channel}
	invalid := &CacheInvalidator{db: cfg.RuleDB, rules: rules, pool: pool, pubsub: cfg.PubSub, channel: cfg.Channel}
	syncer := &CacheSyncWorker{db: cfg.RuleDB, rules: rules, interval: cfg.CacheSyncInterval}
	health := &HealthLoop{degrade: degrade, interval: cfg.HealthInterval}

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
		metrics:          metrics,
		tracer:           tracer,
		sampler:          sampler,
		inflight:         inflight,
		drainTimeout:     cfg.DrainTimeout,
		logger:           cfg.Logger,
	}

	if cfg.EnableHTTP {
		transport := NewHTTPTransport(cfg.HTTPListenAddr, app.Ready)
		if err := transport.ServeRateLimit(app.RateLimitHandler); err != nil {
			return nil, err
		}
		if err := transport.ServeAdmin(app.AdminHandler); err != nil {
			return nil, err
		}
		transport.readTimeout = cfg.HTTPReadTimeout
		transport.writeTimeout = cfg.HTTPWriteTimeout
		transport.idleTimeout = cfg.HTTPIdleTimeout
		transport.maxBodyBytes = cfg.MaxBodyBytes
		transport.enableAuth = cfg.EnableAuth
		transport.adminToken = cfg.AdminToken
		transport.logger = cfg.Logger
		transport.metrics = app.metrics
		transport.region = cfg.Region
		transport.mode = app.Mode
		app.httpTransport = transport
		app.transports = append(app.transports, transport)
	}

	if cfg.EnableGRPC {
		transport := NewGRPCTransport(cfg.GRPCListenAddr, app.Ready, app.Mode, grpcTransportConfig{
			enableAuth: cfg.EnableAuth,
			adminToken: cfg.AdminToken,
			keepAlive:  cfg.GRPCKeepAlive,
			tracer:     app.tracer,
			sampler:    app.sampler,
			metrics:    app.metrics,
			region:     cfg.Region,
			logger:     cfg.Logger,
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
func (app *Application) Mode() OperatingMode {
	if app == nil || app.DegradeControl == nil {
		return ModeNormal
	}
	return app.DegradeControl.Mode()
}
