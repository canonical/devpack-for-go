package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/canonical/devpack-for-go/internal/editor"
)

// testEnv points the helper at temporary home and state directories and returns
// both, mirroring the SNAP_REAL_HOME and SNAP_USER_COMMON layout of the snap.
func testEnv(t *testing.T) (home, state string) {
	t.Helper()
	home = t.TempDir()
	state = t.TempDir()
	t.Setenv("SNAP_REAL_HOME", home)
	t.Setenv("SNAP_USER_COMMON", state)
	t.Setenv("SNAP_INSTANCE_NAME", "devpack-for-go")
	return home, state
}

// usePrompts scripts the setup wizard's answers and restores the production
// prompter afterwards.
func usePrompts(prompts prompter) func() {
	previous := newPrompter
	newPrompter = func(*editor.Manager) prompter { return prompts }
	return func() { newPrompter = previous }
}

type scriptedPrompts struct {
	t        *testing.T
	choices  map[string]string
	confirms map[string]bool
}

func (s *scriptedPrompts) Interactive() bool { return true }

func (s *scriptedPrompts) Choose(label string, choices []string) (string, error) {
	s.t.Helper()
	answer, ok := s.choices[label]
	if !ok {
		s.t.Fatalf("unexpected prompt %q with choices %v", label, choices)
	}
	for _, choice := range choices {
		if choice == answer {
			return answer, nil
		}
	}
	s.t.Fatalf("scripted answer %q is not among %v", answer, choices)
	return "", nil
}

func (s *scriptedPrompts) ConfirmDefault(prompt string, def bool) (bool, error) {
	s.t.Helper()
	answer, ok := s.confirms[prompt]
	if !ok {
		s.t.Fatalf("unexpected confirmation %q", prompt)
	}
	return answer, nil
}

type noPrompts struct{ t *testing.T }

func (n noPrompts) Interactive() bool { return false }

func (n noPrompts) Choose(label string, _ []string) (string, error) {
	n.t.Fatalf("unexpected prompt %q", label)
	return "", nil
}

func (n noPrompts) ConfirmDefault(prompt string, _ bool) (bool, error) {
	n.t.Fatalf("unexpected confirmation %q", prompt)
	return false, nil
}

type abortedPrompts struct{}

func (abortedPrompts) Interactive() bool { return true }

func (abortedPrompts) Choose(string, []string) (string, error) {
	return "", errors.New("setup aborted")
}

func (abortedPrompts) ConfirmDefault(string, bool) (bool, error) {
	return false, errors.New("setup aborted")
}

func TestNoArgumentsPrintsOnboarding(t *testing.T) {
	out := new(bytes.Buffer)
	if err := Run(nil, strings.NewReader(""), out, out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"devpack-for-go doctor",
		"devpack-for-go setup",
		"devpack-for-go versions",
		"devpack-for-go help",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("onboarding output does not mention %q:\n%s", want, text)
		}
	}
}

func TestHelpListsCommandsAndOptions(t *testing.T) {
	out := new(bytes.Buffer)
	if err := Run([]string{"help"}, strings.NewReader(""), out, out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"setup", "doctor", "versions", "editor configure", "editor status", "editor unconfigure", "--gofumpt", "--lint=false"} {
		if !strings.Contains(text, want) {
			t.Errorf("help output does not mention %q:\n%s", want, text)
		}
	}
}

func TestUnknownCommandIsRejected(t *testing.T) {
	out := new(bytes.Buffer)
	if err := Run([]string{"frobnicate"}, strings.NewReader(""), out, out); err == nil {
		t.Fatal("expected unknown command error")
	}
}

func TestSetupWizardConfiguresSelectedEditor(t *testing.T) {
	home, _ := testEnv(t)
	config := filepath.Join(home, ".vimrc")
	defer usePrompts(&scriptedPrompts{
		t: t,
		choices: map[string]string{
			"Editor":          "vim",
			"Language client": "vim-lsp",
		},
		confirms: map[string]bool{
			"Use the bundled golangci-lint?": true,
			"Enable gofumpt formatting?":     false,
		},
	})()

	out := new(bytes.Buffer)
	if err := Run([]string{"setup", "--config", config, "--yes"}, strings.NewReader(""), out, out); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(content, []byte("devpack-for-go.gopls")) {
		t.Fatalf("wizard did not point the editor at the bundled gopls:\n%s", content)
	}

	status := new(bytes.Buffer)
	if err := Run([]string{"editor", "status", "--editor", "vim"}, strings.NewReader(""), status, status); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status.String(), "vim:") || strings.Contains(status.String(), "unconfigured") {
		t.Fatalf("vim is not reported configured:\n%s", status)
	}
}

func TestSetupWizardAbortWritesNothing(t *testing.T) {
	home, _ := testEnv(t)
	config := filepath.Join(home, ".vimrc")
	defer usePrompts(abortedPrompts{})()

	out := new(bytes.Buffer)
	err := Run([]string{"setup", "--config", config}, strings.NewReader(""), out, out)
	if err == nil {
		t.Fatal("expected abort to fail the command")
	}
	if _, statErr := os.Stat(config); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("aborted setup created %s: %v", config, statErr)
	}

	status := new(bytes.Buffer)
	if err := Run([]string{"editor", "status", "--editor", "vim"}, strings.NewReader(""), status, status); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status.String(), "unconfigured") {
		t.Fatalf("aborted setup left state behind:\n%s", status)
	}
}

