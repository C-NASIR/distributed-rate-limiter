// Package ratelimit provides configuration loading.
package ratelimit

import (
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"strconv"
	"time"
)

// LoadOptions controls config loading.
type LoadOptions struct {
	ConfigPath string
	Args       []string
	Environ    []string
}

// LoadConfig loads configuration from defaults, file, env, and flags.
func LoadConfig(opts LoadOptions) (*Config, error) {
	args := opts.Args
	if args == nil {
		args = os.Args[1:]
	}
	environ := opts.Environ
	if environ == nil {
		environ = os.Environ()
	}

	flagOverrides, err := parseFlagOverrides(args)
	if err != nil {
		return nil, err
	}

	configPath := opts.ConfigPath
	if flagOverrides.ConfigPath != nil {
		configPath = *flagOverrides.ConfigPath
	}

	cfg := defaultConfig()
	if configPath != "" {
		fileOverrides, err := loadConfigFile(configPath)
		if err != nil {
			return nil, err
		}
		applyConfigOverrides(cfg, fileOverrides)
	}
	if err := applyEnvOverrides(cfg, environ); err != nil {
		return nil, err
	}
	applyFlagOverrides(cfg, flagOverrides)
	return cfg, nil
}

func defaultConfig() *Config {
	return &Config{
		Region:          "local",
		EnableHTTP:      true,
		HTTPListenAddr:  ":8080",
		EnableGRPC:      true,
		GRPCListenAddr:  ":9090",
		TraceSampleRate: 100,
		CoalesceEnabled: true,
		CoalesceTTL:     10 * time.Millisecond,
		CoalesceShards:  64,
		BreakerOptions: CircuitOptions{
			FailureThreshold: 10,
			OpenDuration:     200 * time.Millisecond,
			HalfOpenMaxCalls: 5,
		},
		CacheSyncInterval: 200 * time.Millisecond,
		HealthInterval:    100 * time.Millisecond,
		LimiterPolicy: LimiterPolicy{
			Shards:          16,
			MaxEntriesShard: 1024,
			QuiesceWindow:   50 * time.Millisecond,
			CloseTimeout:    2 * time.Second,
		},
		FallbackPolicy: FallbackPolicy{
			LocalCapPerWindow:      100,
			DenyWhenNotOwner:       true,
			EmergencyAllowSmallCap: true,
			EmergencyCapPerWindow:  10,
		},
		DegradeThresh: DegradeThresholds{
			RedisUnhealthyFor:   500 * time.Millisecond,
			MembershipUnhealthy: 2 * time.Second,
		},
		RequestTimeout:   2 * time.Second,
		DrainTimeout:     5 * time.Second,
		HTTPReadTimeout:  5 * time.Second,
		HTTPWriteTimeout: 10 * time.Second,
		HTTPIdleTimeout:  60 * time.Second,
		GRPCKeepAlive:    60 * time.Second,
		MaxBodyBytes:     1 << 20,
	}
}

