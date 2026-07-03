# syntax=docker/dockerfile:1

# --- Build the static daemon binary ---
FROM golang:1.26-alpine AS build
WORKDIR /src
# No external dependencies (no go.sum), so this is just the source.
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/yuno-wings .

# --- Minimal runtime ---
# The daemon controls containers via the Docker CLI, so the image ships the
# docker client; mount the host's Docker socket at runtime.
FROM alpine:3.20
RUN apk add --no-cache docker-cli ca-certificates tzdata

COPY --from=build /out/yuno-wings /usr/local/bin/yuno-wings

# Config (config.json) and server data live under /etc/yuno.
WORKDIR /etc/yuno
VOLUME ["/etc/yuno"]
EXPOSE 8080

ENTRYPOINT ["yuno-wings"]