func TestSetupWizardDeclinesBundledLinter(t *testing.T) {
	home, state := testEnv(t)
	config := filepath.Join(home, ".vimrc")
	defer usePrompts(&scriptedPrompts{
		t: t,
		choices: map[string]string{
			"Editor":          "vim",
			"Language client": "vim-go",
		},
		confirms: map[string]bool{
			"Use the bundled golangci-lint?": false,
			"Enable gofumpt formatting?":     false,
		},
	})()

	out := new(bytes.Buffer)
	if err := Run([]string{"setup", "--config", config, "--yes"}, strings.NewReader(""), out, out); err != nil {
		t.Fatal(err)
	}

	wrappers := filepath.Join(state, "editor-config", "bin", "vim")
	if _, err := os.Stat(filepath.Join(wrappers, "gopls")); err != nil {
		t.Fatalf("vim-go wrapper is missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wrappers, "golangci-lint")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("declining the linter still created a wrapper: %v", err)
	}
	vimrc, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(vimrc, []byte("go_metalinter_command")) {
		t.Fatalf("declining the linter still configured it:\n%s", vimrc)
	}
}

func TestSetupFlagsConfigureWithoutPrompts(t *testing.T) {
	home, state := testEnv(t)
	config := filepath.Join(home, ".vimrc")
	defer usePrompts(noPrompts{t: t})()

	out := new(bytes.Buffer)
	args := []string{
		"setup",
		"--editor", "vim",
		"--client", "vim-go",
		"--config", config,
		"--lint=false",
		"--yes",
	}
	if err := Run(args, strings.NewReader(""), out, out); err != nil {
		t.Fatal(err)
	}

	wrappers := filepath.Join(state, "editor-config", "bin", "vim")
	if _, err := os.Stat(filepath.Join(wrappers, "gopls")); err != nil {
		t.Fatalf("vim-go wrapper is missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wrappers, "golangci-lint")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("--lint=false still created a linter wrapper: %v", err)
	}
}

func TestSetupRequiresYesWithoutTerminalConfirmation(t *testing.T) {
	home, _ := testEnv(t)
	config := filepath.Join(home, ".vimrc")
	defer usePrompts(noPrompts{t: t})()

	out := new(bytes.Buffer)
	args := []string{"setup", "--editor", "vim", "--client", "vim-lsp", "--config", config}
	err := Run(args, strings.NewReader(""), out, out)
	if err == nil {
		t.Fatal("expected confirmation to be required")
	}
	if _, statErr := os.Stat(config); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("unconfirmed setup created %s: %v", config, statErr)
	}
}

func TestSetupDryRunWritesNothing(t *testing.T) {
	home, _ := testEnv(t)
	config := filepath.Join(home, ".vimrc")
	defer usePrompts(noPrompts{t: t})()

	out := new(bytes.Buffer)
	args := []string{"setup", "--editor", "vim", "--client", "vim-lsp", "--config", config, "--dry-run"}
	if err := Run(args, strings.NewReader(""), out, out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Create "+config) {
		t.Fatalf("dry run did not preview the change:\n%s", out)
	}
	if _, err := os.Stat(config); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry run created %s: %v", config, err)
	}
}

func TestSetupWithoutTerminalRequiresEditorFlag(t *testing.T) {
	home, _ := testEnv(t)
	config := filepath.Join(home, ".vimrc")
	defer usePrompts(noPrompts{t: t})()

	out := new(bytes.Buffer)
	err := Run([]string{"setup", "--config", config, "--yes"}, strings.NewReader(""), out, out)
	if err == nil {
		t.Fatal("expected setup to require an explicit editor")
	}
	if !strings.Contains(err.Error(), "non-interactive mode") {
		t.Fatalf("error does not explain how to proceed: %v", err)
	}
	if _, statErr := os.Stat(config); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed setup created %s: %v", config, statErr)
	}
}

func TestDoctorReportsEditorIntegration(t *testing.T) {
	testEnv(t)
	out := new(bytes.Buffer)
	if err := Run([]string{"doctor"}, strings.NewReader(""), out, out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "Editor integration: not configured") {
		t.Fatalf("doctor did not report editor integration:\n%s", text)
	}
	if !strings.Contains(text, "devpack-for-go setup") {
		t.Fatalf("doctor did not suggest setup:\n%s", text)
	}
}

func TestDoctorReportsConfiguredEditor(t *testing.T) {
	home, _ := testEnv(t)
	config := filepath.Join(home, ".vimrc")
	defer usePrompts(noPrompts{t: t})()

	if err := Run([]string{"setup", "--editor", "vim", "--client", "vim-lsp", "--config", config, "--yes"}, strings.NewReader(""), new(bytes.Buffer), new(bytes.Buffer)); err != nil {
		t.Fatal(err)
	}

	out := new(bytes.Buffer)
	if err := Run([]string{"doctor"}, strings.NewReader(""), out, out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "vim (configured)") {
		t.Fatalf("doctor did not report the configured editor:\n%s", text)
	}
	if strings.Contains(text, "not configured") {
		t.Fatalf("doctor reported no editor integration:\n%s", text)
	}
	if strings.Contains(text, "devpack-for-go setup") {
		t.Fatalf("doctor suggests setup for a healthy configuration:\n%s", text)
	}
}

func TestDoctorReportsDriftedEditor(t *testing.T) {
	home, _ := testEnv(t)
	config := filepath.Join(home, ".vimrc")
	defer usePrompts(noPrompts{t: t})()

	if err := Run([]string{"setup", "--editor", "vim", "--client", "vim-lsp", "--config", config, "--yes"}, strings.NewReader(""), new(bytes.Buffer), new(bytes.Buffer)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte("set number\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := new(bytes.Buffer)
	if err := Run([]string{"doctor"}, strings.NewReader(""), out, out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "vim (drifted)") {
		t.Fatalf("doctor did not report drift:\n%s", text)
	}
	if !strings.Contains(text, "editor unconfigure") {
		t.Fatalf("doctor did not explain how to repair drift:\n%s", text)
	}
}
