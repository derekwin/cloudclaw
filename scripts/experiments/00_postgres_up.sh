#!/usr/bin/env bash
set -euo pipefail

ACTION="${1:-up}"
PG_CONTAINER_NAME="${PG_CONTAINER_NAME:-cloudclaw-pg}"
PG_PORT="${PG_PORT:-15432}"
PG_USER="${PG_USER:-cloudclaw}"
PG_PASSWORD="${PG_PASSWORD:-cloudclaw}"
PG_DB="${PG_DB:-cloudclaw}"
PG_VOLUME="${PG_VOLUME:-cloudclaw-pgdata}"

log() {
  printf '[pg-setup] %s\n' "$*"
}

die() {
  printf '[pg-setup][error] %s\n' "$*" >&2
  exit 1
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "missing command: $1"
}

wait_pg_ready() {
  local retry
  for retry in $(seq 1 60); do
    if docker exec "$PG_CONTAINER_NAME" pg_isready -U "$PG_USER" -d "$PG_DB" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  return 1
}

need_cmd docker

if [[ "$ACTION" != "up" && "$ACTION" != "clean" ]]; then
  die "usage: $0 [up|clean]"
fi

if [[ "$ACTION" == "clean" ]]; then
  log "clean mode: recreate postgres container and volume"
  if docker ps --format '{{.Names}}' | grep -qx "$PG_CONTAINER_NAME"; then
    log "stopping postgres container: $PG_CONTAINER_NAME"
    docker stop "$PG_CONTAINER_NAME" >/dev/null
  fi
  if docker ps -a --format '{{.Names}}' | grep -qx "$PG_CONTAINER_NAME"; then
    log "removing postgres container: $PG_CONTAINER_NAME"
    docker rm "$PG_CONTAINER_NAME" >/dev/null
  fi
  log "removing postgres volume: $PG_VOLUME"
  docker volume rm "$PG_VOLUME" >/dev/null 2>&1 || true
fi

if docker ps --format '{{.Names}}' | grep -qx "$PG_CONTAINER_NAME"; then
  log "postgres container already running: $PG_CONTAINER_NAME"
elif docker ps -a --format '{{.Names}}' | grep -qx "$PG_CONTAINER_NAME"; then
  log "starting existing postgres container: $PG_CONTAINER_NAME"
  docker start "$PG_CONTAINER_NAME" >/dev/null
else
  log "creating postgres container: $PG_CONTAINER_NAME"
  docker run -d \
    --name "$PG_CONTAINER_NAME" \
    --restart unless-stopped \
    -e POSTGRES_USER="$PG_USER" \
    -e POSTGRES_PASSWORD="$PG_PASSWORD" \
    -e POSTGRES_DB="$PG_DB" \
    -p "${PG_PORT}:5432" \
    -v "${PG_VOLUME}:/var/lib/postgresql/data" \
    postgres:16 >/dev/null
fi

if ! wait_pg_ready; then
  die "postgres did not become ready in time"
fi

dsn="postgres://${PG_USER}:${PG_PASSWORD}@127.0.0.1:${PG_PORT}/${PG_DB}?sslmode=disable"

cat <<EOF
[pg-setup] postgres is ready
[pg-setup] action: $ACTION
[pg-setup] container: $PG_CONTAINER_NAME
[pg-setup] dsn: $dsn

Run these in your current shell:
  export DB_DRIVER=postgres
  export DB_DSN='$dsn'
  export CC_DB_DRIVER=postgres
  export CC_DB_DSN='$dsn'

Then restart runner:
  AGENT_RUNTIME=opencode bash deploy/server/cloudclawctl.sh runner restart
EOF
