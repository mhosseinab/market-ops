#!/bin/bash
# market-ops /work-issue loop - cloud session bootstrap.
# Fail-soft: nothing here may block session start. Tools still missing
# afterwards are handled by the command's ENVIRONMENT FALLBACKS section.
# Invoked from the cloud environment's setup-script stub:
#   #!/bin/bash
#   bash tools/cloud-setup.sh || true
set -x

BIN="$HOME/.local/bin"
mkdir -p "$BIN"
SUDO=""
if sudo -n true 2>/dev/null; then
  SUDO="sudo"
  BIN=/usr/local/bin
fi

# PATH must reach every later shell, not just interactive ones -
# a prior run's agents had to prefix PATH by hand in each command.
LINE='export PATH="$HOME/.local/bin:$HOME/go/bin:$PATH" # work-issue'
for f in "$HOME/.bashrc" "$HOME/.profile"; do
  grep -q "work-issue" "$f" 2>/dev/null || echo "$LINE" >> "$f"
done
export PATH="$BIN:$HOME/.local/bin:$HOME/go/bin:$PATH"

have() { command -v "$1" >/dev/null 2>&1; }

# go-task: `task ci:local` is the pre-merge gate.
if ! have task; then
  curl -fsSL https://taskfile.dev/install.sh -o /tmp/task-install.sh
  sh /tmp/task-install.sh -d -b "$BIN" || true
fi

# No gh CLI on purpose: without an auth token it is useless, and cloud
# sessions route all GitHub operations through the GitHub MCP tools
# (the command's ENVIRONMENT FALLBACKS section covers this).

# Go helpers (Go 1.26 itself auto-downloads via GOTOOLCHAIN=auto).
if ! have golangci-lint; then
  GCI=https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh
  curl -fsSL "$GCI" -o /tmp/golangci.sh
  sh /tmp/golangci.sh -b "$BIN" || true
fi
have sqlc || go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest || true
have goose || go install github.com/pressly/goose/v3/cmd/goose@latest || true
have river || go install github.com/riverqueue/river/cmd/river@latest || true
have actionlint || \
  go install github.com/rhysd/actionlint/cmd/actionlint@latest || true

# Python plane: uv only (CLAUDE.md); semgrep backs the money static guard.
if ! have uv; then
  curl -LsSf https://astral.sh/uv/install.sh -o /tmp/uv-install.sh
  sh /tmp/uv-install.sh || true
fi
have semgrep || uv tool install semgrep || true

# TS plane: pnpm@11.13.1 via corepack (package.json packageManager pin).
if have corepack; then
  corepack enable || true
  corepack prepare pnpm@11.13.1 --activate || true
fi
have pnpm || npm install -g pnpm@11.13.1 || true

# Repo bootstrap: canonical `task setup`, raw commands as fallback.
ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
cd "${ROOT:-.}" || true
if ! task setup; then
  pnpm install --frozen-lockfile || true
  uv sync --group dev || true
fi
(cd services/core && go mod download) || true

# Print remaining gaps without failing the session (docker is expected
# to be absent in cloud sessions; db/integration gates defer to CI).
task doctor || true
