package editor

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

const stateVersion = 1

// supportedEditors lists every editor the helper can configure.
var supportedEditors = []string{"vscode", "vim", "nvim", "emacs"}

var vimClients = []string{"vim-go", "coc-go", "coc", "vim-lsp"}

var emacsClients = []string{"eglot", "lsp-mode"}

// Supported returns the editors the helper can configure, in display order.
func Supported() []string {
	return slices.Clone(supportedEditors)
}

// ClientsFor returns the language clients supported for an editor, or nil when
// the editor needs no client selection.
func ClientsFor(editorName string) []string {
	switch editorName {
	case "vim", "nvim":
		return vimClients
	case "emacs":
		return emacsClients
	default:
		return nil
	}
}

type Options struct {
	Editor  string
	Client  string
	Config  string
	Lint    bool
	Gofumpt bool
	DryRun  bool
	Yes     bool
}

type Manager struct {
	in         io.Reader
	out        io.Writer
	home       string
	stateDir   string
	instance   string
	lookupPath func(string) (string, error)
	runCommand func(string, ...string) ([]byte, error)
}

type state struct {
	Version int                    `json:"version"`
	Editors map[string]editorState `json:"editors"`
}

type editorState struct {
	Editor             string      `json:"editor"`
	Client             string      `json:"client,omitempty"`
	Status             string      `json:"status"`
	Files              []fileState `json:"files"`
	VSCodeCommand      string      `json:"vscode_command,omitempty"`
	ExtensionInstalled bool        `json:"extension_installed,omitempty"`
}

type fileState struct {
	Path           string     `json:"path"`
	Kind           string     `json:"kind"`
	OriginalExists bool       `json:"original_exists"`
	OriginalBase64 string     `json:"original_base64,omitempty"`
	Mode           uint32     `json:"mode"`
	AppliedHash    string     `json:"applied_sha256"`
	ManagedBlock   string     `json:"managed_block,omitempty"`
	JSONEdits      []jsonEdit `json:"json_edits,omitempty"`
}

type fileChange struct {
	state  fileState
	output []byte
}

func NewManager(in io.Reader, out io.Writer) (*Manager, error) {
	home := os.Getenv("SNAP_REAL_HOME")
	if home == "" {
		if os.Getenv("SNAP") != "" {
			return nil, errors.New("SNAP_REAL_HOME is unavailable; refusing to edit an unknown home directory")
		}
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("find home directory: %w", err)
		}
	}

	stateDir := os.Getenv("SNAP_USER_COMMON")
	if stateDir == "" {
		stateDir = filepath.Join(home, ".local", "state", "devpack-for-go")
	}
	instance := os.Getenv("SNAP_INSTANCE_NAME")
	if instance == "" {
		instance = "devpack-for-go"
	}
	return &Manager{
		in:         in,
		out:        out,
		home:       home,
		stateDir:   filepath.Join(stateDir, "editor-config"),
		instance:   instance,
		lookupPath: exec.LookPath,
		runCommand: func(name string, args ...string) ([]byte, error) {
			return exec.Command(name, args...).CombinedOutput()
		},
	}, nil
}

