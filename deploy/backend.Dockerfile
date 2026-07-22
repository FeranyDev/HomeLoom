# syntax=docker/dockerfile:1.7
FROM node:24-alpine AS web
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY frontend/ ./
RUN npm run build:embed

FROM golang:1.26-alpine AS build
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY backend/ ./
COPY --from=web /src/backend/internal/webui/dist ./internal/webui/dist
ARG VERSION
ARG COMMIT=unknown
ARG BUILD_TIME=unknown
RUN --mount=type=cache,target=/root/.cache/go-build test -n "$VERSION" \
    && CGO_ENABLED=0 go build -trimpath -tags embed_webui \
    -ldflags "-s -w -X github.com/feranydev/homeloom/backend/internal/buildinfo.Version=${VERSION} -X github.com/feranydev/homeloom/backend/internal/buildinfo.Commit=${COMMIT} -X github.com/feranydev/homeloom/backend/internal/buildinfo.BuildTime=${BUILD_TIME}" \
    -o /out/homeloom ./cmd/homeloom

FROM alpine:3.23
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S homeloom \
    && adduser -S -G homeloom -h /app homeloom \
    && mkdir -p /data /app \
    && chown -R homeloom:homeloom /data /app \
    && ln -s /data /app/data
WORKDIR /app
COPY --from=build /out/homeloom /usr/local/bin/homeloom
ENV HOMELOOM_HTTP_ADDRESS=0.0.0.0:8090 \
    HOMELOOM_DATABASE_URL=postgres://homeloom:homeloom-dev@127.0.0.1:5432/homeloom?sslmode=disable \
    HOMELOOM_MASTER_KEY=/data/homeloom.key
VOLUME ["/data"]
EXPOSE 8090 51826
HEALTHCHECK --interval=15s --timeout=3s --start-period=10s --retries=3 CMD wget -q -O /dev/null http://127.0.0.1:8090/ready || exit 1
USER homeloom
ENTRYPOINT ["/usr/local/bin/homeloom"]
