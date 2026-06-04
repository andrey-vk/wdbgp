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

ENTRYPOINT ["/app/entrypoint.sh"]