func (m *Manager) Configure(opts Options) error {
	if opts.Editor == "" {
		choice, err := m.Choose("Editor", m.DetectEditors())
		if err != nil {
			return err
		}
		opts.Editor = choice
	}
	opts.Editor = strings.ToLower(opts.Editor)
	if !validEditor(opts.Editor) {
		return fmt.Errorf("unsupported editor %q", opts.Editor)
	}
	if !opts.DryRun {
		unlock, err := m.lock()
		if err != nil {
			return err
		}
		defer unlock()
	}

	current, err := m.loadState()
	if err != nil {
		return err
	}
	if existing, ok := current.Editors[opts.Editor]; ok {
		healthy, reason := m.editorHealthy(existing)
		if healthy {
			_, err := fmt.Fprintf(m.out, "%s is already configured\n", opts.Editor)
			return err
		}
		return fmt.Errorf("%s configuration has drifted: %s; unconfigure it before reconfiguring", opts.Editor, reason)
	}

	planned, changes, installExtension, err := m.plan(opts)
	if err != nil {
		return err
	}
	if len(changes) == 0 && !installExtension {
		_, err := fmt.Fprintf(m.out, "%s already uses the requested settings; nothing to change\n", opts.Editor)
		return err
	}
	if planned.Client != "" {
		if _, err := fmt.Fprintf(m.out, "Requires %s to be installed with your editor's package manager\n", planned.Client); err != nil {
			return err
		}
	}
	for _, change := range changes {
		if _, err := fmt.Fprintf(m.out, "%s %s\n", actionFor(change.state.OriginalExists), change.state.Path); err != nil {
			return err
		}
	}
	if installExtension {
		if _, err := fmt.Fprintln(m.out, "Install VS Code extension golang.go"); err != nil {
			return err
		}
	}
	if opts.DryRun {
		return nil
	}
	if !opts.Yes {
		confirmed, err := m.Confirm("Apply these changes?")
		if err != nil {
			return err
		}
		if !confirmed {
			return errors.New("configuration cancelled")
		}
	}

	planned.Status = "pending"
	current.Editors[opts.Editor] = planned
	if err := m.saveState(current); err != nil {
		return err
	}
	applied := make([]fileChange, 0, len(changes))
	for _, change := range changes {
		check := func() error { return originalMatches(change) }
		if err := atomicWriteChecked(change.state.Path, change.output, os.FileMode(change.state.Mode), check); err != nil {
			return m.rollbackConfigure(current, opts.Editor, planned, applied, false, err)
		}
		applied = append(applied, change)
	}
	if installExtension {
		output, err := m.runCommand(planned.VSCodeCommand, "--install-extension", "golang.go")
		if err != nil {
			installErr := fmt.Errorf("install VS Code Go extension: %w: %s", err, strings.TrimSpace(string(output)))
			return m.rollbackConfigure(current, opts.Editor, planned, applied, false, installErr)
		}
		planned.ExtensionInstalled = true
	}
	planned.Status = "active"
	current.Editors[opts.Editor] = planned
	if err := m.saveState(current); err != nil {
		extensionInstalled := false
		if planned.ExtensionInstalled {
			_, uninstallErr := m.runCommand(planned.VSCodeCommand, "--uninstall-extension", "golang.go")
			if uninstallErr != nil {
				err = errors.Join(err, fmt.Errorf("roll back VS Code extension: %w", uninstallErr))
				extensionInstalled = true
			}
		}
		return m.rollbackConfigure(current, opts.Editor, planned, applied, extensionInstalled, err)
	}
	_, err = fmt.Fprintf(m.out, "%s configured successfully\n", opts.Editor)
	return err
}

// Summary reports the managed configuration state of every supported editor as
// one of "configured", "drifted", or "unconfigured".
func (m *Manager) Summary() (map[string]string, error) {
	current, err := m.loadState()
	if err != nil {
		return nil, err
	}
	results := make(map[string]string, len(supportedEditors))
	for _, name := range supportedEditors {
		entry, ok := current.Editors[name]
		if !ok {
			results[name] = "unconfigured"
			continue
		}
		if healthy, _ := m.editorHealthy(entry); healthy {
			results[name] = "configured"
		} else {
			results[name] = "drifted"
		}
	}
	return results, nil
}

