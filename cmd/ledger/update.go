package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const releaseAPI = "https://api.github.com/repos/CesarPetrescu/ledger/releases/latest"
const releasePrefix = "https://github.com/CesarPetrescu/ledger/releases/download/"

type release struct {
	Tag        string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name   string `json:"name"`
		URL    string `json:"browser_download_url"`
		Digest string `json:"digest"`
		Size   int64  `json:"size"`
	} `json:"assets"`
}

func startAutoUpdate() {
	if os.Getenv("LEDGER_AUTO_UPDATE") == "0" || version == "dev" {
		return
	}
	marker, err := updateMarker()
	if err != nil {
		return
	}
	if info, err := os.Stat(marker); err == nil && time.Since(info.ModTime()) < 24*time.Hour {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	log, err := openNoFollow(filepath.Join(filepath.Dir(marker), "update.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return
	}
	defer log.Close()
	cmd := exec.Command(exe, "update", "--automatic")
	cmd.Stdout = log
	cmd.Stderr = log
	detach(cmd)
	if cmd.Start() == nil {
		_ = cmd.Process.Release()
	}
}

func update(ctx context.Context, automatic bool) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	marker, err := updateMarker()
	if err != nil {
		return err
	}
	return withLock(ctx, marker+".lock", func() error {
		if automatic {
			if info, err := os.Stat(marker); err == nil && time.Since(info.ModTime()) < 24*time.Hour {
				return nil
			}
		}
		if err := atomicWrite(marker, []byte(time.Now().Format(time.RFC3339)), 0600); err != nil {
			return err
		}
		client := &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 5 || req.URL.Scheme != "https" || req.URL.User != nil {
				return errors.New("unsafe release redirect")
			}
			host := req.URL.Hostname()
			if host != "github.com" && host != "release-assets.githubusercontent.com" && host != "objects.githubusercontent.com" {
				return errors.New("unexpected release download host")
			}
			return nil
		}}
		req, err := http.NewRequestWithContext(ctx, "GET", releaseAPI, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		res, err := client.Do(req)
		if err != nil {
			return errors.New("could not check GitHub releases")
		}
		var r release
		err = decodeResponse(res, &r)
		res.Body.Close()
		if err != nil {
			return err
		}
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		exe, err = filepath.EvalSymlinks(exe)
		if err != nil {
			return err
		}
		return installRelease(ctx, client, exe, r)
	})
}

func selectAsset(r release, current, platform, arch string) (string, string, int64, error) {
	target, err := releaseVersion(r.Tag)
	if err != nil || r.Draft || r.Prerelease {
		return "", "", 0, errors.New("latest release is not a stable version")
	}
	if current != "dev" {
		installed, err := releaseVersion(current)
		if err != nil {
			return "", "", 0, err
		}
		newer := false
		for i := range target {
			if target[i] != installed[i] {
				newer = target[i] > installed[i]
				break
			}
		}
		if !newer {
			return "", "", 0, nil
		}
	}
	name := "ledger_" + platform + "_" + arch
	if platform == "windows" {
		name += ".exe"
	}
	for _, a := range r.Assets {
		if a.Name != name {
			continue
		}
		expected := releasePrefix + url.PathEscape(r.Tag) + "/" + a.Name
		digest, err := hex.DecodeString(strings.TrimPrefix(a.Digest, "sha256:"))
		if a.URL != expected || !strings.HasPrefix(a.Digest, "sha256:") || err != nil || len(digest) != 32 || a.Size <= 0 || a.Size > 32<<20 {
			return "", "", 0, errors.New("release asset URL, SHA-256 digest, or size is invalid")
		}
		return a.URL, a.Digest, a.Size, nil
	}
	return "", "", 0, errors.New("release has no binary for this platform")
}

func verifyAsset(body []byte, digest string, size int64) error {
	sum := sha256.Sum256(body)
	if int64(len(body)) != size || "sha256:"+hex.EncodeToString(sum[:]) != digest {
		return errors.New("release integrity check failed; installed binary preserved")
	}
	return nil
}

func releaseVersion(raw string) ([3]uint64, error) {
	var v [3]uint64
	parts := strings.Split(strings.TrimPrefix(raw, "v"), ".")
	if !strings.HasPrefix(raw, "v") || len(parts) != 3 {
		return v, errors.New("expected a stable vMAJOR.MINOR.PATCH release")
	}
	for i, s := range parts {
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil || strconv.FormatUint(n, 10) != s {
			return v, errors.New("invalid release version")
		}
		v[i] = n
	}
	return v, nil
}

func installRelease(ctx context.Context, client *http.Client, exe string, r release) error {
	// Another process may have upgraded the installed file since this process started.
	current, err := exec.CommandContext(ctx, exe, "version").Output()
	if err != nil {
		return errors.New("could not read installed version")
	}
	assetURL, digest, size, err := selectAsset(r, strings.TrimSpace(string(current)), runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	if assetURL == "" {
		fmt.Fprintln(os.Stderr, "Ledger is up to date.")
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, "GET", assetURL, nil)
	if err != nil {
		return err
	}
	res, err := client.Do(req)
	if err != nil {
		return errors.New("could not download release")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("release download returned HTTP %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, size+1))
	if err != nil {
		return err
	}
	if err = verifyAsset(body, digest, size); err != nil {
		return err
	}
	// Validate the executable before replacing the working installation.
	candidate, err := privateTemp(filepath.Dir(exe), ".ledger-release-*"+executableSuffix)
	if err != nil {
		return err
	}
	candidatePath := candidate.Name()
	defer os.Remove(candidatePath)
	if _, err = candidate.Write(body); err != nil {
		candidate.Close()
		return err
	}
	if err = candidate.Chmod(0755); err != nil {
		candidate.Close()
		return err
	}
	if err = candidate.Sync(); err != nil {
		candidate.Close()
		return err
	}
	if err = candidate.Close(); err != nil {
		return err
	}
	output, err := exec.CommandContext(ctx, candidatePath, "version").Output()
	if err != nil || strings.TrimSpace(string(output)) != r.Tag {
		return errors.New("downloaded executable failed its version check")
	}
	if err = replaceExecutable(candidatePath, exe); err != nil {
		return err
	}
	if err = syncDirectory(filepath.Dir(exe)); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "Updated Ledger to", r.Tag)
	return nil
}

func updateMarker() (string, error) {
	dir, err := credentialDir("")
	return filepath.Join(dir, ".update-check"), err
}
