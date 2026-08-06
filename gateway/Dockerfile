# Orchestra security gateway — the single egress proxy, run as a container that
# sits on the sandbox egress network. It is the ONLY component that holds API
# keys and reaches the internet; sandboxes reach it by DNS name and can go
# nowhere else.
#
# Build:  docker build -t orchestra/gateway:latest .
# Run is orchestrated by the Tauri shell (see src-tauri/src/sidecars.rs):
# attached to orchestra-egress (internal) + orchestra-upstream (bridge), with
# ANTHROPIC_API_KEY / GITHUB_TOKEN injected and 127.0.0.1:8787 published to host.

# --- build stage -------------------------------------------------------------
FROM golang:1.25 AS build
WORKDIR /src

# Stdlib-only module: copying go.mod is enough (no external deps to download).
COPY go.mod ./
COPY . .

# Static, CGO-free binary so it runs on the distroless static base.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /gateway .

# --- final stage -------------------------------------------------------------
# distroless/static ships CA certificates, so TLS to api.anthropic.com works.
FROM gcr.io/distroless/static
COPY --from=build /gateway /gateway
ENTRYPOINT ["/gateway"]
