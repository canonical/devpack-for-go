package toolversions

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestAllVersionsArePinned(t *testing.T) {
	tools, err := All()
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 7 {
		t.Fatalf("got %d entries, want 7", len(tools))
	}
	for _, tool := range tools {
		if tool.Version == "latest" || tool.Version == "" {
			t.Errorf("%s is not pinned: %q", tool.Name, tool.Version)
		}
	}
}

func TestGoTracksStayInSync(t *testing.T) {
	tools, err := All()
	if err != nil {
		t.Fatal(err)
	}
	current := strings.TrimSuffix(tools[0].Version, "/stable")
	parts := strings.Split(current, ".")
	if len(parts) != 2 {
		t.Fatalf("unexpected Go track %q", tools[0].Version)
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		t.Fatalf("parse Go track %q: %v", tools[0].Version, err)
	}
	previous := fmt.Sprintf("%s.%d", parts[0], minor-1)

	assertFileContains(t, filepath.Join("..", "..", "snapcraft.yaml"), "- go/"+tools[0].Version)
	assertFileContains(t, filepath.Join("..", "..", ".github", "workflows", "test.yaml"),
		"go-version: '"+current+".x'",
		"go-channel: ["+previous+"/stable, "+current+"/stable]",
	)
	assertFileContains(t, filepath.Join("..", "..", ".github", "workflows", "update-tools.yaml"),
		"go-version: '"+current+".x'",
		"--channel="+previous+"/stable",
		"--channel="+current+"/stable",
	)
}

func assertFileContains(t *testing.T, path string, values ...string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range values {
		if !strings.Contains(string(content), value) {
			t.Errorf("%s does not contain %q", path, value)
		}
	}
}