type configOverrides struct {
	Region                     *string              `json:"Region"`
	RegionGroup                *string              `json:"RegionGroup"`
	EnableGlobalOwnership      *bool                `json:"EnableGlobalOwnership"`
	GlobalOwnershipFallback    *bool                `json:"GlobalOwnershipFallback"`
	RegionInstanceWeight       *int                 `json:"RegionInstanceWeight"`
	DegradeRequireRegionQuorum *bool                `json:"DegradeRequireRegionQuorum"`
	RegionQuorumFraction       *float64             `json:"RegionQuorumFraction"`
	Channel                    *string              `json:"Channel"`
	HTTPListenAddr             *string              `json:"HTTPListenAddr"`
	EnableHTTP                 *bool                `json:"EnableHTTP"`
	EnableGRPC                 *bool                `json:"EnableGRPC"`
	GRPCListenAddr             *string              `json:"GRPCListenAddr"`
	GRPCKeepAlive              *durationValue       `json:"GRPCKeepAlive"`
	TraceSampleRate            *int                 `json:"TraceSampleRate"`
	CoalesceEnabled            *bool                `json:"CoalesceEnabled"`
	CoalesceTTL                *durationValue       `json:"CoalesceTTL"`
	CoalesceShards             *int                 `json:"CoalesceShards"`
	BreakerOptions             *circuitOptionsInput `json:"BreakerOptions"`
	CacheSyncInterval          *durationValue       `json:"CacheSyncInterval"`
	HealthInterval             *durationValue       `json:"HealthInterval"`
	LimiterPolicy              *limiterPolicyInput  `json:"LimiterPolicy"`
	FallbackPolicy             *fallbackPolicyInput `json:"FallbackPolicy"`
	DegradeThresh              *degradeThreshInput  `json:"DegradeThresh"`
	HTTPReadTimeout            *durationValue       `json:"HTTPReadTimeout"`
	HTTPWriteTimeout           *durationValue       `json:"HTTPWriteTimeout"`
	HTTPIdleTimeout            *durationValue       `json:"HTTPIdleTimeout"`
	RequestTimeout             *durationValue       `json:"RequestTimeout"`
	DrainTimeout               *durationValue       `json:"DrainTimeout"`
	MaxBodyBytes               *int64               `json:"MaxBodyBytes"`
	EnableAuth                 *bool                `json:"EnableAuth"`
	AdminToken                 *string              `json:"AdminToken"`
}

type circuitOptionsInput struct {
	FailureThreshold *int64         `json:"FailureThreshold"`
	OpenDuration     *durationValue `json:"OpenDuration"`
	HalfOpenMaxCalls *int64         `json:"HalfOpenMaxCalls"`
}

type limiterPolicyInput struct {
	Shards          *int           `json:"Shards"`
	MaxEntriesShard *int           `json:"MaxEntriesShard"`
	QuiesceWindow   *durationValue `json:"QuiesceWindow"`
	CloseTimeout    *durationValue `json:"CloseTimeout"`
}

type fallbackPolicyInput struct {
	LocalCapPerWindow      *int64 `json:"LocalCapPerWindow"`
	DenyWhenNotOwner       *bool  `json:"DenyWhenNotOwner"`
	EmergencyAllowSmallCap *bool  `json:"EmergencyAllowSmallCap"`
	EmergencyCapPerWindow  *int64 `json:"EmergencyCapPerWindow"`
}

type degradeThreshInput struct {
	RedisUnhealthyFor   *durationValue `json:"RedisUnhealthyFor"`
	MembershipUnhealthy *durationValue `json:"MembershipUnhealthy"`
	ErrorRateWindow     *durationValue `json:"ErrorRateWindow"`
}

type durationValue struct {
	Value time.Duration
	Set   bool
}

func (d *durationValue) UnmarshalJSON(data []byte) error {
	if d == nil {
		return nil
	}
	if string(data) == "null" {
		return nil
	}
	var number json.Number
	if err := json.Unmarshal(data, &number); err == nil {
		value, err := number.Int64()
		if err != nil {
			return err
		}
		d.Value = time.Duration(value) * time.Millisecond
		d.Set = true
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		value, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return err
		}
		d.Value = time.Duration(value) * time.Millisecond
		d.Set = true
		return nil
	}
	return errors.New("invalid duration value")
}

type flagOverrides struct {
	ConfigPath              *string
	Region                  *string
	EnableHTTP              *bool
	HTTPListenAddr          *string
	EnableGRPC              *bool
	GRPCListenAddr          *string
	EnableAuth              *bool
	AdminToken              *string
	TraceSampleRate         *int
	CoalesceEnabled         *bool
	CoalesceTTLMS           *int
	BreakerFailureThreshold *int
	BreakerOpenMS           *int
}

