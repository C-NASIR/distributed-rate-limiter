// Package config provides environment config overrides.
package config

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

func applyEnvOverrides(cfg *Config, environ []string) error {
	if cfg == nil {
		return errors.New("config is required")
	}
	values := envMap(environ)
	if value, ok := values["RATELIMIT_REGION"]; ok {
		cfg.Region = value
	}
	if value, ok := values["RATELIMIT_ENABLE_HTTP"]; ok {
		parsed, err := parseBoolEnv("RATELIMIT_ENABLE_HTTP", value)
		if err != nil {
			return err
		}
		cfg.EnableHTTP = parsed
	}
	if value, ok := values["RATELIMIT_HTTP_ADDR"]; ok {
		cfg.HTTPListenAddr = value
	}
	if value, ok := values["RATELIMIT_ENABLE_GRPC"]; ok {
		parsed, err := parseBoolEnv("RATELIMIT_ENABLE_GRPC", value)
		if err != nil {
			return err
		}
		cfg.EnableGRPC = parsed
	}
	if value, ok := values["RATELIMIT_GRPC_ADDR"]; ok {
		cfg.GRPCListenAddr = value
	}
	if value, ok := values["RATELIMIT_ENABLE_AUTH"]; ok {
		parsed, err := parseBoolEnv("RATELIMIT_ENABLE_AUTH", value)
		if err != nil {
			return err
		}
		cfg.EnableAuth = parsed
	}
	if value, ok := values["RATELIMIT_ADMIN_TOKEN"]; ok {
		cfg.AdminToken = value
	}
	if value, ok := values["RATELIMIT_TRACE_SAMPLE_RATE"]; ok {
		parsed, err := parseIntEnv("RATELIMIT_TRACE_SAMPLE_RATE", value)
		if err != nil {
			return err
		}
		cfg.TraceSampleRate = int(parsed)
	}
	if value, ok := values["RATELIMIT_COALESCE_ENABLED"]; ok {
		parsed, err := parseBoolEnv("RATELIMIT_COALESCE_ENABLED", value)
		if err != nil {
			return err
		}
		cfg.CoalesceEnabled = parsed
	}
	if value, ok := values["RATELIMIT_COALESCE_TTL_MS"]; ok {
		parsed, err := parseIntEnv("RATELIMIT_COALESCE_TTL_MS", value)
		if err != nil {
			return err
		}
		cfg.CoalesceTTL = time.Duration(parsed) * time.Millisecond
	}
	if value, ok := values["RATELIMIT_BREAKER_FAILURE_THRESHOLD"]; ok {
		parsed, err := parseIntEnv("RATELIMIT_BREAKER_FAILURE_THRESHOLD", value)
		if err != nil {
			return err
		}
		cfg.BreakerOptions.FailureThreshold = parsed
	}
	if value, ok := values["RATELIMIT_BREAKER_OPEN_MS"]; ok {
		parsed, err := parseIntEnv("RATELIMIT_BREAKER_OPEN_MS", value)
		if err != nil {
			return err
		}
		cfg.BreakerOptions.OpenDuration = time.Duration(parsed) * time.Millisecond
	}
	return nil
}

func envMap(environ []string) map[string]string {
	values := make(map[string]string)
	for _, entry := range environ {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		if key == "" {
			continue
		}
		values[key] = parts[1]
	}
	return values
}

func parseBoolEnv(name, value string) (bool, error) {
	parsed, err := strconv.ParseBool(strings.TrimSpace(value))
	if err != nil {
		return false, errors.New("invalid env value for " + name)
	}
	return parsed, nil
}

func parseIntEnv(name, value string) (int64, error) {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0, errors.New("invalid env value for " + name)
	}
	return parsed, nil
}
