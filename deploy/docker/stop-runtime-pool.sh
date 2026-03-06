#!/usr/bin/env bash
set -euo pipefail

LABEL="${1:-app=opencode-agent}"

ids=$(docker ps -aq --filter "label=$LABEL")
if [[ -z "${ids}" ]]; then
  echo "no containers found for label: $LABEL"
  exit 0
fi

echo "removing containers with label: $LABEL"
docker rm -f ${ids}
