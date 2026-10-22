package toolversions

import (
	_ "embed"
	"fmt"
	"strings"
)

//go:embed versions.env
var raw string

type Tool struct {
	Name    string
	Version string
}

func All() ([]Tool, error) {
	values := make(map[string]string)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" || value == "" {
			return nil, fmt.Errorf("invalid tool version entry %q", line)
		}
		values[key] = value
	}

	tools := []Tool{
		{Name: "Go build track", Version: values["GO_TRACK"]},
		{Name: "dlv", Version: values["DLV_VERSION"]},
		{Name: "gopls", Version: values["GOPLS_VERSION"]},
		{Name: "golangci-lint", Version: values["GOLANGCI_LINT_VERSION"]},
		{Name: "govulncheck", Version: values["GOVULNCHECK_VERSION"]},
		{Name: "gotests", Version: values["GOTESTS_VERSION"]},
		{Name: "gofumpt", Version: values["GOFUMPT_VERSION"]},
	}
	for _, tool := range tools {
		if tool.Version == "" {
			return nil, fmt.Errorf("missing version for %s", tool.Name)
		}
	}
	return tools, nil
}
