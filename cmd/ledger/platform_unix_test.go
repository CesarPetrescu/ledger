//go:build !windows

package main

import (
	"os"
	"os/exec"
	"testing"
)

// helperCommand runs a recorded helper through the shell, as Codex does.
func helperCommand(t *testing.T, helper string) *exec.Cmd {
	t.Helper()
	return exec.Command("sh", "-c", helper)
}

func makeCredentialsPublic(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
}