func parseFlagOverrides(args []string) (flagOverrides, error) {
	fs := flag.NewFlagSet("ratelimit", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	setFlagUsage(fs)

	configPath := fs.String("config", "", "config file path")
	region := fs.String("region", "", "region value")
	enableHTTP := fs.Bool("enable_http", false, "enable http")
	httpAddr := fs.String("http_addr", "", "http address")
	enableGRPC := fs.Bool("enable_grpc", false, "enable grpc")
	grpcAddr := fs.String("grpc_addr", "", "grpc address")
	enableAuth := fs.Bool("enable_auth", false, "enable auth")
	adminToken := fs.String("admin_token", "", "admin token")
	traceSampleRate := fs.Int("trace_sample_rate", 0, "trace sample rate")
	coalesceEnabled := fs.Bool("coalesce_enabled", false, "coalesce enabled")
	coalesceTTL := fs.Int("coalesce_ttl_ms", 0, "coalesce ttl ms")
	breakerFailure := fs.Int("breaker_failure_threshold", 0, "breaker failure threshold")
	breakerOpen := fs.Int("breaker_open_ms", 0, "breaker open ms")

	if err := fs.Parse(args); err != nil {
		return flagOverrides{}, errors.New("invalid flag values")
	}

	overrides := flagOverrides{}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "config":
			overrides.ConfigPath = configPath
		case "region":
			overrides.Region = region
		case "enable_http":
			overrides.EnableHTTP = enableHTTP
		case "http_addr":
			overrides.HTTPListenAddr = httpAddr
		case "enable_grpc":
			overrides.EnableGRPC = enableGRPC
		case "grpc_addr":
			overrides.GRPCListenAddr = grpcAddr
		case "enable_auth":
			overrides.EnableAuth = enableAuth
		case "admin_token":
			overrides.AdminToken = adminToken
		case "trace_sample_rate":
			overrides.TraceSampleRate = traceSampleRate
		case "coalesce_enabled":
			overrides.CoalesceEnabled = coalesceEnabled
		case "coalesce_ttl_ms":
			overrides.CoalesceTTLMS = coalesceTTL
		case "breaker_failure_threshold":
			overrides.BreakerFailureThreshold = breakerFailure
		case "breaker_open_ms":
			overrides.BreakerOpenMS = breakerOpen
		}
	})
	return overrides, nil
}

func setFlagUsage(fs *flag.FlagSet) {
	if fs == nil {
		return
	}
	fs.Usage = func() {}
}

func loadConfigFile(path string) (*configOverrides, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var overrides configOverrides
	if err := json.Unmarshal(data, &overrides); err != nil {
		return nil, err
	}
	return &overrides, nil
}

