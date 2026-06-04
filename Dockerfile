FROM python:3.14-alpine3.23

RUN apk add --no-cache bird

WORKDIR /app
COPY wdbgp /app/wdbgp
COPY entrypoint.sh /app/entrypoint.sh

RUN chmod +x /app/entrypoint.sh \
    && mkdir -p /data /run/bird

ENV WDBGP_DB=/data/wdbgp.sqlite3 \
    WDBGP_BIRD_CONFIG=/etc/bird.conf \
    WDBGP_BIRD_SOCKET=/run/bird/bird.ctl \
    WDBGP_HOST=0.0.0.0 \
    WDBGP_PORT=8080

VOLUME ["/data"]
EXPOSE 8080 179

HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD python -c "import os, urllib.request; urllib.request.urlopen(f'http://127.0.0.1:{os.getenv(\"WDBGP_PORT\", \"8080\")}/healthz', timeout=3).read()" || exit 1

ENTRYPOINT ["/app/entrypoint.sh"]
