package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App is the Wails-bound backend: it manages the Ookla CLI and fans tests out.
type App struct {
	ctx     context.Context
	mu      sync.Mutex
	running bool
	cmds    []*exec.Cmd
}

func NewApp() *App { return &App{} }

func (a *App) startup(ctx context.Context) { a.ctx = ctx }

// ---- Ookla CLI management ---------------------------------------------------

const ooklaVersion = "1.2.0"

func cliDownloadURL() (string, error) {
	base := "https://install.speedtest.net/app/cli/ookla-speedtest-" + ooklaVersion
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

func cliDir() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cfg, "aggregate-speedtest")
	return dir, os.MkdirAll(dir, 0o755)
}

func cliPath() (string, error) {
	dir, err := cliDir()
	if err != nil {
		return "", err
	}
	name := "speedtest"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(dir, name), nil
}

// CLIStatus reports whether the Ookla CLI is present and its version.
func (a *App) CLIStatus() map[string]any {
	p, err := cliPath()
	if err != nil {
		return map[string]any{"present": false, "error": err.Error()}
	}
	cmd := exec.Command(p, "--version")
	hideConsole(cmd)
	out, err := cmd.Output()
	if err != nil {
		return map[string]any{"present": false}
	}
	version, _, _ := strings.Cut(string(out), "\n")
	return map[string]any{"present": true, "version": version, "path": p}
}

// InstallCLI downloads and extracts the Ookla CLI. The caller must have shown
// the user Ookla's license terms first — running the CLI passes
// --accept-license/--accept-gdpr on their behalf.
func (a *App) InstallCLI() (map[string]any, error) {
	url, err := cliDownloadURL()
	if err != nil {
		return nil, err
	}
	dir, err := cliDir()
	if err != nil {
		return nil, err
	}
	archive := filepath.Join(dir, filepath.Base(url))
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("download: HTTP %d from %s", resp.StatusCode, url)
	}
	f, err := os.Create(archive)
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return nil, err
	}
	f.Close()
	defer os.Remove(archive)

	// tar handles both .tgz and .zip on every OS this app targets
	// (bsdtar on macOS/Windows 10+, GNU tar on Linux).
	member := "speedtest"
	if runtime.GOOS == "windows" {
		member = "speedtest.exe"
	}
	tarCmd := exec.Command("tar", "-xf", archive, "-C", dir, member)
	hideConsole(tarCmd)
	if out, err := tarCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("extract: %v: %s", err, out)
	}
	p, _ := cliPath()
	_ = os.Chmod(p, 0o755)
	return a.CLIStatus(), nil
}

// ---- Server discovery -------------------------------------------------------

type Server struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Location string `json:"location"`
	Country  string `json:"country"`
}

// NearbyServers asks the CLI for the closest test servers.
func (a *App) NearbyServers() ([]Server, error) {
	p, err := cliPath()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(p, "-L", "-f", "json", "--accept-license", "--accept-gdpr")
	hideConsole(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("server list: %w", err)
	}
	var parsed struct {
		Servers []Server `json:"servers"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, fmt.Errorf("server list parse: %w", err)
	}
	return parsed.Servers, nil
}

// ---- Test execution ---------------------------------------------------------

// Run launches one Ookla test per server id, all in parallel, and streams
// progress to the frontend as "leg" events. Emits "run:done" when every leg
// finishes. The aggregate is the frontend's job — it's just a sum.
func (a *App) Run(ids []int) error {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("a run is already in progress")
	}
	if len(ids) == 0 {
		a.mu.Unlock()
		return fmt.Errorf("no servers selected")
	}
	a.running = true
	a.cmds = nil
	a.mu.Unlock()

	p, err := cliPath()
	if err != nil {
		a.mu.Lock()
		a.running = false
		a.mu.Unlock()
		return err
	}

	var wg sync.WaitGroup
	for _, id := range ids {
		cmd := exec.Command(p, "-s", strconv.Itoa(id), "-f", "jsonl",
			"--accept-license", "--accept-gdpr")
		hideConsole(cmd)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			continue
		}
		cmd.Stderr = nil
		if err := cmd.Start(); err != nil {
			wruntime.EventsEmit(a.ctx, "leg", map[string]any{
				"id": id, "type": "error", "message": err.Error(),
			})
			continue
		}
		a.mu.Lock()
		a.cmds = append(a.cmds, cmd)
		a.mu.Unlock()

		wg.Add(1)
		go func(id int, cmd *exec.Cmd, stdout io.ReadCloser) {
			defer wg.Done()
			sc := bufio.NewScanner(stdout)
			sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
			for sc.Scan() {
				var ev map[string]any
				if json.Unmarshal(sc.Bytes(), &ev) != nil {
					continue
				}
				ev["id"] = id
				wruntime.EventsEmit(a.ctx, "leg", ev)
			}
			if err := cmd.Wait(); err != nil {
				wruntime.EventsEmit(a.ctx, "leg", map[string]any{
					"id": id, "type": "error", "message": err.Error(),
				})
			}
		}(id, cmd, stdout)
	}

	go func() {
		wg.Wait()
		a.mu.Lock()
		a.running = false
		a.cmds = nil
		a.mu.Unlock()
		wruntime.EventsEmit(a.ctx, "run:done", nil)
	}()
	return nil
}

// Stop kills every in-flight test.
func (a *App) Stop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, c := range a.cmds {
		if c.Process != nil {
			_ = c.Process.Kill()
		}
	}
	a.running = false
}
