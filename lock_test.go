package main

import (
	"path/filepath"
	"testing"
)

func TestWithinRejectsSibling(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "parent")
	if within(parent, filepath.Join(parent, "..", "sibling", "secret")) {
		t.Fatal("sibling accepted")
	}
	if !within(parent, filepath.Join(parent, "child")) {
		t.Fatal("child rejected")
	}
}
