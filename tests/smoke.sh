#!/bin/sh

set -eu

devpack-for-go versions
devpack-for-go.dlv version
devpack-for-go.gopls version
devpack-for-go.golangci-lint version
devpack-for-go.govulncheck -version
devpack-for-go.gotests -h >/dev/null
devpack-for-go.gofumpt -version

workdir="$(mktemp -d)"
setup_home="$(mktemp -d)"
setup_state="$(mktemp -d)"
trap 'rm -rf "$workdir" "$setup_home" "$setup_state"' EXIT

cat >"$workdir/go.mod" <<'EOF'
module example.com/devpack-smoke

go 1.26
EOF

cat >"$workdir/main.go" <<'EOF'
package main

import "fmt"

func main() { fmt.Println("devpack") }
EOF

devpack-for-go.gofumpt -w "$workdir/main.go"
(
    cd "$workdir"
    devpack-for-go.golangci-lint run
    devpack-for-go.govulncheck ./...
    go test ./...
)

# Configure, inspect, and remove editor integration in a throwaway home. Every
# setup question is answered with a flag so this never waits on a terminal.
export SNAP_REAL_HOME="$setup_home"
export SNAP_USER_COMMON="$setup_state"

devpack-for-go setup \
    --editor vim --client vim-lsp --lint=false --gofumpt=false \
    --config "$setup_home/.vimrc" --yes
devpack-for-go editor status --editor vim | grep -Eq '^vim:[[:space:]]+configured$'
devpack-for-go doctor | grep -q 'vim (configured)'
devpack-for-go editor unconfigure --editor vim
test ! -e "$setup_home/.vimrc"
devpack-for-go editor status --editor vim | grep -Eq '^vim:[[:space:]]+unconfigured$'
