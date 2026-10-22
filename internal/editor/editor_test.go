package editor

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func testManager(t *testing.T) (*Manager, *bytes.Buffer) {
	t.Helper()
	home := t.TempDir()
	out := new(bytes.Buffer)
	manager := &Manager{
		in:       strings.NewReader(""),
		out:      out,
		home:     home,
		stateDir: filepath.Join(home, "state"),
		instance: "devpack-for-go",
		lookupPath: func(name string) (string, error) {
			if name == "code" {
				return "/usr/bin/code", nil
			}
			return "", errors.New("not found")
		},
		runCommand: func(name string, args ...string) ([]byte, error) {
			return []byte("golang.go\n"), nil
		},
	}
	return manager, out
}

func TestVSCodeConfigureIsIdempotentAndReversible(t *testing.T) {
	manager, _ := testManager(t)
	path := filepath.Join(manager.home, "settings.json")
	original := []byte("{\n  // User setting\n  \"editor.fontSize\": 14,\n}\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	opts := Options{Editor: "vscode", Config: path, Lint: true, Gofumpt: true, Yes: true}
	if err := manager.Configure(opts); err != nil {
		t.Fatal(err)
	}
	configured, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(configured, []byte("devpack-for-go.gopls")) || !bytes.Contains(configured, []byte("User setting")) {
		t.Fatalf("unexpected configured file:\n%s", configured)
	}
	if !bytes.Contains(configured, []byte(`"golangci-lint-v2"`)) || !bytes.Contains(configured, []byte(`/snap/bin/devpack-for-go.golangci-lint`)) {
		t.Fatalf("bundled linter path is not configured:\n%s", configured)
	}
	if err := manager.Configure(opts); err != nil {
		t.Fatal(err)
	}
	again, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(configured, again) {
		t.Fatal("second configure changed the file")
	}
	if err := manager.Unconfigure("vscode", false); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored, original) {
		t.Fatalf("original file was not restored\nwant:\n%s\ngot:\n%s", original, restored)
	}
}

func TestVimConfigureAndUnconfigure(t *testing.T) {
	manager, _ := testManager(t)
	path := filepath.Join(manager.home, ".vimrc")
	original := []byte("set number\n")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	opts := Options{Editor: "vim", Client: "vim-lsp", Config: path, Yes: true}
	if err := manager.Configure(opts); err != nil {
		t.Fatal(err)
	}
	configured, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(configured, []byte("BEGIN devpack-for-go")) || !bytes.Contains(configured, []byte("devpack-for-go.gopls")) {
		t.Fatalf("unexpected Vim configuration:\n%s", configured)
	}
	if err := manager.Unconfigure("vim", false); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored, original) {
		t.Fatalf("original Vim file was not restored: %q", restored)
	}
}

func TestUnconfigurePreservesUnrelatedTextChanges(t *testing.T) {
	manager, _ := testManager(t)
	path := filepath.Join(manager.home, ".emacs.d", "init.el")
	opts := Options{Editor: "emacs", Client: "eglot", Config: path, Yes: true}
	if err := manager.Configure(opts); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("\n(setq user-full-name \"Gopher\")\n"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Unconfigure("emacs", false); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(content, []byte("BEGIN devpack-for-go")) {
		t.Fatalf("managed block remains:\n%s", content)
	}
	if !bytes.Contains(content, []byte("user-full-name")) {
		t.Fatalf("unrelated change was removed:\n%s", content)
	}
}

func TestPromptsReportActionableErrorAtEOF(t *testing.T) {
	// /dev/null is a character device, so it passes the interactivity check and
	// then ends immediately. The prompts must explain how to proceed instead of
	// failing with a bare EOF.
	devNull, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = devNull.Close() }()

	manager := &Manager{
		in:         devNull,
		out:        new(bytes.Buffer),
		home:       t.TempDir(),
		stateDir:   filepath.Join(t.TempDir(), "state"),
		instance:   "devpack-for-go",
		lookupPath: func(string) (string, error) { return "", errors.New("not found") },
	}
	if !manager.Interactive() {
		t.Fatal("a character device should count as interactive input")
	}
	if _, err := manager.Choose("Editor", []string{"vim", "nvim"}); err == nil || !strings.Contains(err.Error(), "non-interactive mode") {
		t.Fatalf("Choose at EOF returned %v, want an actionable error", err)
	}
	if _, err := manager.ConfirmDefault("Apply these changes?", true); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("ConfirmDefault at EOF returned %v, want an actionable error", err)
	}
}

