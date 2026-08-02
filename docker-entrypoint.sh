#!/bin/sh
# Container entrypoint for the `status` service.
#
# Production (LITESTREAM_ENABLED=true, the default — Coolify): restore the SQLite
# database from Cloudflare R2 if the local file is absent (a fresh volume or a
# rebuilt image), then run the app under `litestream replicate -exec` so every
# write is streamed to R2. Litestream exits when the app exits, so Coolify sees a
# normal process lifecycle.
#
# Local (LITESTREAM_ENABLED=false, docker-compose): run the app directly with no
# R2 dependency — the offline end-to-end harness.
set -eu

: "${STATUS_DB_PATH:?STATUS_DB_PATH must be set}"
mkdir -p "$(dirname "${STATUS_DB_PATH}")"

if [ "${LITESTREAM_ENABLED:-true}" = "true" ]; then
  echo "entrypoint: restoring ${STATUS_DB_PATH} from R2 if it does not exist"
  # -if-db-not-exists: skip if the DB is already on the volume.
  # -if-replica-exists: on a first-ever deploy the R2 replica is empty, so a plain
  #   restore would exit non-zero and, under `set -e`, crash the container. This
  #   flag makes that case a clean no-op so the app starts fresh and begins replicating.
  litestream restore -if-db-not-exists -if-replica-exists -config /etc/litestream.yml "${STATUS_DB_PATH}"
  echo "entrypoint: starting status under 'litestream replicate -exec'"
  exec litestream replicate -config /etc/litestream.yml -exec "status"
else
  echo "entrypoint: LITESTREAM_ENABLED=false — starting status without replication"
  exec status
fi
