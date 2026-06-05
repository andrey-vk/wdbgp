FROM golang:1.26.4-alpine3.23 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/wdbgp ./cmd/wdbgp

FROM alpine:3.23 AS certs

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
