package main

import (
	"flag"
	"fmt"
	"io"
)

func newFlagSet(name string, output io.Writer) *flag.FlagSet {
	if output == nil {
		output = io.Discard
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(output)
	fs.String("config", "", "config file path")
	fs.String("region", "", "region value")
	fs.Bool("enable_http", false, "enable http")
	fs.String("http_addr", "", "http address")
	fs.Bool("enable_grpc", false, "enable grpc")
	fs.String("grpc_addr", "", "grpc address")
	fs.Bool("enable_auth", false, "enable auth")
	fs.String("admin_token", "", "admin token")
	fs.Int("trace_sample_rate", 0, "trace sample rate")
	fs.Bool("coalesce_enabled", false, "coalesce enabled")
	fs.Int("coalesce_ttl_ms", 0, "coalesce ttl ms")
	fs.Int("breaker_failure_threshold", 0, "breaker failure threshold")
	fs.Int("breaker_open_ms", 0, "breaker open ms")
	fs.Usage = func() {
		printUsage(output)
	}
	return fs
}

func printUsage(w io.Writer) {
	if w == nil {
		return
	}
	fmt.Fprintln(w, "Usage")
	fmt.Fprintln(w, "  ratelimit [print_config] [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Flags")
	fmt.Fprintln(w, "  config string config file path")
	fmt.Fprintln(w, "  region string region value")
	fmt.Fprintln(w, "  enable_http bool enable http")
	fmt.Fprintln(w, "  http_addr string http address")
	fmt.Fprintln(w, "  enable_grpc bool enable grpc")
	fmt.Fprintln(w, "  grpc_addr string grpc address")
	fmt.Fprintln(w, "  enable_auth bool enable auth")
	fmt.Fprintln(w, "  admin_token string admin token")
	fmt.Fprintln(w, "  trace_sample_rate int trace sample rate")
	fmt.Fprintln(w, "  coalesce_enabled bool coalesce enabled")
	fmt.Fprintln(w, "  coalesce_ttl_ms int coalesce ttl ms")
	fmt.Fprintln(w, "  breaker_failure_threshold int breaker failure threshold")
	fmt.Fprintln(w, "  breaker_open_ms int breaker open ms")
}
