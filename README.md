# Devpack for Go

Devpack for Go packages current, tested Go development tools in one maintained
snap. The tools use whichever `go` command is selected by your `PATH`, so the
snap works with the official [Go snap](https://snapcraft.io/go), distribution
packages, tarball installations, and Go version managers.

The snap includes:

- [Delve](https://github.com/go-delve/delve), the Go debugger
- [gopls](https://pkg.go.dev/golang.org/x/tools/gopls), the Go language server
- [golangci-lint](https://github.com/golangci/golangci-lint), a fast linter runner
- [govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck), the Go vulnerability scanner
- [gotests](https://github.com/cweill/gotests), a test generator
- [gofumpt](https://github.com/mvdan/gofumpt), a stricter Go formatter

## Install

Install the snap from the Snap Store:

```shell
sudo snap install devpack-for-go --classic
```

You also need a Go toolchain. The official Go snap is the simplest option:

```shell
sudo snap install go --classic
```

Then configure an editor with the interactive setup wizard:

```shell
devpack-for-go setup
```

Run diagnostics to confirm which Go installation, tools, and editor
configuration were detected:

```shell
devpack-for-go doctor
devpack-for-go versions
```

Every tool is available under its snap-qualified name:

```shell
devpack-for-go.dlv
devpack-for-go.gopls
devpack-for-go.golangci-lint
devpack-for-go.govulncheck
devpack-for-go.gotests
devpack-for-go.gofumpt
```

You can create shorter aliases yourself:

```shell
sudo snap alias devpack-for-go.dlv dlv
sudo snap alias devpack-for-go.gopls gopls
sudo snap alias devpack-for-go.golangci-lint golangci-lint
sudo snap alias devpack-for-go.govulncheck govulncheck
sudo snap alias devpack-for-go.gotests gotests
sudo snap alias devpack-for-go.gofumpt gofumpt
```

Automatic aliases for `dlv` and `gotests` have been requested from the Snap
Store and will be declared in a later release once they are granted. `gopls`,
`golangci-lint`, `govulncheck`, and `gofumpt` cannot be aliased automatically:
the store already has snaps under those names, and snapd grants each snap use of
its own name. A manual alias for those works only when the corresponding snap is
not installed.

## Editor Integration

`devpack-for-go setup` asks which editor and language client to configure,
whether to use the bundled golangci-lint and gofumpt, previews every file it
will change, and asks for confirmation before writing anything:

```shell
devpack-for-go setup
```

Every question can be answered with a flag instead, which keeps the command
usable in scripts and CI. `editor configure` accepts the same options:

```shell
devpack-for-go setup --editor vscode --yes
devpack-for-go setup --editor vim --client vim-go --yes
devpack-for-go setup --editor nvim --client coc-go --yes
devpack-for-go setup --editor emacs --client eglot --yes
```

Without a terminal, `setup` never prompts: it applies the supplied flags and
reports which choice is missing.

Supported Vim and Neovim clients are `vim-go`, `coc-go`, `coc`, and `vim-lsp`.
Supported Emacs clients are `eglot` and `lsp-mode`. Add `--gofumpt` to enable
gofumpt-style formatting, `--lint=false` to skip the bundled linter, or
`--dry-run` to preview without writing files. Use `--config PATH` to override
the detected editor configuration file.

The helper supports VS Code, Vim, Neovim, and Emacs. It installs the official
Go extension when configuring VS Code. Vim, Neovim, and Emacs plugins must
already be installed through the editor's package manager; the helper prints
this prerequisite before applying their configuration.

The helper preserves comments in JSONC files and records only the settings and
managed blocks it owns. Inspect or remove its changes with:

```shell
devpack-for-go editor status
devpack-for-go editor status --json
devpack-for-go editor unconfigure --editor vscode
devpack-for-go editor unconfigure
```

The compatibility command `devpack-for-go.setup-guide` runs the same setup
wizard.

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

Run the unit tests with the Go version declared in `go.mod`:

```shell
go test ./...
go vet ./...
```

Pinned tool releases live in `internal/toolversions/versions.env`. The weekly
update workflow checks the Go module proxy for newer stable releases, updates
the manifest, and opens a pull request. CI builds the snap and smoke-tests it
against the two supported Go release lines.

The published package is available at <https://snapcraft.io/devpack-for-go>.

## License

The helper, packaging, and tests in this repository are released under the
GNU General Public License version 3. See [LICENSE](LICENSE). Each bundled tool
keeps its own upstream license.
