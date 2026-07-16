#!/usr/bin/env sh

set -eu

NAME=${HOMELOOM_POSTGRES_CONTAINER:-homeloom-postgres}
VOLUME=${HOMELOOM_POSTGRES_VOLUME:-homeloom-postgres-data}
PORT=${HOMELOOM_POSTGRES_PORT:-54329}
PASSWORD=${HOMELOOM_POSTGRES_PASSWORD:-homeloom-dev}

if container list --quiet | grep -qx "$NAME"; then
  :
elif container list --all --quiet | grep -qx "$NAME"; then
  container start "$NAME"
else
  container run --detach \
    --name "$NAME" \
    --publish "127.0.0.1:$PORT:5432" \
    --volume "$VOLUME:/var/lib/postgresql/data" \
    --env POSTGRES_DB=homeloom \
    --env POSTGRES_USER=homeloom \
    --env "POSTGRES_PASSWORD=$PASSWORD" \
    --env PGDATA=/var/lib/postgresql/data/pgdata \
    postgres:17-alpine
fi

attempt=0
until container exec "$NAME" pg_isready -U homeloom -d homeloom >/dev/null 2>&1; do
  attempt=$((attempt + 1))
  if [ "$attempt" -ge 30 ]; then
    echo "PostgreSQL container did not become ready" >&2
    exit 1
  fi
  sleep 1
done

printf '%s\n' "postgres://homeloom:$PASSWORD@127.0.0.1:$PORT/homeloom?sslmode=disable"
