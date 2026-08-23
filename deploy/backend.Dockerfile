FROM node:24-alpine AS node-deps
COPY .cache/npm /root/.npm

FROM node-deps AS web
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --offline
COPY frontend/ ./
RUN npm run build:embed

FROM golang:1.26-alpine AS go-deps
ENV GOMODCACHE=/go/pkg/mod \
    GOPROXY=off
COPY .cache/go-mod /go/pkg/mod

FROM go-deps AS build
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
COPY --from=web /src/backend/internal/webui/dist ./internal/webui/dist
ARG VERSION
ARG COMMIT=unknown
ARG BUILD_TIME=unknown
ARG TARGETOS=linux
ARG TARGETARCH
RUN test -n "$VERSION" \
    && CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="${TARGETARCH:-$(go env GOARCH)}" go build -trimpath -tags embed_webui \
    -ldflags "-s -w -X github.com/feranydev/homeloom/backend/internal/buildinfo.Version=${VERSION} -X github.com/feranydev/homeloom/backend/internal/buildinfo.Commit=${COMMIT} -X github.com/feranydev/homeloom/backend/internal/buildinfo.BuildTime=${BUILD_TIME}" \
    -o /out/homeloom ./cmd/homeloom
RUN test -n "$VERSION" \
    && CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="${TARGETARCH:-$(go env GOARCH)}" go build -trimpath \
    -ldflags "-s -w -X github.com/feranydev/homeloom/backend/internal/buildinfo.Version=${VERSION} -X github.com/feranydev/homeloom/backend/internal/buildinfo.Commit=${COMMIT} -X github.com/feranydev/homeloom/backend/internal/buildinfo.BuildTime=${BUILD_TIME}" \
    -o /out/homeloom-mcp-agent ./cmd/homeloom-mcp-agent

FROM go-deps AS camera-kernel
WORKDIR /src/camera-kernel
COPY camera-kernel/go.mod camera-kernel/go.sum ./
RUN go mod download
COPY camera-kernel/ ./
ARG TARGETOS=linux
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="${TARGETARCH:-$(go env GOARCH)}" go build -trimpath -ldflags "-s -w" \
    -o /out/homeloom-camera-kernel .

FROM node-deps AS matter-runtime
WORKDIR /src/matter-runtime
COPY matter-runtime/package.json matter-runtime/package-lock.json ./
RUN npm ci --offline
COPY matter-runtime/ ./
RUN npm run build && npm prune --omit=dev

FROM node:24-alpine
# The target OpenWrt host reaches this mirror substantially faster than the
# default CDN. This affects only this image-build stage, not host APK settings.
RUN sed -i 's|https://dl-cdn.alpinelinux.org|https://mirrors.ustc.edu.cn|g' /etc/apk/repositories \
	&& apk add --no-cache ca-certificates ffmpeg intel-media-driver iputils-ping libva-utils tzdata \
    && addgroup -S homeloom \
    && adduser -S -G homeloom -h /app homeloom \
    && mkdir -p /data /app/matter-runtime \
    && chown -R homeloom:homeloom /data /app \
    && ln -s /data /app/data \
    && ln -s /usr/bin/ffmpeg /usr/local/bin/ffmpeg
WORKDIR /app
COPY --from=build /out/homeloom /usr/local/bin/homeloom
COPY --from=build /out/homeloom-mcp-agent /usr/local/bin/homeloom-mcp-agent
COPY --from=camera-kernel /out/homeloom-camera-kernel /usr/local/bin/homeloom-camera-kernel
COPY --from=matter-runtime /src/matter-runtime/dist /app/matter-runtime/dist
COPY --from=matter-runtime /src/matter-runtime/node_modules /app/matter-runtime/node_modules
ENV HOMELOOM_HTTP_ADDRESS=0.0.0.0:8090 \
    HOMELOOM_DATABASE_URL=postgres://homeloom:homeloom-dev@127.0.0.1:5432/homeloom?sslmode=disable \
    HOMELOOM_MASTER_KEY=/data/homeloom.key \
    HOMELOOM_MEDIA_ENABLED=true \
    HOMELOOM_CAMERA_KERNEL_BIN=/usr/local/bin/homeloom-camera-kernel \
    HOMELOOM_MEDIA_RUNTIME_DIR=/data/media/publishers \
    HOMELOOM_MATTER_RUNTIME=/app/matter-runtime/dist/src/cli.js
VOLUME ["/data"]
EXPOSE 8090 51826
HEALTHCHECK --interval=15s --timeout=3s --start-period=10s --retries=3 CMD wget -q -O /dev/null http://127.0.0.1:8090/ready || exit 1
USER homeloom
ENTRYPOINT ["/usr/local/bin/homeloom"]
