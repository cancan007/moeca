#!/usr/bin/env bash
# Launch Orchestra as a native Tauri app with its Go sidecars.
#
#   ./scripts/dev.sh
#
# Builds the three Go services into src-tauri/bin, points the Rust sidecar
# manager at them + the configs/ directory, and runs `tauri dev` (which starts
# the Vite dev server via beforeDevCommand). Set ANTHROPIC_API_KEY / GITHUB_TOKEN
# in your environment first — the gateway injects them; agents never see them.
set -euo pipefail
cd "$(dirname "$0")/.."
ROOT="$(pwd)"

echo "▸ building Go sidecars…"
mkdir -p src-tauri/bin
# gateway binary is kept for ORCHESTRA_GATEWAY_MODE=host and tests; the default
# run uses the containerized gateway built below.
(cd gateway   && go build -o "$ROOT/src-tauri/bin/orchestra-gateway"   .)
(cd hostagent && go build -o "$ROOT/src-tauri/bin/orchestra-hostagent" .)
(cd sandbox   && go build -o "$ROOT/src-tauri/bin/orchestra-sandbox"   .)

echo "▸ building service container images…"
if command -v docker >/dev/null 2>&1; then
  docker build -t orchestra/gateway:latest gateway/
  docker build -t orchestra/rag:latest ragindex/
  # the package-registry proxy: how a strict sandbox installs dependencies
  # without ever getting a route to the internet
  docker build -t orchestra/registry:latest registry/
  # knowledge folders are registered in Settings → RAG (mounted read-only);
  # ORCHESTRA_KNOWLEDGE_DIR still seeds the list for installs that predate that

  echo "▸ building sandbox images (base / poly / media)…"
  # base is the default for agent stages; poly adds the toolchains a build or
  # test stage needs; media carries ffmpeg/ImageMagick and runs with no network.
  docker build -t orchestra/agent:latest agent/
  docker build -f agent/Dockerfile.poly  -t orchestra/agent-poly:latest  agent/
  docker build -f agent/Dockerfile.media -t orchestra/agent-media:latest agent/
else
  echo "  docker not found — gateway will fall back to host mode if you set ORCHESTRA_GATEWAY_MODE=host"
fi

export ORCHESTRA_BIN_DIR="$ROOT/src-tauri/bin"
export ORCHESTRA_CONFIG_DIR="$ROOT/configs"

echo "▸ launching Orchestra (gateway :8787, hostagent :8788, sandbox :8789, rag :8790, registry :8791)…"
pnpm tauri dev
