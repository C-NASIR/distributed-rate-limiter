#!/bin/sh
set -e

docker compose down
docker volume rm postgres_data >/dev/null 2>&1 || true
docker compose up --build
