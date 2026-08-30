// Package ookla locates, downloads, and manages the proprietary Speedtest CLI.
// The binary is never redistributed — it is fetched from Ookla's own servers,
// and running it accepts Ookla's EULA/GDPR terms on the user's behalf.
package ookla

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

const Version = "1.2.0"

func DownloadURL() (string, error) {
	base := "https://install.speedtest.net/app/cli/ookla-speedtest-" + Version
	switch runtime.GOOS {
	case "darwin":
		return base + "-macosx-universal.tgz", nil
	case "linux":
		switch runtime.GOARCH {
		case "amd64":
			return base + "-linux-x86_64.tgz", nil
		case "arm64":
			return base + "-linux-aarch64.tgz", nil
		}
	case "windows":
		if runtime.GOARCH == "amd64" {
			return base + "-win64.zip", nil
		}
	}
	return "", fmt.Errorf("no Ookla CLI build for %s/%s", runtime.GOOS, runtime.GOARCH)
}

func Dir() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cfg, "aggregate-speedtest")
	return dir, os.MkdirAll(dir, 0o755)
}

// Path returns where the CLI lives (or would live) for this user.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	name := "speedtest"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(dir, name), nil
}

// Find returns a usable CLI path: $PATH first (covers system installs),
// then the app's own config dir. Empty string if neither exists.
func Find() string {
	if p, err := exec.LookPath("speedtest"); err == nil {
		return p
	}
	p, err := Path()
	if err == nil {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// Install downloads and extracts the CLI into Dir().
func Install() (string, error) {
	url, err := DownloadURL()
	if err != nil {
		return "", err
	}
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	archive := filepath.Join(dir, filepath.Base(url))
	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download: HTTP %d from %s", resp.StatusCode, url)
	}
	f, err := os.Create(archive)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return "", err
	}
	f.Close()
	defer os.Remove(archive)

	// tar handles both .tgz and .zip on every OS this app targets
	// (bsdtar on macOS/Windows 10+, GNU tar on Linux).
	member := "speedtest"
	if runtime.GOOS == "windows" {
		member = "speedtest.exe"
	}
	cmd := exec.Command("tar", "-xf", archive, "-C", dir, member)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("extract: %v: %s", err, out)
	}
	p, _ := Path()
	_ = os.Chmod(p, 0o755)
	return p, nil
}
