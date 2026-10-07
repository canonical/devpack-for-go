package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/canonical/devpack-for-go/internal/editor"
	"github.com/canonical/devpack-for-go/internal/toolversions"
)

const welcome = `Devpack for Go is installed.

Next steps:
  1. Check your Go toolchain:
       devpack-for-go doctor

  2. Configure an editor:
       devpack-for-go setup

  3. View bundled tool versions:
       devpack-for-go versions

Run 'devpack-for-go help' for all commands.`

const usage = `Usage:
  devpack-for-go setup [options]
  devpack-for-go doctor
  devpack-for-go versions
  devpack-for-go editor configure [options]
  devpack-for-go editor status [--editor EDITOR] [--json]
  devpack-for-go editor unconfigure [--editor EDITOR] [--dry-run]

Options:
  --editor EDITOR   vscode, vim, nvim, or emacs
  --client CLIENT   vim-go, coc-go, coc, vim-lsp, eglot, or lsp-mode
  --config PATH     explicit editor configuration file
  --lint=false      skip the bundled golangci-lint
  --gofumpt         enable gofumpt formatting
  --dry-run         show changes without writing them
  --yes             apply changes without confirmation

setup asks for every option that is left out and previews all changes before
applying them. The same options configure an editor non-interactively.`

// prompter answers the setup wizard's questions. *editor.Manager implements it
// for interactive terminals.
type prompter interface {
	Interactive() bool
	Choose(label string, choices []string) (string, error)
	ConfirmDefault(prompt string, def bool) (bool, error)
}

// newPrompter is replaced in tests to script the wizard's answers.
var newPrompter = func(manager *editor.Manager) prompter { return manager }

func Run(args []string, in io.Reader, out, errOut io.Writer) error {
	if len(args) == 0 {
		_, err := fmt.Fprintln(out, welcome)
		return err
	}

	switch args[0] {
	case "help", "-h", "--help":
		_, err := fmt.Fprintln(out, usage)
		return err
	case "setup":
		return runSetup(args[1:], in, out, errOut)
	case "versions":
		return printVersions(out)
	case "doctor":
		return doctor(in, out)
	case "editor":
		return runEditor(args[1:], in, out, errOut)
	default:
		return fmt.Errorf("unknown command %q\n\n%s", args[0], usage)
	}
}

func printVersions(out io.Writer) error {
	tools, err := toolversions.All()
	if err != nil {
		return err
	}
	for _, tool := range tools {
		if _, err := fmt.Fprintf(out, "%-16s %s\n", tool.Name+":", tool.Version); err != nil {
			return err
		}
	}
	return nil
}

func doctor(in io.Reader, out io.Writer) error {
	if _, err := fmt.Fprintln(out, "Devpack for Go diagnostics"); err != nil {
		return err
	}
	if goPath, err := exec.LookPath("go"); err == nil {
		version, versionErr := exec.Command(goPath, "version").CombinedOutput()
		if versionErr != nil {
			if _, err := fmt.Fprintf(out, "Go:      %s (version check failed: %v)\n", goPath, versionErr); err != nil {
				return err
			}
		} else {
			if _, err := fmt.Fprintf(out, "Go:      %s\n", strings.TrimSpace(string(version))); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(out, "Go path: %s\n", goPath); err != nil {
				return err
			}
		}
	} else {
		if _, err := fmt.Fprintln(out, "Go:      not found"); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(out, "Install it with: sudo snap install go --classic"); err != nil {
			return err
		}
	}

	instance := os.Getenv("SNAP_INSTANCE_NAME")
	if instance == "" {
		instance = "devpack-for-go"
	}
	for _, app := range []string{"dlv", "gopls", "golangci-lint", "govulncheck", "gotests", "gofumpt"} {
		path := filepath.Join("/snap/bin", instance+"."+app)
		if _, err := os.Stat(path); err == nil {
			if _, err := fmt.Fprintf(out, "%-14s %s\n", app+":", path); err != nil {
				return err
			}
		} else {
			if _, err := fmt.Fprintf(out, "%-14s %s (not installed)\n", app+":", path); err != nil {
				return err
			}
		}
	}
	return editorIntegration(in, out)
}

