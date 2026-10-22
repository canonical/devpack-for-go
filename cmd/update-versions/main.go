package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

var modules = map[string]string{
	"DLV_VERSION":           "github.com/go-delve/delve",
	"GOPLS_VERSION":         "golang.org/x/tools/gopls",
	"GOLANGCI_LINT_VERSION": "github.com/golangci/golangci-lint/v2",
	"GOVULNCHECK_VERSION":   "golang.org/x/vuln",
	"GOTESTS_VERSION":       "github.com/cweill/gotests",
	"GOFUMPT_VERSION":       "mvdan.cc/gofumpt",
}

func main() {
	write := flag.Bool("write", false, "update the version manifest")
	manifest := flag.String("manifest", "internal/toolversions/versions.env", "version manifest path")
	flag.Parse()

	content, err := os.ReadFile(*manifest)
	if err != nil {
		fatal(err)
	}
	lines := strings.Split(string(content), "\n")
	changed := false
	client := &http.Client{Timeout: 30 * time.Second}
	for index, line := range lines {
		key, current, ok := strings.Cut(line, "=")
		module, tracked := modules[key]
		if !ok || !tracked {
			continue
		}
		latest, err := latestVersion(client, module)
		if err != nil {
			fatal(fmt.Errorf("check %s: %w", module, err))
		}
		if latest == current {
			continue
		}
		fmt.Printf("%s: %s -> %s\n", key, current, latest)
		lines[index] = key + "=" + latest
		changed = true
	}

	if !changed {
		fmt.Println("All tool versions are current.")
		return
	}
	if !*write {
		os.Exit(1)
	}
	if err := os.WriteFile(*manifest, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		fatal(err)
	}
}

func latestVersion(client *http.Client, module string) (string, error) {
	url := "https://proxy.golang.org/" + escapeModule(module) + "/@latest"
	response, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("go proxy returned %s", response.Status)
	}
	var info struct {
		Version string `json:"Version"`
	}
	if err := json.NewDecoder(bufio.NewReader(response.Body)).Decode(&info); err != nil {
		return "", err
	}
	if info.Version == "" {
		return "", fmt.Errorf("go proxy returned an empty version")
	}
	return info.Version, nil
}

func escapeModule(module string) string {
	var escaped strings.Builder
	for _, char := range module {
		if char >= 'A' && char <= 'Z' {
			escaped.WriteByte('!')
			escaped.WriteRune(char + ('a' - 'A'))
		} else {
			escaped.WriteRune(char)
		}
	}
	return escaped.String()
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
