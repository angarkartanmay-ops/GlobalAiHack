# syntax=docker/dockerfile:1
#
# Two images from one file:
#
#   docker build -t heimdall .                         # all-in-one: stock Bifrost + Heimdall
#   docker build -t heimdall --target heimdall .       # Heimdall alone, for docker compose
#
# The gateway in the all-in-one image is the official maximhq/bifrost image,
# unmodified. Heimdall is one static binary added next to it.

ARG BIFROST_IMAGE=maximhq/bifrost:latest

FROM node:22-alpine AS ui
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui /src/web/dist ./web/dist
ARG VERSION=1.0.0
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/heimdall ./cmd/heimdall

# Heimdall alone.
FROM alpine:3.20 AS heimdall
RUN apk add --no-cache ca-certificates && adduser -D -u 1000 heimdall \
  && mkdir -p /data /gateway && chown -R 1000:0 /data /gateway && chmod -R g=rwX /data /gateway
COPY --from=build /out/heimdall /usr/local/bin/heimdall
USER 1000
ENV PORT=8787 HEIMDALL_DATA_DIR=/data
VOLUME ["/data"]
EXPOSE 8787
HEALTHCHECK --interval=15s --timeout=5s --start-period=5s --retries=5 \
  CMD wget -q -O /dev/null "http://127.0.0.1:${PORT}/healthz" || exit 1
ENTRYPOINT ["heimdall"]
CMD ["serve"]

# All-in-one: the official gateway image plus Heimdall.
FROM ${BIFROST_IMAGE} AS all-in-one
USER root
COPY --from=build /out/heimdall /usr/local/bin/heimdall
COPY deploy/entrypoint.sh /app/heimdall-entrypoint.sh
RUN chmod +x /app/heimdall-entrypoint.sh \
  && mkdir -p /app/data \
  && chown -R 1000:0 /app/data \
  && chmod -R g=rwX /app/data
USER 1000:0
ENV PORT=7860 GATEWAY_PORT=8080 HEIMDALL_HOME=/app/data
EXPOSE 7860
HEALTHCHECK --interval=30s --timeout=10s --start-period=20s --retries=3 \
  CMD wget -q -O /dev/null "http://127.0.0.1:${PORT}/healthz" || exit 1
ENTRYPOINT ["/app/heimdall-entrypoint.sh"]
CMD []