func applyConfigOverrides(cfg *Config, overrides *configOverrides) {
	if cfg == nil || overrides == nil {
		return
	}
	if overrides.Region != nil {
		cfg.Region = *overrides.Region
	}
	if overrides.RegionGroup != nil {
		cfg.RegionGroup = *overrides.RegionGroup
	}
	if overrides.EnableGlobalOwnership != nil {
		cfg.EnableGlobalOwnership = *overrides.EnableGlobalOwnership
	}
	if overrides.GlobalOwnershipFallback != nil {
		cfg.GlobalOwnershipFallback = *overrides.GlobalOwnershipFallback
	}
	if overrides.RegionInstanceWeight != nil {
		cfg.RegionInstanceWeight = *overrides.RegionInstanceWeight
	}
	if overrides.DegradeRequireRegionQuorum != nil {
		cfg.DegradeRequireRegionQuorum = *overrides.DegradeRequireRegionQuorum
	}
	if overrides.RegionQuorumFraction != nil {
		cfg.RegionQuorumFraction = *overrides.RegionQuorumFraction
	}
	if overrides.Channel != nil {
		cfg.Channel = *overrides.Channel
	}
	if overrides.HTTPListenAddr != nil {
		cfg.HTTPListenAddr = *overrides.HTTPListenAddr
	}
	if overrides.EnableHTTP != nil {
		cfg.EnableHTTP = *overrides.EnableHTTP
	}
	if overrides.EnableGRPC != nil {
		cfg.EnableGRPC = *overrides.EnableGRPC
	}
	if overrides.GRPCListenAddr != nil {
		cfg.GRPCListenAddr = *overrides.GRPCListenAddr
	}
	if overrides.GRPCKeepAlive != nil && overrides.GRPCKeepAlive.Set {
		cfg.GRPCKeepAlive = overrides.GRPCKeepAlive.Value
	}
	if overrides.TraceSampleRate != nil {
		cfg.TraceSampleRate = *overrides.TraceSampleRate
	}
	if overrides.CoalesceEnabled != nil {
		cfg.CoalesceEnabled = *overrides.CoalesceEnabled
	}
	if overrides.CoalesceTTL != nil && overrides.CoalesceTTL.Set {
		cfg.CoalesceTTL = overrides.CoalesceTTL.Value
	}
	if overrides.CoalesceShards != nil {
		cfg.CoalesceShards = *overrides.CoalesceShards
	}
	if overrides.BreakerOptions != nil {
		if overrides.BreakerOptions.FailureThreshold != nil {
			cfg.BreakerOptions.FailureThreshold = *overrides.BreakerOptions.FailureThreshold
		}
		if overrides.BreakerOptions.OpenDuration != nil && overrides.BreakerOptions.OpenDuration.Set {
			cfg.BreakerOptions.OpenDuration = overrides.BreakerOptions.OpenDuration.Value
		}
		if overrides.BreakerOptions.HalfOpenMaxCalls != nil {
			cfg.BreakerOptions.HalfOpenMaxCalls = *overrides.BreakerOptions.HalfOpenMaxCalls
		}
	}
	if overrides.CacheSyncInterval != nil && overrides.CacheSyncInterval.Set {
		cfg.CacheSyncInterval = overrides.CacheSyncInterval.Value
	}
	if overrides.HealthInterval != nil && overrides.HealthInterval.Set {
		cfg.HealthInterval = overrides.HealthInterval.Value
	}
	if overrides.LimiterPolicy != nil {
		if overrides.LimiterPolicy.Shards != nil {
			cfg.LimiterPolicy.Shards = *overrides.LimiterPolicy.Shards
		}
		if overrides.LimiterPolicy.MaxEntriesShard != nil {
			cfg.LimiterPolicy.MaxEntriesShard = *overrides.LimiterPolicy.MaxEntriesShard
		}
		if overrides.LimiterPolicy.QuiesceWindow != nil && overrides.LimiterPolicy.QuiesceWindow.Set {
			cfg.LimiterPolicy.QuiesceWindow = overrides.LimiterPolicy.QuiesceWindow.Value
		}
		if overrides.LimiterPolicy.CloseTimeout != nil && overrides.LimiterPolicy.CloseTimeout.Set {
			cfg.LimiterPolicy.CloseTimeout = overrides.LimiterPolicy.CloseTimeout.Value
		}
	}
	if overrides.FallbackPolicy != nil {
		if overrides.FallbackPolicy.LocalCapPerWindow != nil {
			cfg.FallbackPolicy.LocalCapPerWindow = *overrides.FallbackPolicy.LocalCapPerWindow
		}
		if overrides.FallbackPolicy.DenyWhenNotOwner != nil {
			cfg.FallbackPolicy.DenyWhenNotOwner = *overrides.FallbackPolicy.DenyWhenNotOwner
		}
		if overrides.FallbackPolicy.EmergencyAllowSmallCap != nil {
			cfg.FallbackPolicy.EmergencyAllowSmallCap = *overrides.FallbackPolicy.EmergencyAllowSmallCap
		}
		if overrides.FallbackPolicy.EmergencyCapPerWindow != nil {
			cfg.FallbackPolicy.EmergencyCapPerWindow = *overrides.FallbackPolicy.EmergencyCapPerWindow
		}
	}
	if overrides.DegradeThresh != nil {
		if overrides.DegradeThresh.RedisUnhealthyFor != nil && overrides.DegradeThresh.RedisUnhealthyFor.Set {
			cfg.DegradeThresh.RedisUnhealthyFor = overrides.DegradeThresh.RedisUnhealthyFor.Value
		}
		if overrides.DegradeThresh.MembershipUnhealthy != nil && overrides.DegradeThresh.MembershipUnhealthy.Set {
			cfg.DegradeThresh.MembershipUnhealthy = overrides.DegradeThresh.MembershipUnhealthy.Value
		}
		if overrides.DegradeThresh.ErrorRateWindow != nil && overrides.DegradeThresh.ErrorRateWindow.Set {
			cfg.DegradeThresh.ErrorRateWindow = overrides.DegradeThresh.ErrorRateWindow.Value
		}
	}
	if overrides.HTTPReadTimeout != nil && overrides.HTTPReadTimeout.Set {
		cfg.HTTPReadTimeout = overrides.HTTPReadTimeout.Value
	}
	if overrides.HTTPWriteTimeout != nil && overrides.HTTPWriteTimeout.Set {
		cfg.HTTPWriteTimeout = overrides.HTTPWriteTimeout.Value
	}
	if overrides.HTTPIdleTimeout != nil && overrides.HTTPIdleTimeout.Set {
		cfg.HTTPIdleTimeout = overrides.HTTPIdleTimeout.Value
	}
	if overrides.RequestTimeout != nil && overrides.RequestTimeout.Set {
		cfg.RequestTimeout = overrides.RequestTimeout.Value
	}
	if overrides.DrainTimeout != nil && overrides.DrainTimeout.Set {
		cfg.DrainTimeout = overrides.DrainTimeout.Value
	}
	if overrides.MaxBodyBytes != nil {
		cfg.MaxBodyBytes = *overrides.MaxBodyBytes
	}
	if overrides.EnableAuth != nil {
		cfg.EnableAuth = *overrides.EnableAuth
	}
	if overrides.AdminToken != nil {
		cfg.AdminToken = *overrides.AdminToken
	}
}

