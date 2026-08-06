# Orchestra agent runtime — the "base" sandbox image, and the entrypoint that
# runs an agentic coding task against the mounted git worktree at /work.
#
# This is the smallest of the three sandbox images (see also Dockerfile.poly and
# Dockerfile.media): distroless, no shell, no toolchain — just the agent. An LLM
# stage that only reads and edits files needs nothing more, and the attack
# surface of an image is the surface an agent gets, so the default is the
# smallest thing that works.
#
# Build:  docker build -t orchestra/agent:latest .
# The sandbox launches it with /work mounted read-write and the gateway env
# injected server-side (ANTHROPIC_BASE_URL=http://orchestra-gateway:8787/anthropic).

# --- build stage -------------------------------------------------------------
FROM golang:1.25 AS build
WORKDIR /src

# Stdlib-only module: copying go.mod is enough (no external deps to download).
COPY go.mod ./
COPY . .

# Static, CGO-free binary so it runs on the distroless static base.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /agent .

# --- final stage -------------------------------------------------------------
FROM gcr.io/distroless/static
COPY --from=build /agent /agent
WORKDIR /work
# CMD rather than ENTRYPOINT so a stage's `cmd` REPLACES the default instead of
# being appended to it as arguments. All three sandbox images use CMD, so
# Stage.Cmd means the same thing whichever image a stage names.
CMD ["/agent"]
