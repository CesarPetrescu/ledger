//go:build !windows

package main

import (
	"os"
	"testing"
)

func makeCredentialsPublic(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
}