func TestSummaryTracksConfigurationState(t *testing.T) {
	manager, _ := testManager(t)
	path := filepath.Join(manager.home, ".vimrc")
	if err := manager.Configure(Options{Editor: "vim", Client: "vim-lsp", Config: path, Yes: true}); err != nil {
		t.Fatal(err)
	}

	summary, err := manager.Summary()
	if err != nil {
		t.Fatal(err)
	}
	if summary["vim"] != "configured" {
		t.Errorf("vim = %q, want configured", summary["vim"])
	}
	for _, name := range []string{"vscode", "nvim", "emacs"} {
		if summary[name] != "unconfigured" {
			t.Errorf("%s = %q, want unconfigured", name, summary[name])
		}
	}

	if err := os.WriteFile(path, []byte("set number\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	summary, err = manager.Summary()
	if err != nil {
		t.Fatal(err)
	}
	if summary["vim"] != "drifted" {
		t.Errorf("vim = %q, want drifted", summary["vim"])
	}
}

func TestSupportedEditorsAndClients(t *testing.T) {
	if got := Supported(); len(got) != 4 || got[0] != "vscode" {
		t.Fatalf("Supported() = %v", got)
	}
	if got := ClientsFor("vscode"); got != nil {
		t.Errorf("ClientsFor(vscode) = %v, want nil", got)
	}
	for _, editorName := range []string{"vim", "nvim"} {
		clients := ClientsFor(editorName)
		for _, want := range []string{"vim-go", "coc-go", "coc", "vim-lsp"} {
			if !slices.Contains(clients, want) {
				t.Errorf("ClientsFor(%s) = %v, missing %q", editorName, clients, want)
			}
		}
	}
	for _, want := range []string{"eglot", "lsp-mode"} {
		if !slices.Contains(ClientsFor("emacs"), want) {
			t.Errorf("ClientsFor(emacs) is missing %q", want)
		}
	}
}

func TestUnconfigureRejectsUnknownEditor(t *testing.T) {
	manager, _ := testManager(t)
	if err := manager.Unconfigure("sublime", false); err == nil {
		t.Fatal("expected unsupported editor error")
	}
}

func TestDryRunHasNoFilesystemEffects(t *testing.T) {
	manager, _ := testManager(t)
	path := filepath.Join(manager.home, ".vimrc")
	opts := Options{Editor: "vim", Client: "vim-lsp", Config: path, DryRun: true}
	if err := manager.Configure(opts); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry run created config: %v", err)
	}
	if _, err := os.Stat(manager.statePath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry run created state: %v", err)
	}
}

func TestVimAndNeovimUseIndependentWrappers(t *testing.T) {
	manager, _ := testManager(t)
	vimConfig := filepath.Join(manager.home, ".vimrc")
	nvimConfig := filepath.Join(manager.home, ".config", "nvim", "init.vim")
	if err := manager.Configure(Options{Editor: "vim", Client: "vim-go", Config: vimConfig, Lint: true, Yes: true}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Configure(Options{Editor: "nvim", Client: "vim-go", Config: nvimConfig, Lint: true, Yes: true}); err != nil {
		t.Fatal(err)
	}

	vimWrapper := filepath.Join(manager.stateDir, "bin", "vim", "gopls")
	nvimWrapper := filepath.Join(manager.stateDir, "bin", "nvim", "gopls")
	if _, err := os.Stat(vimWrapper); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(nvimWrapper); err != nil {
		t.Fatal(err)
	}
	if err := manager.Unconfigure("vim", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(vimWrapper); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Vim wrapper remains after unconfigure: %v", err)
	}
	if _, err := os.Stat(nvimWrapper); err != nil {
		t.Fatalf("Neovim wrapper was removed with Vim: %v", err)
	}
	current, err := manager.loadState()
	if err != nil {
		t.Fatal(err)
	}
	if healthy, reason := manager.editorHealthy(current.Editors["nvim"]); !healthy {
		t.Fatalf("Neovim configuration is unhealthy: %s", reason)
	}
}

func TestVimGoLintOptOutDoesNotCreateLinterWrapper(t *testing.T) {
	manager, _ := testManager(t)
	path := filepath.Join(manager.home, ".vimrc")
	if err := manager.Configure(Options{Editor: "vim", Client: "vim-go", Config: path, Lint: false, Yes: true}); err != nil {
		t.Fatal(err)
	}
	linter := filepath.Join(manager.stateDir, "bin", "vim", "golangci-lint")
	if _, err := os.Stat(linter); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lint opt-out created a linter wrapper: %v", err)
	}
}

func TestNeovimCocUsesNeovimConfigDirectory(t *testing.T) {
	manager, _ := testManager(t)
	if err := manager.Configure(Options{Editor: "nvim", Client: "coc-go", Yes: true}); err != nil {
		t.Fatal(err)
	}
	nvimConfig := filepath.Join(manager.home, ".config", "nvim", "coc-settings.json")
	if _, err := os.Stat(nvimConfig); err != nil {
		t.Fatalf("Neovim coc configuration was not created: %v", err)
	}
	vimConfig := filepath.Join(manager.home, ".vim", "coc-settings.json")
	if _, err := os.Stat(vimConfig); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Neovim configuration was written under .vim: %v", err)
	}
}

