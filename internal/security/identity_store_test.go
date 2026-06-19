package security

import (
	"path/filepath"
	"testing"
)

func TestIdentityStorePathUsesKeystoreOverride(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GHOSTABLE_KEYSTORE", root)

	store, err := NewIdentityStore()
	if err != nil {
		t.Fatal(err)
	}

	expected := filepath.Join(root, "project_1.json")
	if path := store.Path("project/1"); path != expected {
		t.Fatalf("expected override path %s, got %s", expected, path)
	}
}
