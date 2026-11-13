# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS TARGETARCH VERSION=dev SENTRY_DSN=
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# the web dashboard (WebAssembly) that the server embeds; the same for every platform
RUN sh scripts/build-web.sh
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w -X github.com/herosizy/healthsync-server/server.version=$VERSION -X github.com/herosizy/healthsync-server/internal/telemetry.dsn=$SENTRY_DSN" -o /out/healthsync-server .

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 1000 healthsync && mkdir /data && chown healthsync /data
COPY --from=build /out/healthsync-server /usr/local/bin/healthsync-server
USER healthsync
ENV HEALTHSYNC_DATA=/data HOME=/data
VOLUME /data
EXPOSE 8787
HEALTHCHECK --interval=1m --timeout=5s CMD wget -qO- http://127.0.0.1:${HEALTHSYNC_PORT:-8787}/health >/dev/null || exit 1
ENTRYPOINT ["healthsync-server"]
CMD ["serve"]
