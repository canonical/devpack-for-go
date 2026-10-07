package editor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tailscale/hujson"
)

type jsonEdit struct {
	Pointer       string          `json:"pointer"`
	BeforePresent bool            `json:"before_present"`
	Before        json.RawMessage `json:"before,omitempty"`
	Applied       json.RawMessage `json:"applied"`
	Container     bool            `json:"container,omitempty"`
}

type desiredJSON struct {
	Pointer string
	Value   any
}

func updateJSONC(input []byte, desired []desiredJSON) ([]byte, []jsonEdit, error) {
	bom := []byte(nil)
	if bytes.HasPrefix(input, []byte{0xef, 0xbb, 0xbf}) {
		bom = []byte{0xef, 0xbb, 0xbf}
		input = input[3:]
	}
	if len(bytes.TrimSpace(input)) == 0 {
		input = []byte("{}\n")
	}

	value, err := hujson.Parse(input)
	if err != nil {
		return nil, nil, fmt.Errorf("parse JSONC: %w", err)
	}
	if _, ok := value.Value.(*hujson.Object); !ok {
		return nil, nil, fmt.Errorf("settings root must be an object")
	}

	var edits []jsonEdit
	created := make(map[string]bool)
	for _, item := range desired {
		segments, err := pointerSegments(item.Pointer)
		if err != nil || len(segments) == 0 {
			return nil, nil, fmt.Errorf("invalid JSON pointer %q", item.Pointer)
		}

		for i := 1; i < len(segments); i++ {
			parentPointer := joinPointer(segments[:i])
			node, duplicates, err := findUnique(&value, segments[:i])
			if err != nil {
				return nil, nil, err
			}
			if duplicates {
				return nil, nil, fmt.Errorf("duplicate key at %s", parentPointer)
			}
			if node == nil {
				if err := applyJSONPatch(&value, "add", parentPointer, json.RawMessage(`{}`)); err != nil {
					return nil, nil, err
				}
				if !created[parentPointer] {
					edits = append(edits, jsonEdit{
						Pointer:   parentPointer,
						Applied:   json.RawMessage(`{}`),
						Container: true,
					})
					created[parentPointer] = true
				}
				continue
			}
			if _, ok := node.Value.(*hujson.Object); !ok {
				return nil, nil, fmt.Errorf("%s must be an object", parentPointer)
			}
		}

		node, duplicates, err := findUnique(&value, segments)
		if err != nil {
			return nil, nil, err
		}
		if duplicates {
			return nil, nil, fmt.Errorf("duplicate key at %s", item.Pointer)
		}
		applied, err := json.Marshal(item.Value)
		if err != nil {
			return nil, nil, err
		}
		edit := jsonEdit{Pointer: item.Pointer, Applied: applied}
		op := "add"
		if node != nil {
			before, err := normalizedJSON(node)
			if err != nil {
				return nil, nil, err
			}
			if bytes.Equal(before, applied) {
				continue
			}
			edit.BeforePresent = true
			edit.Before = before
			op = "replace"
		}
		if err := applyJSONPatch(&value, op, item.Pointer, applied); err != nil {
			return nil, nil, err
		}
		edits = append(edits, edit)
	}

	return append(bom, value.Pack()...), edits, nil
}

func revertJSONC(input []byte, edits []jsonEdit) ([]byte, error) {
	bom := []byte(nil)
	if bytes.HasPrefix(input, []byte{0xef, 0xbb, 0xbf}) {
		bom = []byte{0xef, 0xbb, 0xbf}
		input = input[3:]
	}
	value, err := hujson.Parse(input)
	if err != nil {
		return nil, fmt.Errorf("parse JSONC: %w", err)
	}

	for i := len(edits) - 1; i >= 0; i-- {
		edit := edits[i]
		segments, _ := pointerSegments(edit.Pointer)
		node, duplicates, err := findUnique(&value, segments)
		if err != nil {
			return nil, err
		}
		if duplicates {
			return nil, fmt.Errorf("duplicate key at %s", edit.Pointer)
		}
		if node == nil {
			continue
		}
		if edit.Container {
			object, ok := node.Value.(*hujson.Object)
			if !ok || len(object.Members) != 0 {
				continue
			}
			if err := applyJSONPatch(&value, "remove", edit.Pointer, nil); err != nil {
				return nil, err
			}
			continue
		}
		current, err := normalizedJSON(node)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(current, edit.Applied) {
			return nil, fmt.Errorf("owned setting %s was changed by the user", edit.Pointer)
		}
		if edit.BeforePresent {
			if err := applyJSONPatch(&value, "replace", edit.Pointer, edit.Before); err != nil {
				return nil, err
			}
		} else if err := applyJSONPatch(&value, "remove", edit.Pointer, nil); err != nil {
			return nil, err
		}
	}

	return append(bom, value.Pack()...), nil
}

func jsonEditsHealthy(input []byte, edits []jsonEdit) bool {
	if bytes.HasPrefix(input, []byte{0xef, 0xbb, 0xbf}) {
		input = input[3:]
	}
	value, err := hujson.Parse(input)
	if err != nil {
		return false
	}
	for _, edit := range edits {
		if edit.Container {
			continue
		}
		segments, _ := pointerSegments(edit.Pointer)
		node, duplicates, err := findUnique(&value, segments)
		if err != nil || duplicates || node == nil {
			return false
		}
		current, err := normalizedJSON(node)
		if err != nil || !bytes.Equal(current, edit.Applied) {
			return false
		}
	}
	return true
}

func applyJSONPatch(value *hujson.Value, operation, pointer string, raw json.RawMessage) error {
	entry := map[string]any{"op": operation, "path": pointer}
	if operation != "remove" {
		entry["value"] = raw
	}
	patch, err := json.Marshal([]map[string]any{entry})
	if err != nil {
		return err
	}
	if err := value.Patch(patch); err != nil {
		return fmt.Errorf("apply JSON patch at %s: %w", pointer, err)
	}
	return nil
}

func normalizedJSON(value *hujson.Value) ([]byte, error) {
	clone := value.Clone()
	clone.Standardize()
	var compact bytes.Buffer
	if err := json.Compact(&compact, clone.Pack()); err != nil {
		return nil, err
	}
	return compact.Bytes(), nil
}

func findUnique(root *hujson.Value, segments []string) (*hujson.Value, bool, error) {
	current := root
	for _, segment := range segments {
		object, ok := current.Value.(*hujson.Object)
		if !ok {
			return nil, false, nil
		}
		var found *hujson.Value
		for i := range object.Members {
			literal, ok := object.Members[i].Name.Value.(hujson.Literal)
			if !ok || literal.String() != segment {
				continue
			}
			if found != nil {
				return found, true, nil
			}
			found = &object.Members[i].Value
		}
		if found == nil {
			return nil, false, nil
		}
		current = found
	}
	return current, false, nil
}

func pointerSegments(pointer string) ([]string, error) {
	if pointer == "" {
		return nil, nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, fmt.Errorf("pointer must start with a slash")
	}
	parts := strings.Split(pointer[1:], "/")
	for i, part := range parts {
		part = strings.ReplaceAll(part, "~1", "/")
		part = strings.ReplaceAll(part, "~0", "~")
		parts[i] = part
	}
	return parts, nil
}

func joinPointer(segments []string) string {
	encoded := make([]string, len(segments))
	for i, segment := range segments {
		segment = strings.ReplaceAll(segment, "~", "~0")
		segment = strings.ReplaceAll(segment, "/", "~1")
		encoded[i] = segment
	}
	return "/" + strings.Join(encoded, "/")
}
