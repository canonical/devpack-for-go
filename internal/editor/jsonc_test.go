package editor

import (
	"bytes"
	"testing"
)

func TestUpdateAndRevertJSONCPreservesContent(t *testing.T) {
	original := []byte("{\n  // Keep this comment.\n  \"editor.fontSize\": 14,\n}\n")
	updated, edits, err := updateJSONC(original, []desiredJSON{
		{Pointer: "/go.alternateTools/gopls", Value: "/snap/bin/devpack-for-go.gopls"},
		{Pointer: "/go.alternateTools/dlv", Value: "/snap/bin/devpack-for-go.dlv"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(updated, []byte("Keep this comment")) {
		t.Fatalf("comment was lost:\n%s", updated)
	}
	if !jsonEditsHealthy(updated, edits) {
		t.Fatalf("updated settings are not reported healthy:\n%s", updated)
	}

	reverted, err := revertJSONC(updated, edits)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(reverted, []byte("Keep this comment")) || !bytes.Contains(reverted, []byte(`"editor.fontSize": 14`)) {
		t.Fatalf("revert did not preserve original content:\n%s", reverted)
	}
	if bytes.Contains(reverted, []byte("go.alternateTools")) {
		t.Fatalf("revert left an owned setting:\n%s", reverted)
	}
}

func TestRevertJSONCPreservesUnrelatedChanges(t *testing.T) {
	original := []byte("{\n  \"editor.fontSize\": 14\n}\n")
	updated, edits, err := updateJSONC(original, []desiredJSON{
		{Pointer: "/go.lintTool", Value: "golangci-lint-v2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	withUserChange, _, err := updateJSONC(updated, []desiredJSON{
		{Pointer: "/editor.wordWrap", Value: "on"},
	})
	if err != nil {
		t.Fatal(err)
	}

	reverted, err := revertJSONC(withUserChange, edits)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(reverted, []byte("go.lintTool")) {
		t.Fatalf("owned setting was not removed:\n%s", reverted)
	}
	if !bytes.Contains(reverted, []byte("editor.wordWrap")) {
		t.Fatalf("unrelated setting was removed:\n%s", reverted)
	}
}

func TestRevertJSONCPreservesSiblingInCreatedContainer(t *testing.T) {
	updated, edits, err := updateJSONC([]byte("{}\n"), []desiredJSON{
		{Pointer: "/go.alternateTools/gopls", Value: "/snap/bin/devpack-for-go.gopls"},
	})
	if err != nil {
		t.Fatal(err)
	}
	withUserChange, _, err := updateJSONC(updated, []desiredJSON{
		{Pointer: "/go.alternateTools/go", Value: "/usr/local/bin/go"},
	})
	if err != nil {
		t.Fatal(err)
	}

	reverted, err := revertJSONC(withUserChange, edits)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(reverted, []byte("devpack-for-go.gopls")) {
		t.Fatalf("owned setting was not removed:\n%s", reverted)
	}
	if !bytes.Contains(reverted, []byte("/usr/local/bin/go")) {
		t.Fatalf("user-owned sibling was removed:\n%s", reverted)
	}
}

func TestUpdateJSONCRejectsDuplicateKeys(t *testing.T) {
	input := []byte(`{"go.alternateTools": {}, "go.alternateTools": {}}`)
	_, _, err := updateJSONC(input, []desiredJSON{
		{Pointer: "/go.alternateTools/gopls", Value: "gopls"},
	})
	if err == nil {
		t.Fatal("expected duplicate key error")
	}
}

func TestRevertJSONCRejectsChangedOwnedValue(t *testing.T) {
	updated, edits, err := updateJSONC([]byte("{}\n"), []desiredJSON{
		{Pointer: "/go.lintTool", Value: "golangci-lint-v2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	changed, _, err := updateJSONC(updated, []desiredJSON{
		{Pointer: "/go.lintTool", Value: "revive"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := revertJSONC(changed, edits); err == nil {
		t.Fatal("expected changed owned value to be rejected")
	}
}
