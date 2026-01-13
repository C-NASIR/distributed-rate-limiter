// Package ratelimit wires application dependencies.
package ratelimit

import (
	"context"
	"errors"
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
	transports       []Transport
	metrics          *InMemoryMetrics
	tracer           Tracer
	sampler          Sampler
	cancel           context.CancelFunc
	wg               sync.WaitGroup
}

// NewApplication validates configuration and prepares the application.
func NewApplication(cfg *Config) (*Application, error) {
	if cfg == nil {
		return nil, errors.New("config is required")
	}
	if cfg.Region == "" {
		return nil, errors.New("region is required")
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
	if !cfg.CoalesceEnabled {
		cfg.CoalesceEnabled = true
	}
	if cfg.CoalesceTTL == 0 {
		cfg.CoalesceTTL = 10 * time.Millisecond
	}
	if cfg.CoalesceShards == 0 {
		cfg.CoalesceShards = 64
	}
	redis := cfg.Redis
	if redis == nil {
		redis = NewInMemoryRedis(nil)
	}
	membership := cfg.Membership
	if membership == nil {
		membership = NewStaticMembership("local", []string{"local"})
	}
	rules := NewRuleCache()
	degrade := NewDegradeController(redis, membership, cfg.DegradeThresh)
	ownership := &RendezvousOwnership{m: membership}
	fallback := &FallbackLimiter{
		ownership: ownership,
		policy:    normalizeFallbackPolicy(cfg.FallbackPolicy),
		mode:      degrade,
		local:     &LocalLimiterStore{},
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

	rate := NewRateLimitHandler(rules, pool, keys, cfg.Region, respPool, tracer, sampler, metrics, coalescer)
	rate.batch = bp

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
	}

	if cfg.EnableHTTP {
		transport := NewHTTPTransport(cfg.HTTPListenAddr, app.Ready)
		if err := transport.ServeRateLimit(app.RateLimitHandler); err != nil {
			return nil, err
		}
		if err := transport.ServeAdmin(app.AdminHandler); err != nil {
			return nil, err
		}
		transport.metrics = app.metrics
		transport.mode = app.Mode
		app.httpTransport = transport
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

	app.ready.Store(true)

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
	if app.cancel != nil {
		app.cancel()
	}
	app.ready.Store(false)
	for _, transport := range app.transports {
		if transport == nil {
			continue
		}
		_ = transport.Shutdown(ctx)
	}
	done := make(chan struct{})
	go func() {
		app.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
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
