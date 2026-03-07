#!/usr/bin/env bash
set -euo pipefail

PG_CONTAINER_NAME="${PG_CONTAINER_NAME:-cloudclaw-pg}"
REMOVE_DATA="${REMOVE_DATA:-0}"
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

need_cmd docker

if docker ps --format '{{.Names}}' | grep -qx "$PG_CONTAINER_NAME"; then
  log "stopping postgres container: $PG_CONTAINER_NAME"
  docker stop "$PG_CONTAINER_NAME" >/dev/null
fi

if docker ps -a --format '{{.Names}}' | grep -qx "$PG_CONTAINER_NAME"; then
  log "removing postgres container: $PG_CONTAINER_NAME"
  docker rm "$PG_CONTAINER_NAME" >/dev/null
fi

if [ "$REMOVE_DATA" = "1" ]; then
  log "removing volume: $PG_VOLUME"
  docker volume rm "$PG_VOLUME" >/dev/null 2>&1 || true
fi

log "done"
