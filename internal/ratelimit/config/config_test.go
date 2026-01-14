package config_test

import (
	"testing"
	"time"

	"ratelimit/internal/ratelimit/app"
	"ratelimit/internal/ratelimit/config"
)

func TestConfigValidation(t *testing.T) {
	t.Parallel()

	_, err := app.NewApplication(&config.Config{Region: "test", EnableHTTP: true})
	if err == nil {
		t.Fatalf("expected error for missing http listen address")
	}

	_, err = app.NewApplication(&config.Config{Region: "test", EnableGRPC: true})
	if err == nil {
		t.Fatalf("expected error for missing grpc listen address")
	}

	_, err = app.NewApplication(&config.Config{Region: "test", EnableAuth: true})
	if err == nil {
		t.Fatalf("expected error for missing admin token")
	}

	_, err = app.NewApplication(&config.Config{Region: "test", HTTPReadTimeout: -time.Second})
	if err == nil {
		t.Fatalf("expected error for negative timeout")
	}

	_, err = app.NewApplication(&config.Config{Region: "test", DegradeRequireRegionQuorum: true, RegionQuorumFraction: 1.5})
	if err == nil {
		t.Fatalf("expected error for invalid region quorum fraction")
	}
}
