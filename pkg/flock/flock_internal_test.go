//go:build !js && !wasip1

package flock

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSameFileAtPath checks the identity comparison that the ephemeral
// gate uses. The gate must accept the locked file and reject every other
// file at the path.
func TestSameFileAtPath(t *testing.T) {
	dir := t.TempDir()
	pathA := filepath.Join(dir, "a.lock")
	pathB := filepath.Join(dir, "b.lock")
	pathMissing := filepath.Join(dir, "missing.lock")

	fhA, err := os.OpenFile(pathA, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open a: %v", err)
	}
	defer fhA.Close()

	fhB, err := os.OpenFile(pathB, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open b: %v", err)
	}
	defer fhB.Close()

	same, err := sameFileAtPath(pathA, fhA)
	if err != nil {
		t.Fatalf("sameFileAtPath(a): %v", err)
	}
	if !same {
		t.Error("sameFileAtPath(a) = false, want true")
	}

	same, err = sameFileAtPath(pathB, fhA)
	if err != nil {
		t.Fatalf("sameFileAtPath(b): %v", err)
	}
	if same {
		t.Error("sameFileAtPath(b) = true, want false")
	}

	same, err = sameFileAtPath(pathMissing, fhA)
	if err != nil {
		t.Fatalf("sameFileAtPath(missing): %v", err)
	}
	if same {
		t.Error("sameFileAtPath(missing) = true, want false")
	}
}
