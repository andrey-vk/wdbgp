FROM --platform=$BUILDPLATFORM node:24-alpine AS frontend

WORKDIR /src/webgui
COPY webgui/package.json webgui/package-lock.json ./
RUN npm ci
COPY webgui/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26.4-alpine3.23 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY spaembed.go ./
COPY --from=frontend /src/webgui/dist ./webgui/dist
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT
RUN set -eux; \
    if [ "$TARGETARCH" = "arm" ]; then export GOARM="${TARGETVARIANT#v}"; fi; \
    CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" \
    go build -trimpath -ldflags="-s -w" -o /out/wdbgp ./cmd/wdbgp

FROM --platform=$BUILDPLATFORM alpine:3.23 AS certs

RUN apk add --no-cache ca-certificates \
    && mkdir -p /data

FROM scratch

COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=certs /data /data
COPY --from=build /out/wdbgp /usr/local/bin/wdbgp

ENV WDBGP_DB=/data/wdbgp.sqlite3 \
    WDBGP_HOST=0.0.0.0 \
    WDBGP_PORT=8080 \
    WDBGP_BGP_PORT=179

VOLUME ["/data"]
EXPOSE 8080 179

HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD ["wdbgp", "healthcheck"]

ENTRYPOINT ["wdbgp"]
CMD ["serve"]