func editorIntegration(in io.Reader, out io.Writer) error {
	manager, err := editor.NewManager(in, out)
	if err != nil {
		_, printErr := fmt.Fprintf(out, "Editor integration: unavailable (%v)\n", err)
		return printErr
	}
	summary, err := manager.Summary()
	if err != nil {
		return err
	}

	var managed []string
	drifted := false
	for _, name := range editor.Supported() {
		status := summary[name]
		if status == "unconfigured" {
			continue
		}
		if status != "configured" {
			drifted = true
		}
		managed = append(managed, name+" ("+status+")")
	}
	if len(managed) == 0 {
		if _, err := fmt.Fprintln(out, "Editor integration: not configured"); err != nil {
			return err
		}
		_, err := fmt.Fprintln(out, "Run 'devpack-for-go setup' to configure an editor.")
		return err
	}
	if _, err := fmt.Fprintf(out, "Editor integration: %s\n", strings.Join(managed, ", ")); err != nil {
		return err
	}
	if drifted {
		_, err := fmt.Fprintln(out, "Repair drifted editors with 'devpack-for-go editor unconfigure' followed by 'devpack-for-go setup'.")
		return err
	}
	return nil
}

func runSetup(args []string, in io.Reader, out, errOut io.Writer) error {
	parsed, err := parseOptions("setup", args, errOut)
	if err != nil {
		return err
	}
	manager, err := editor.NewManager(in, out)
	if err != nil {
		return err
	}
	prompts := newPrompter(manager)
	opts := parsed.opts
	// Without a terminal the wizard stays silent and Configure reports which
	// choice must be supplied as a flag.
	interactive := prompts.Interactive()

	if _, err := fmt.Fprintln(out, "Devpack for Go setup"); err != nil {
		return err
	}
	if interactive && opts.editorName == "" {
		choice, err := prompts.Choose("Editor", manager.DetectEditors())
		if err != nil {
			return err
		}
		opts.editorName = choice
	}
	opts.editorName = strings.ToLower(opts.editorName)
	if clients := editor.ClientsFor(opts.editorName); interactive && opts.client == "" && len(clients) > 0 {
		choice, err := prompts.Choose("Language client", clients)
		if err != nil {
			return err
		}
		opts.client = choice
	}
	if interactive && !parsed.provided["lint"] {
		enabled, err := prompts.ConfirmDefault("Use the bundled golangci-lint?", true)
		if err != nil {
			return err
		}
		opts.lint = enabled
	}
	if interactive && !parsed.provided["gofumpt"] {
		enabled, err := prompts.ConfirmDefault("Enable gofumpt formatting?", false)
		if err != nil {
			return err
		}
		opts.gofumpt = enabled
	}
	if _, err := fmt.Fprintln(out); err != nil {
		return err
	}
	return manager.Configure(opts.editorOptions())
}

func runEditor(args []string, in io.Reader, out, errOut io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("missing editor action: configure, status, or unconfigure")
	}
	action := args[0]
	parsed, err := parseOptions("editor "+action, args[1:], errOut)
	if err != nil {
		return err
	}
	manager, err := editor.NewManager(in, out)
	if err != nil {
		return err
	}
	opts := parsed.opts

	switch action {
	case "configure":
		return manager.Configure(opts.editorOptions())
	case "status":
		return manager.Status(opts.editorName, opts.jsonOutput)
	case "unconfigure":
		return manager.Unconfigure(opts.editorName, opts.dryRun)
	default:
		return fmt.Errorf("unknown editor action %q", action)
	}
}

type options struct {
	editorName string
	client     string
	config     string
	lint       bool
	gofumpt    bool
	dryRun     bool
	yes        bool
	jsonOutput bool
}

func (o options) editorOptions() editor.Options {
	return editor.Options{
		Editor:  o.editorName,
		Client:  o.client,
		Config:  o.config,
		Lint:    o.lint,
		Gofumpt: o.gofumpt,
		DryRun:  o.dryRun,
		Yes:     o.yes,
	}
}

type parsedOptions struct {
	opts     options
	provided map[string]bool
}

func parseOptions(name string, args []string, errOut io.Writer) (parsedOptions, error) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(errOut)
	var opts options
	flags.StringVar(&opts.editorName, "editor", "", "editor to configure")
	flags.StringVar(&opts.client, "client", "", "Vim or Emacs language client")
	flags.StringVar(&opts.config, "config", "", "explicit editor configuration file")
	flags.BoolVar(&opts.lint, "lint", true, "configure golangci-lint")
	flags.BoolVar(&opts.gofumpt, "gofumpt", false, "enable gofumpt formatting")
	flags.BoolVar(&opts.dryRun, "dry-run", false, "show changes without writing them")
	flags.BoolVar(&opts.yes, "yes", false, "apply changes without confirmation")
	flags.BoolVar(&opts.jsonOutput, "json", false, "print status as JSON")
	if err := flags.Parse(args); err != nil {
		return parsedOptions{}, err
	}
	provided := make(map[string]bool)
	flags.Visit(func(f *flag.Flag) { provided[f.Name] = true })
	return parsedOptions{opts: opts, provided: provided}, nil
}