func (m *Manager) Status(editorName string, jsonOutput bool) error {
	if editorName != "" && !validEditor(editorName) {
		return fmt.Errorf("unsupported editor %q", editorName)
	}
	summary, err := m.Summary()
	if err != nil {
		return err
	}
	results := summary
	if editorName != "" {
		results = map[string]string{editorName: summary[editorName]}
	}
	if jsonOutput {
		encoded, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(m.out, string(encoded))
		return err
	}
	names := make([]string, 0, len(results))
	for name := range results {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, err := fmt.Fprintf(m.out, "%-8s %s\n", name+":", results[name]); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) Unconfigure(editorName string, dryRun bool) error {
	if !dryRun {
		unlock, err := m.lock()
		if err != nil {
			return err
		}
		defer unlock()
	}
	current, err := m.loadState()
	if err != nil {
		return err
	}
	var names []string
	if editorName != "" {
		if !validEditor(editorName) {
			return fmt.Errorf("unsupported editor %q", editorName)
		}
		names = []string{editorName}
	} else {
		for name := range current.Editors {
			names = append(names, name)
		}
		sort.Strings(names)
	}
	if len(names) == 0 {
		_, err := fmt.Fprintln(m.out, "No editor configuration is managed by devpack-for-go")
		return err
	}

	for _, name := range names {
		entry, ok := current.Editors[name]
		if !ok {
			if _, err := fmt.Fprintf(m.out, "%s is not configured\n", name); err != nil {
				return err
			}
			continue
		}
		for _, file := range entry.Files {
			if dryRun {
				if _, err := fmt.Fprintf(m.out, "Restore %s\n", file.Path); err != nil {
					return err
				}
				continue
			}
			if err := m.revertFile(file); err != nil {
				return fmt.Errorf("unconfigure %s: %w", name, err)
			}
		}
		if entry.ExtensionInstalled && !dryRun {
			output, err := m.runCommand(entry.VSCodeCommand, "--uninstall-extension", "golang.go")
			if err != nil {
				return fmt.Errorf("uninstall VS Code Go extension: %w: %s", err, strings.TrimSpace(string(output)))
			}
		}
		if !dryRun {
			delete(current.Editors, name)
			if err := m.saveState(current); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(m.out, "%s unconfigured successfully\n", name); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) plan(opts Options) (editorState, []fileChange, bool, error) {
	switch opts.Editor {
	case "vscode":
		return m.planVSCode(opts)
	case "vim", "nvim":
		return m.planVim(opts)
	case "emacs":
		return m.planEmacs(opts)
	default:
		return editorState{}, nil, false, fmt.Errorf("unsupported editor %q", opts.Editor)
	}
}

func (m *Manager) planVSCode(opts Options) (editorState, []fileChange, bool, error) {
	path := opts.Config
	if path == "" {
		path = filepath.Join(m.home, ".config", "Code", "User", "settings.json")
	}
	desired := []desiredJSON{
		{Pointer: "/go.alternateTools/gopls", Value: m.toolPath("gopls")},
		{Pointer: "/go.alternateTools/dlv", Value: m.toolPath("dlv")},
	}
	if opts.Lint {
		desired = append(desired,
			desiredJSON{Pointer: "/go.lintTool", Value: "golangci-lint-v2"},
			desiredJSON{Pointer: "/go.alternateTools/golangci-lint-v2", Value: m.toolPath("golangci-lint")},
		)
	}
	if opts.Gofumpt {
		desired = append(desired, desiredJSON{Pointer: "/gopls/formatting.gofumpt", Value: true})
	}
	change, err := m.jsonChange(path, desired)
	if err != nil {
		return editorState{}, nil, false, err
	}
	code, err := m.lookupPath("code")
	if err != nil {
		return editorState{}, nil, false, errors.New("VS Code command 'code' was not found on PATH")
	}
	output, err := m.runCommand(code, "--list-extensions")
	if err != nil {
		return editorState{}, nil, false, fmt.Errorf("list VS Code extensions: %w", err)
	}
	installed := false
	for _, line := range strings.Split(string(output), "\n") {
		if strings.EqualFold(strings.TrimSpace(line), "golang.go") {
			installed = true
			break
		}
	}
	entry := editorState{Editor: "vscode", Files: []fileState{change.state}, VSCodeCommand: code}
	return entry, []fileChange{change}, !installed, nil
}

func (m *Manager) planVim(opts Options) (editorState, []fileChange, bool, error) {
	client := strings.ToLower(opts.Client)
	if client == "" {
		choice, err := m.Choose("Language client", vimClients)
		if err != nil {
			return editorState{}, nil, false, err
		}
		client = choice
	}
	if !slices.Contains(vimClients, client) {
		return editorState{}, nil, false, fmt.Errorf("unsupported %s client %q", opts.Editor, client)
	}

	if client == "coc-go" || client == "coc" {
		path := opts.Config
		if path == "" {
			if opts.Editor == "nvim" {
				path = filepath.Join(m.home, ".config", "nvim", "coc-settings.json")
			} else {
				path = filepath.Join(m.home, ".vim", "coc-settings.json")
			}
		}
		var desired []desiredJSON
		if client == "coc-go" {
			desired = []desiredJSON{
				{Pointer: "/go.goplsPath", Value: m.toolPath("gopls")},
				{Pointer: "/go.checkForUpdates", Value: "disabled"},
			}
			if opts.Gofumpt {
				desired = append(desired, desiredJSON{Pointer: "/go.goplsOptions/gofumpt", Value: true})
			}
		} else {
			settings := map[string]any{
				"command":      m.toolPath("gopls"),
				"filetypes":    []string{"go", "gomod", "gowork", "gotmpl"},
				"rootPatterns": []string{"go.work", "go.mod", ".git"},
			}
			if opts.Gofumpt {
				settings["settings"] = map[string]any{"gopls": map[string]any{"gofumpt": true}}
			}
			desired = []desiredJSON{{Pointer: "/languageserver/devpack-gopls", Value: settings}}
		}
		change, err := m.jsonChange(path, desired)
		if err != nil {
			return editorState{}, nil, false, err
		}
		entry := editorState{Editor: opts.Editor, Client: client, Files: []fileState{change.state}}
		return entry, []fileChange{change}, false, nil
	}

	path := opts.Config
	if path == "" {
		path = m.vimConfigPath(opts.Editor)
	}
	script := m.vimScript(client, opts)
	if filepath.Ext(path) == ".lua" {
		script = "vim.cmd([=[\n" + script + "]=])\n"
	}
	change, err := m.blockChange(path, opts.Editor, script)
	if err != nil {
		return editorState{}, nil, false, err
	}
	changes := []fileChange{change}
	if client == "vim-go" {
		apps := []string{"gopls", "dlv", "gotests", "gofumpt", "govulncheck"}
		if opts.Lint {
			apps = append(apps, "golangci-lint")
		}
		for _, app := range apps {
			wrapper, err := m.wrapperChange(opts.Editor, app)
			if err != nil {
				return editorState{}, nil, false, err
			}
			changes = append(changes, wrapper)
		}
	}
	files := make([]fileState, 0, len(changes))
	for _, item := range changes {
		files = append(files, item.state)
	}
	entry := editorState{Editor: opts.Editor, Client: client, Files: files}
	return entry, changes, false, nil
}

func (m *Manager) planEmacs(opts Options) (editorState, []fileChange, bool, error) {
	client := strings.ToLower(opts.Client)
	if client == "" {
		client = m.detectEmacsClient()
		if client == "" {
			choice, err := m.Choose("Language client", emacsClients)
			if err != nil {
				return editorState{}, nil, false, err
			}
			client = choice
		}
	}
	if !slices.Contains(emacsClients, client) {
		return editorState{}, nil, false, fmt.Errorf("unsupported Emacs client %q", client)
	}
	path := opts.Config
	if path == "" {
		path = m.emacsConfigPath()
	}
	script := m.emacsScript(client, opts)
	change, err := m.blockChange(path, "emacs", script)
	if err != nil {
		return editorState{}, nil, false, err
	}
	entry := editorState{Editor: "emacs", Client: client, Files: []fileState{change.state}}
	return entry, []fileChange{change}, false, nil
}

func (m *Manager) jsonChange(path string, desired []desiredJSON) (fileChange, error) {
	resolved, input, mode, existed, err := readConfig(path)
	if err != nil {
		return fileChange{}, err
	}
	output, edits, err := updateJSONC(input, desired)
	if err != nil {
		return fileChange{}, fmt.Errorf("update %s: %w", resolved, err)
	}
	return fileChange{
		state: fileState{
			Path:           resolved,
			Kind:           "jsonc",
			OriginalExists: existed,
			OriginalBase64: base64.StdEncoding.EncodeToString(input),
			Mode:           uint32(mode),
			AppliedHash:    hash(output),
			JSONEdits:      edits,
		},
		output: output,
	}, nil
}

func (m *Manager) blockChange(path, editorName, script string) (fileChange, error) {
	resolved, input, mode, existed, err := readConfig(path)
	if err != nil {
		return fileChange{}, err
	}
	prefix := "\""
	if editorName == "emacs" {
		prefix = ";;"
	} else if filepath.Ext(path) == ".lua" {
		prefix = "--"
	}
	begin := prefix + " BEGIN devpack-for-go managed configuration"
	end := prefix + " END devpack-for-go managed configuration"
	if bytes.Contains(input, []byte(begin)) || bytes.Contains(input, []byte(end)) {
		return fileChange{}, fmt.Errorf("%s already contains a devpack-for-go managed marker", resolved)
	}
	block := begin + "\n" + script
	if !strings.HasSuffix(block, "\n") {
		block += "\n"
	}
	block += end + "\n"
	output := append([]byte(nil), input...)
	if len(output) > 0 && !bytes.HasSuffix(output, []byte("\n")) {
		output = append(output, '\n')
	}
	if len(output) > 0 {
		output = append(output, '\n')
	}
	output = append(output, block...)
	return fileChange{
		state: fileState{
			Path:           resolved,
			Kind:           "block",
			OriginalExists: existed,
			OriginalBase64: base64.StdEncoding.EncodeToString(input),
			Mode:           uint32(mode),
			AppliedHash:    hash(output),
			ManagedBlock:   block,
		},
		output: output,
	}, nil
}

func (m *Manager) vimScript(client string, opts Options) string {
	switch client {
	case "vim-go":
		lines := []string{
			fmt.Sprintf("let $PATH = '%s:' . $PATH", vimQuote(filepath.Join(m.stateDir, "bin", opts.Editor))),
			"let g:go_gopls_enabled = 1",
		}
		if opts.Gofumpt {
			lines = append(lines, "let g:go_gopls_gofumpt = v:true")
		}
		if opts.Lint {
			lines = append(lines, "let g:go_metalinter_command = 'golangci-lint'")
		}
		return strings.Join(lines, "\n") + "\n"
	case "vim-lsp":
		return fmt.Sprintf("if executable('%s')\n  augroup devpack_for_go_lsp\n    autocmd!\n    autocmd User lsp_setup call lsp#register_server({'name': 'devpack-gopls', 'cmd': {server_info->['%s']}, 'allowlist': ['go', 'gomod', 'gowork', 'gotmpl']})\n  augroup END\nendif\n", m.toolPath("gopls"), m.toolPath("gopls"))
	default:
		return ""
	}
}

func (m *Manager) wrapperChange(editorName, app string) (fileChange, error) {
	path := filepath.Join(m.stateDir, "bin", editorName, app)
	resolved, original, _, existed, err := readConfig(path)
	if err != nil {
		return fileChange{}, err
	}
	output := []byte("#!/bin/sh\nexec " + m.toolPath(app) + " \"$@\"\n")
	return fileChange{
		state: fileState{
			Path:           resolved,
			Kind:           "generated",
			OriginalExists: existed,
			OriginalBase64: base64.StdEncoding.EncodeToString(original),
			Mode:           0o755,
			AppliedHash:    hash(output),
		},
		output: output,
	}, nil
}

func (m *Manager) emacsScript(client string, opts Options) string {
	gopls := strconv.Quote(m.toolPath("gopls"))
	if client == "eglot" {
		return fmt.Sprintf("(with-eval-after-load 'eglot\n  (add-to-list 'eglot-server-programs\n               '((go-mode go-ts-mode go-dot-mod-mode go-dot-work-mode) . (%s))))\n(add-hook 'go-mode-hook #'eglot-ensure)\n(add-hook 'go-ts-mode-hook #'eglot-ensure)\n", gopls)
	}
	gofumpt := "nil"
	if opts.Gofumpt {
		gofumpt = "t"
	}
	return fmt.Sprintf("(with-eval-after-load 'lsp-go\n  (setq lsp-go-gopls-server-path %s\n        lsp-go-use-gofumpt %s))\n(add-hook 'go-mode-hook #'lsp-deferred)\n(add-hook 'go-ts-mode-hook #'lsp-deferred)\n", gopls, gofumpt)
}

func (m *Manager) editorHealthy(entry editorState) (bool, string) {
	if entry.Status != "active" {
		return false, "an earlier operation did not complete"
	}
	for _, file := range entry.Files {
		content, err := os.ReadFile(file.Path)
		if err != nil {
			return false, file.Path + " is unavailable"
		}
		if hash(content) == file.AppliedHash {
			continue
		}
		switch file.Kind {
		case "jsonc":
			if jsonEditsHealthy(content, file.JSONEdits) {
				continue
			}
		case "block":
			if bytes.Contains(content, []byte(file.ManagedBlock)) {
				continue
			}
		case "generated":
			// Generated wrappers must remain byte-for-byte identical.
		}
		return false, file.Path + " no longer contains the applied settings"
	}
	return true, ""
}

func (m *Manager) revertFile(file fileState) error {
	current, err := os.ReadFile(file.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if hash(current) == file.AppliedHash {
		return restoreOriginal(file)
	}
	if file.OriginalExists {
		original, decodeErr := base64.StdEncoding.DecodeString(file.OriginalBase64)
		if decodeErr != nil {
			return fmt.Errorf("decode backup for %s: %w", file.Path, decodeErr)
		}
		if bytes.Equal(current, original) {
			return nil
		}
	}
	var output []byte
	switch file.Kind {
	case "jsonc":
		output, err = revertJSONC(current, file.JSONEdits)
	case "block":
		if !bytes.Contains(current, []byte(file.ManagedBlock)) {
			return fmt.Errorf("managed block in %s was changed", file.Path)
		}
		output = bytes.Replace(current, []byte("\n"+file.ManagedBlock), nil, 1)
		if bytes.Equal(output, current) {
			output = bytes.Replace(current, []byte(file.ManagedBlock), nil, 1)
		}
	case "generated":
		return fmt.Errorf("generated wrapper %s was changed", file.Path)
	default:
		return fmt.Errorf("unknown managed file kind %q", file.Kind)
	}
	if err != nil {
		return err
	}
	return atomicWrite(file.Path, output, os.FileMode(file.Mode))
}

func restoreOriginal(file fileState) error {
	if !file.OriginalExists {
		if err := os.Remove(file.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	original, err := base64.StdEncoding.DecodeString(file.OriginalBase64)
	if err != nil {
		return fmt.Errorf("decode backup for %s: %w", file.Path, err)
	}
	return atomicWrite(file.Path, original, os.FileMode(file.Mode))
}

func (m *Manager) rollbackConfigure(current state, editorName string, planned editorState, applied []fileChange, extensionInstalled bool, cause error) error {
	failed, rollbackErr := restoreAppliedChanges(applied)
	if rollbackErr == nil && !extensionInstalled {
		delete(current.Editors, editorName)
	} else {
		planned.Status = "pending"
		planned.ExtensionInstalled = extensionInstalled
		planned.Files = make([]fileState, 0, len(failed))
		for _, change := range failed {
			planned.Files = append(planned.Files, change.state)
		}
		current.Editors[editorName] = planned
	}
	if err := m.saveState(current); err != nil {
		rollbackErr = errors.Join(rollbackErr, fmt.Errorf("save rollback state: %w", err))
	}
	return errors.Join(cause, rollbackErr)
}

func restoreAppliedChanges(changes []fileChange) ([]fileChange, error) {
	var failed []fileChange
	var result error
	for i := len(changes) - 1; i >= 0; i-- {
		change := changes[i]
		content, err := os.ReadFile(change.state.Path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) && !change.state.OriginalExists {
				continue
			}
			failed = append(failed, change)
			result = errors.Join(result, fmt.Errorf("roll back %s: %w", change.state.Path, err))
			continue
		}
		if hash(content) != change.state.AppliedHash {
			failed = append(failed, change)
			result = errors.Join(result, fmt.Errorf("roll back %s: file changed after it was written", change.state.Path))
			continue
		}
		if err := restoreOriginal(change.state); err != nil {
			failed = append(failed, change)
			result = errors.Join(result, fmt.Errorf("roll back %s: %w", change.state.Path, err))
		}
	}
	return failed, result
}

func (m *Manager) loadState() (state, error) {
	current := state{Version: stateVersion, Editors: make(map[string]editorState)}
	content, err := os.ReadFile(m.statePath())
	if errors.Is(err, os.ErrNotExist) {
		return current, nil
	}
	if err != nil {
		return current, fmt.Errorf("read state: %w", err)
	}
	if err := json.Unmarshal(content, &current); err != nil {
		return current, fmt.Errorf("parse state: %w", err)
	}
	if current.Version != stateVersion {
		return current, fmt.Errorf("unsupported state version %d", current.Version)
	}
	if current.Editors == nil {
		current.Editors = make(map[string]editorState)
	}
	return current, nil
}

func (m *Manager) saveState(current state) error {
	if err := os.MkdirAll(m.stateDir, 0o700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	content, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	return atomicWrite(m.statePath(), content, 0o600)
}

func (m *Manager) statePath() string {
	return filepath.Join(m.stateDir, "state.json")
}

func (m *Manager) lock() (func(), error) {
	if err := os.MkdirAll(m.stateDir, 0o700); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(m.stateDir, "lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open editor configuration lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock editor configuration: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}

func (m *Manager) toolPath(app string) string {
	return filepath.Join("/snap/bin", m.instance+"."+app)
}

// DetectEditors returns the editors that appear to be installed, falling back to
// every supported editor when nothing is detected.
func (m *Manager) DetectEditors() []string {
	var found []string
	checks := []struct {
		name string
		bins []string
		path string
	}{
		{name: "vscode", bins: []string{"code"}, path: filepath.Join(m.home, ".config", "Code")},
		{name: "vim", bins: []string{"vim"}, path: filepath.Join(m.home, ".vimrc")},
		{name: "nvim", bins: []string{"nvim"}, path: filepath.Join(m.home, ".config", "nvim")},
		{name: "emacs", bins: []string{"emacs"}, path: filepath.Join(m.home, ".emacs.d")},
	}
	for _, check := range checks {
		available := false
		for _, binary := range check.bins {
			if _, err := m.lookupPath(binary); err == nil {
				available = true
			}
		}
		if _, err := os.Stat(check.path); err == nil {
			available = true
		}
		if available {
			found = append(found, check.name)
		}
	}
	if len(found) == 0 {
		return slices.Clone(supportedEditors)
	}
	return found
}

// Interactive reports whether standard input is an interactive terminal.
func (m *Manager) Interactive() bool {
	file, ok := m.in.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// Choose prompts for one of the given choices. It reports an error instead of
// blocking when standard input is not an interactive terminal.
func (m *Manager) Choose(label string, choices []string) (string, error) {
	if !m.Interactive() {
		return "", fmt.Errorf("%s must be selected explicitly in non-interactive mode", strings.ToLower(label))
	}
	if len(choices) == 1 {
		return choices[0], nil
	}
	if _, err := fmt.Fprintf(m.out, "%s:\n", label); err != nil {
		return "", err
	}
	for i, choice := range choices {
		if _, err := fmt.Fprintf(m.out, "  %d. %s\n", i+1, choice); err != nil {
			return "", err
		}
	}
	if _, err := fmt.Fprint(m.out, "Selection: "); err != nil {
		return "", err
	}
	line, err := bufio.NewReader(m.in).ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) {
			return "", fmt.Errorf("%s must be selected explicitly in non-interactive mode", strings.ToLower(label))
		}
		return "", err
	}
	selection, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || selection < 1 || selection > len(choices) {
		return "", errors.New("invalid selection")
	}
	return choices[selection-1], nil
}

// Confirm asks a yes/no question that defaults to no. It reports an error
// instead of blocking when standard input is not an interactive terminal.
func (m *Manager) Confirm(prompt string) (bool, error) {
	return m.ConfirmDefault(prompt, false)
}

// ConfirmDefault asks a yes/no question with the given default answer. An empty
// reply selects the default.
func (m *Manager) ConfirmDefault(prompt string, def bool) (bool, error) {
	if !m.Interactive() {
		return false, errors.New("--yes is required in non-interactive mode")
	}
	suffix := " [y/N] "
	if def {
		suffix = " [Y/n] "
	}
	if _, err := fmt.Fprintf(m.out, "%s%s", prompt, suffix); err != nil {
		return false, err
	}
	line, err := bufio.NewReader(m.in).ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) {
			return false, errors.New("--yes is required in non-interactive mode")
		}
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer == "" {
		return def, nil
	}
	return answer == "y" || answer == "yes", nil
}

func (m *Manager) vimConfigPath(editorName string) string {
	if editorName == "nvim" {
		lua := filepath.Join(m.home, ".config", "nvim", "init.lua")
		if _, err := os.Stat(lua); err == nil {
			return lua
		}
		return filepath.Join(m.home, ".config", "nvim", "init.vim")
	}
	vimrc := filepath.Join(m.home, ".vimrc")
	alternate := filepath.Join(m.home, ".vim", "vimrc")
	if _, err := os.Stat(vimrc); errors.Is(err, os.ErrNotExist) {
		if _, err := os.Stat(alternate); err == nil {
			return alternate
		}
	}
	return vimrc
}

func (m *Manager) emacsConfigPath() string {
	candidates := []string{
		filepath.Join(m.home, ".config", "emacs", "init.el"),
		filepath.Join(m.home, ".emacs"),
		filepath.Join(m.home, ".emacs.el"),
		filepath.Join(m.home, ".emacs.d", "init.el"),
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return candidates[len(candidates)-1]
}

func (m *Manager) detectEmacsClient() string {
	path := m.emacsConfigPath()
	content, _ := os.ReadFile(path)
	hasEglot := bytes.Contains(content, []byte("eglot"))
	hasLSP := bytes.Contains(content, []byte("lsp-mode")) || bytes.Contains(content, []byte("lsp-deferred"))
	if hasEglot && !hasLSP {
		return "eglot"
	}
	if hasLSP && !hasEglot {
		return "lsp-mode"
	}
	return ""
}

func readConfig(path string) (resolved string, content []byte, mode os.FileMode, existed bool, err error) {
	path, err = filepath.Abs(path)
	if err != nil {
		return "", nil, 0, false, err
	}
	resolved = path
	if info, lstatErr := os.Lstat(path); lstatErr == nil && info.Mode()&os.ModeSymlink != 0 {
		resolved, err = filepath.EvalSymlinks(path)
		if err != nil {
			return "", nil, 0, false, fmt.Errorf("resolve %s: %w", path, err)
		}
	}
	content, err = os.ReadFile(resolved)
	if errors.Is(err, os.ErrNotExist) {
		return resolved, nil, 0o644, false, nil
	}
	if err != nil {
		return "", nil, 0, false, fmt.Errorf("read %s: %w", resolved, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", nil, 0, false, err
	}
	return resolved, content, info.Mode().Perm(), true, nil
}

func originalMatches(change fileChange) error {
	content, err := os.ReadFile(change.state.Path)
	if errors.Is(err, os.ErrNotExist) && !change.state.OriginalExists {
		return nil
	}
	if err != nil {
		return fmt.Errorf("verify %s before writing: %w", change.state.Path, err)
	}
	if !change.state.OriginalExists {
		return fmt.Errorf("%s was created after it was previewed; refusing to overwrite it", change.state.Path)
	}
	original, err := base64.StdEncoding.DecodeString(change.state.OriginalBase64)
	if err != nil {
		return fmt.Errorf("decode original %s: %w", change.state.Path, err)
	}
	if !bytes.Equal(content, original) {
		return fmt.Errorf("%s changed after it was previewed; refusing to overwrite it", change.state.Path)
	}
	return nil
}

func atomicWrite(path string, content []byte, mode os.FileMode) error {
	return atomicWriteChecked(path, content, mode, nil)
}

func atomicWriteChecked(path string, content []byte, mode os.FileMode, check func() error) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	temp, err := os.CreateTemp(dir, ".devpack-for-go-*")
	if err != nil {
		return fmt.Errorf("create temporary file for %s: %w", path, err)
	}
	tempName := temp.Name()
	defer func() { _ = os.Remove(tempName) }()
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(content); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if check != nil {
		if err := check(); err != nil {
			return err
		}
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

func validEditor(name string) bool {
	return slices.Contains(supportedEditors, name)
}

func actionFor(exists bool) string {
	if exists {
		return "Update"
	}
	return "Create"
}

func hash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func vimQuote(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}
