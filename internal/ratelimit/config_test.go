package ratelimit

import (
	"testing"
	"time"
)

func TestConfigValidation(t *testing.T) {
	t.Parallel()

	_, err := NewApplication(&Config{Region: "test", EnableHTTP: true})
	if err == nil {
		t.Fatalf("expected error for missing http listen address")
	}

	_, err = NewApplication(&Config{Region: "test", EnableGRPC: true})
	if err == nil {
		t.Fatalf("expected error for missing grpc listen address")
	}

	_, err = NewApplication(&Config{Region: "test", EnableAuth: true})
	if err == nil {
		t.Fatalf("expected error for missing admin token")
	}

	_, err = NewApplication(&Config{Region: "test", HTTPReadTimeout: -time.Second})
	if err == nil {
		t.Fatalf("expected error for negative timeout")
	}

	_, err = NewApplication(&Config{Region: "test", DegradeRequireRegionQuorum: true, RegionQuorumFraction: 1.5})
	if err == nil {
		t.Fatalf("expected error for invalid region quorum fraction")
	}
}
