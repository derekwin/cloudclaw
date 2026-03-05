#!/usr/bin/env bash
set -euo pipefail

COUNT="${1:-3}"
IMAGE="${2:-ghcr.io/sipeed/picoclaw:latest}"
NAME_PREFIX="${3:-picoclaw-agent}"
LABEL="${4:-app=picoclaw-agent}"

for i in $(seq 1 "$COUNT"); do
  name="${NAME_PREFIX}-${i}"
  if docker ps -a --format '{{.Names}}' | grep -Fxq "$name"; then
    echo "skip existing container: $name"
    continue
  fi
  echo "starting $name from $IMAGE"
  docker run -d \
    --name "$name" \
    --label "$LABEL" \
    "$IMAGE" \
    /bin/sh -lc 'sleep infinity' >/dev/null
  echo "started $name"
done

echo "running containers with label $LABEL:"
docker ps --filter "label=$LABEL" --format 'table {{.Names}}\t{{.Image}}\t{{.Status}}'