func applyFlagOverrides(cfg *Config, overrides flagOverrides) {
	if cfg == nil {
		return
	}
	if overrides.Region != nil {
		cfg.Region = *overrides.Region
	}
	if overrides.EnableHTTP != nil {
		cfg.EnableHTTP = *overrides.EnableHTTP
	}
	if overrides.HTTPListenAddr != nil {
		cfg.HTTPListenAddr = *overrides.HTTPListenAddr
	}
	if overrides.EnableGRPC != nil {
		cfg.EnableGRPC = *overrides.EnableGRPC
	}
	if overrides.GRPCListenAddr != nil {
		cfg.GRPCListenAddr = *overrides.GRPCListenAddr
	}
	if overrides.EnableAuth != nil {
		cfg.EnableAuth = *overrides.EnableAuth
	}
	if overrides.AdminToken != nil {
		cfg.AdminToken = *overrides.AdminToken
	}
	if overrides.TraceSampleRate != nil {
		cfg.TraceSampleRate = *overrides.TraceSampleRate
	}
	if overrides.CoalesceEnabled != nil {
		cfg.CoalesceEnabled = *overrides.CoalesceEnabled
	}
	if overrides.CoalesceTTLMS != nil {
		cfg.CoalesceTTL = time.Duration(*overrides.CoalesceTTLMS) * time.Millisecond
	}
	if overrides.BreakerFailureThreshold != nil {
		cfg.BreakerOptions.FailureThreshold = int64(*overrides.BreakerFailureThreshold)
	}
	if overrides.BreakerOpenMS != nil {
		cfg.BreakerOptions.OpenDuration = time.Duration(*overrides.BreakerOpenMS) * time.Millisecond
	}
}