func TestOriginalMatchesRejectsChangeAfterPreview(t *testing.T) {
	manager, _ := testManager(t)
	path := filepath.Join(manager.home, ".vimrc")
	if err := os.WriteFile(path, []byte("set number\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	change, err := manager.blockChange(path, "vim", "set hidden\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("set relativenumber\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := originalMatches(change); err == nil {
		t.Fatal("expected a stale preview to be rejected")
	}
}

func TestPartialRollbackReturnsOnlyFilesNeedingRecovery(t *testing.T) {
	manager, _ := testManager(t)
	first, err := manager.blockChange(filepath.Join(manager.home, "first.vim"), "vim", "set hidden\n")
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.blockChange(filepath.Join(manager.home, "second.vim"), "vim", "set number\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []fileChange{first, second} {
		if err := atomicWrite(change.state.Path, change.output, os.FileMode(change.state.Mode)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(second.state.Path, []byte("changed after apply\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	failed, err := restoreAppliedChanges([]fileChange{first, second})
	if err == nil {
		t.Fatal("expected partial rollback error")
	}
	if len(failed) != 1 || failed[0].state.Path != second.state.Path {
		t.Fatalf("unexpected failed rollback files: %+v", failed)
	}
	if _, err := os.Stat(first.state.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("successfully rolled back file remains: %v", err)
	}
}

func TestRollbackRetainsExtensionOwnership(t *testing.T) {
	manager, _ := testManager(t)
	planned := editorState{
		Editor:             "vscode",
		Status:             "active",
		VSCodeCommand:      "/usr/bin/code",
		ExtensionInstalled: true,
	}
	current := state{Version: stateVersion, Editors: map[string]editorState{"vscode": planned}}
	rollbackCause := errors.New("extension uninstall failed")
	if err := manager.rollbackConfigure(current, "vscode", planned, nil, true, rollbackCause); !errors.Is(err, rollbackCause) {
		t.Fatalf("rollback error = %v, want %v", err, rollbackCause)
	}
	saved, err := manager.loadState()
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := saved.Editors["vscode"]
	if !ok || !entry.ExtensionInstalled || entry.Status != "pending" {
		t.Fatalf("extension ownership was not retained: %+v", entry)
	}
}
