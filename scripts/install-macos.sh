#!/usr/bin/env bash
# Rebuild Orchestra (moeca) from the current checkout and replace the installed
# /Applications/moeca.app with it.
#
#   ./scripts/install-macos.sh          # build from the current branch
#   ./scripts/install-macos.sh --pull   # git checkout main && git pull first
#
# Everything is built BEFORE the installed app is touched, so a failed build
# leaves the running app alone. The previous app is kept at
# /Applications/moeca.app.bak until the next run.
#
# The Developer ID identity in src-tauri/tauri.conf.json is applied by the
# bundler; keeping it stable is what lets the Keychain hold GitHub App creds
# across upgrades.
set -euo pipefail
cd "$(dirname "$0")/.."
ROOT="$(pwd)"
APP="/Applications/moeca.app"

# A failure here is easy to miss: it scrolls past under a wall of build output
# and the script just stops, which looks exactly like "nothing happened" — the
# installed app is silently left at its old version. Say so, loudly, at the end.
# Reported from an EXIT trap rather than ERR: `set -u` aborts (and plain
# `exit 1`s) never fire ERR, and those are exactly the failures that look like
# "the script just stopped".
INSTALL_STARTED=0
FINISHED=0
on_exit() {
  local code=$?
  [[ "$FINISHED" == "1" ]] && return 0
  echo "" >&2
  echo "✗ FAILED (exit $code)" >&2
  if [[ "$INSTALL_STARTED" == "0" ]]; then
    echo "  ${APP} was NOT modified — it is still the previous version." >&2
  else
    echo "  the install step was interrupted; the previous app is at ${APP}.bak" >&2
  fi
  echo "  scroll up for the actual error." >&2
  return 0
}
trap on_exit EXIT

# Match the machine's real architecture — the HARDWARE, not this process.
# Both of the obvious signals lie under Rosetta: `uname -m` reports x86_64 for a
# translated shell, and `rustc -vV` reports host x86_64-apple-darwin when rustup
# was installed from one. Trusting either silently builds an Intel app on Apple
# Silicon. hw.optional.arm64 is 1 on Apple Silicon even under translation.
if [[ "$(sysctl -n hw.optional.arm64 2>/dev/null || echo 0)" == "1" ]]; then
  TRIPLE="aarch64-apple-darwin"; GOARCH="arm64"
else
  TRIPLE="x86_64-apple-darwin";  GOARCH="amd64"
fi
if [[ "$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)" == "1" ]]; then
  echo "▸ note: this shell runs under Rosetta — building natively for $TRIPLE anyway"
fi

if [[ "${1:-}" == "--pull" ]]; then
  echo "▸ updating to origin/main…"
  git checkout main
  git pull --ff-only
fi

echo "▸ target $TRIPLE"
rustup target list --installed | grep -qx "$TRIPLE" || rustup target add "$TRIPLE"

echo "▸ building Go sidecars…"
mkdir -p src-tauri/bin
for s in gateway hostagent sandbox; do
  (cd "$s" && GOOS=darwin GOARCH="$GOARCH" go build -o "$ROOT/src-tauri/bin/orchestra-$s-$TRIPLE" .)
done

echo "▸ building container images…"
if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
  # Services. The registry proxy is not optional in practice: without it a
  # strict sandbox has no way to install dependencies at all.
  docker build -q -t orchestra/gateway:latest gateway/ >/dev/null
  docker build -q -t orchestra/rag:latest ragindex/ >/dev/null
  docker build -q -t orchestra/registry:latest registry/ >/dev/null
  echo "  gateway + rag + registry images rebuilt"
  # Sandbox runtimes. These are what the image allowlist in configs/sandbox.json
  # points at, so a stage naming "poly" or "media" fails to launch without them.
  docker build -q -t orchestra/agent:latest agent/ >/dev/null
  docker build -q -f agent/Dockerfile.poly  -t orchestra/agent-poly:latest  agent/ >/dev/null
  docker build -q -f agent/Dockerfile.media -t orchestra/agent-media:latest agent/ >/dev/null
  echo "  sandbox images rebuilt (base / poly / media)"
else
  echo "  ⚠ docker unavailable — skipping image rebuild (services and sandboxes keep their old images)"
fi

# Builds the frontend too (tauri.conf.json beforeBuildCommand = pnpm build).
# Retried: Developer ID signing fetches a secure timestamp from
# timestamp.apple.com for every binary (4 sidecars + app + dmg), and Apple
# throttles that service under repeated builds. It surfaces as
# "A timestamp was expected but was not found" and fails the whole bundle —
# transient, so back off and try again rather than leaving the app stale.
echo "▸ building the app (Rust release — takes a few minutes)…"
build_ok=0
for attempt in 1 2 3; do
  if pnpm tauri build --target "$TRIPLE"; then
    build_ok=1
    break
  fi
  if [[ $attempt -lt 3 ]]; then
    echo "  ⚠ build failed (attempt $attempt/3) — if this was Apple's timestamp service throttling, a retry usually clears it. waiting 45s…"
    sleep 45
  fi
done
if [[ "$build_ok" != "1" ]]; then
  echo "✗ build failed 3 times — $APP left untouched." >&2
  exit 1
fi

BUILT="src-tauri/target/$TRIPLE/release/bundle/macos/moeca.app"
[[ -d "$BUILT" ]] || { echo "build produced no app at $BUILT" >&2; exit 1; }

# ── only now touch the installed app ──
if pgrep -f "$APP/Contents/MacOS/orchestra" >/dev/null 2>&1; then
  echo "▸ quitting the running app…"
  osascript -e 'quit app "moeca"' || true
  for _ in $(seq 1 30); do
    pgrep -f "$APP/Contents/MacOS/orchestra" >/dev/null 2>&1 || break
    sleep 1
  done
fi

# Brace the variable: macOS ships bash 3.2, whose identifier parser is not
# UTF-8 aware, so "$APP…" is read as the variable name APP\xe2\x80\xa6 — unbound,
# which kills the script under `set -u`. Any $VAR touching a multibyte char
# needs ${VAR}.
echo "▸ installing to ${APP}…"
INSTALL_STARTED=1
# rm the old backup first: `mv` into an existing directory would nest it.
rm -rf "$APP.bak"
if [[ -d "$APP" ]]; then
  mv "$APP" "$APP.bak"
fi
ditto "$BUILT" "$APP"
codesign --verify --strict "$APP"
echo "  signature verified"

echo "▸ launching…"
open -a "$APP"
FINISHED=1
echo "✓ done — previous app kept at ${APP}.bak"
