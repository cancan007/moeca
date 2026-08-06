# Orchestra package-registry proxy — the dependency-fetch counterpart to the
# gateway. Runs as a container dual-homed on orchestra-egress (where sandboxes
# reach it) and orchestra-upstream (its own NAT'd route out), so `npm install` /
# `pip install` / `go mod download` work inside a sandbox that has no route to
# the internet.
#
# Build:  docker build -t orchestra/registry:latest .
# Run is orchestrated by the Tauri shell (see src-tauri/src/sidecars.rs).

# --- build stage -------------------------------------------------------------
FROM golang:1.25 AS build
WORKDIR /src

# Stdlib-only module: copying go.mod is enough (no external deps to download).
COPY go.mod ./
COPY . .

# Static, CGO-free binary so it runs on the distroless static base.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /registry .

# --- final stage -------------------------------------------------------------
# distroless/static ships CA certificates, so TLS to the public registries works.
FROM gcr.io/distroless/static
COPY --from=build /registry /registry
ENTRYPOINT ["/registry"]
