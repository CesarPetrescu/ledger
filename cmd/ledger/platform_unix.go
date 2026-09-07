//go:build !windows

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

const executableSuffix = ""

func privateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errors.New("credential directory must be a real directory with mode 0700")
	}
	return nil
}

func withLock(ctx context.Context, path string, fn func() error) error {
	f, err := openNoFollow(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK {
			return err
		}
		if err = waitFor(ctx, 25*time.Millisecond); err != nil {
			return errors.New("credentials busy; retry shortly")
		}
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

func openNoFollow(path string, flag int, mode os.FileMode) (*os.File, error) {
	return os.OpenFile(path, flag|syscall.O_NOFOLLOW, mode)
}

func checkPrivate(f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0077 != 0 {
		return errors.New("credentials must have mode 0600")
	}
	return nil
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func replaceFile(from, to string) error       { return os.Rename(from, to) }
func replaceExecutable(from, to string) error { return replaceFile(from, to) }
func detach(cmd *exec.Cmd)                    { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
func shellQuote(s string) string              { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func configDir() (string, error)              { return os.UserConfigDir() }
func headerHelper(exe, profile, dir string) string {
	return shellQuote(exe) + " auth headers --profile " + shellQuote(profile) + " --credential-dir " + shellQuote(dir)
}
func codexCommand(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, "codex", args...)
}

func privateTemp(dir, pattern string) (*os.File, error) { return os.CreateTemp(dir, pattern) }
