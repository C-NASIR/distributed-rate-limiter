#!/bin/sh
set -e

go run ./cmd/ratelimit --config configs/local.json
