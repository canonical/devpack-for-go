# Devpack for Go

Devpack for Go is a companion snap for the official [Go snap](https://snapcraft.io/go). It packages commonly used Go development tools while using the Go toolchain from `/snap/go/current`.

The snap includes:

- [Delve](https://github.com/go-delve/delve)
- [gopls](https://pkg.go.dev/golang.org/x/tools/gopls)
- [golangci-lint](https://github.com/golangci/golangci-lint)
- [govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck)
- [gotests](https://github.com/cweill/gotests)
- [gofumpt](https://github.com/mvdan/gofumpt)

## Install

Install Go and Devpack for Go from the Snap Store:

```shell
sudo snap install go --classic
sudo snap install devpack-for-go --classic
```

Run the setup guide for commands that configure aliases and VS Code:

```shell
devpack-for-go.setup-guide
```

Without aliases, tools are available under their snap-qualified names, such as `devpack-for-go.gopls` and `devpack-for-go.golangci-lint`.

## Build

Install Snapcraft and build the snap from the repository root:

```shell
sudo snap install snapcraft --classic
snapcraft
```

Install the locally built snap with:

```shell
sudo snap install ./devpack-for-go_*.snap --dangerous --classic
```

The published package is available at <https://snapcraft.io/devpack-for-go>.
