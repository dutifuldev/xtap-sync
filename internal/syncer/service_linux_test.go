package syncer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInstallAndUninstallSystemdUserServiceNoLoad(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, ".config"))

	servicePath, err := InstallSystemdUserService(ServiceConfig{
		Label:       "dev.example.xtap-sync",
		BinaryPath:  "/tmp/bin/xtap-sync",
		SourceDir:   "/tmp/source",
		RepoDir:     "/tmp/repo",
		Interval:    15 * time.Minute,
		LoadService: false,
	})
	if err != nil {
		t.Fatal(err)
	}

	unitDir := filepath.Join(root, ".config", "systemd", "user")
	wantService := filepath.Join(unitDir, "dev.example.xtap-sync.service")
	wantTimer := filepath.Join(unitDir, "dev.example.xtap-sync.timer")
	if servicePath != wantService {
		t.Fatalf("servicePath = %q, want %q", servicePath, wantService)
	}

	service := readFile(t, wantService)
	timer := readFile(t, wantTimer)
	assertContainsAll(t, service, []string{
		`ExecStart="/tmp/bin/xtap-sync" "service-run"`,
		`"--source" "/tmp/source"`,
		`"--repo" "/tmp/repo"`,
		`WorkingDirectory=/tmp/repo`,
	})
	assertContainsAll(t, timer, []string{
		"OnStartupSec=1min",
		"OnUnitActiveSec=900s",
		"Persistent=true",
		"Unit=dev.example.xtap-sync.service",
	})

	removed, err := UninstallSystemdUserService("dev.example.xtap-sync")
	if err != nil {
		t.Fatal(err)
	}
	if removed != wantService {
		t.Fatalf("removed = %q, want %q", removed, wantService)
	}
	if _, err := os.Stat(wantService); !os.IsNotExist(err) {
		t.Fatalf("service unit should be removed, err=%v", err)
	}
	if _, err := os.Stat(wantTimer); !os.IsNotExist(err) {
		t.Fatalf("timer unit should be removed, err=%v", err)
	}
}

func assertContainsAll(t *testing.T, content string, wants []string) {
	t.Helper()

	for _, want := range wants {
		if !strings.Contains(content, want) {
			t.Fatalf("content missing %q:\n%s", want, content)
		}
	}
}

func TestInstallSystemdUserServiceLoadsWithSystemctl(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	logPath := filepath.Join(root, "systemctl.log")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	systemctlPath := filepath.Join(binDir, "systemctl")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + logPath + "\n"
	if err := os.WriteFile(systemctlPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, ".config"))
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := InstallSystemdUserService(ServiceConfig{
		Label:       "dev.example.xtap-sync-load",
		BinaryPath:  "/tmp/bin/xtap-sync",
		SourceDir:   "/tmp/source",
		RepoDir:     "/tmp/repo",
		LoadService: true,
	}); err != nil {
		t.Fatal(err)
	}

	log := readFile(t, logPath)
	for _, want := range []string{
		"--user daemon-reload",
		"--user enable --now dev.example.xtap-sync-load.timer",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("systemctl log missing %q:\n%s", want, log)
		}
	}
}

func TestInstallSystemdUserServiceValidatesFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  ServiceConfig
		want string
	}{
		{name: "label", cfg: ServiceConfig{}, want: "label"},
		{name: "binary", cfg: ServiceConfig{Label: "x"}, want: "binary"},
		{name: "source", cfg: ServiceConfig{Label: "x", BinaryPath: "/bin/x"}, want: "source"},
		{name: "repo", cfg: ServiceConfig{Label: "x", BinaryPath: "/bin/x", SourceDir: "/source"}, want: "repo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := InstallSystemdUserService(tt.cfg)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestSystemdServiceUnitUsesConfigWhenProvided(t *testing.T) {
	t.Parallel()

	unit := systemdServiceUnit(ServiceConfig{
		Label:      "dev.example.xtap-sync",
		BinaryPath: "/tmp/bin/xtap-sync",
		ConfigPath: "/tmp/config.json",
		SourceDir:  "/tmp/source",
		RepoDir:    "/tmp/repo",
	})

	if !strings.Contains(unit, `"--config" "/tmp/config.json"`) {
		t.Fatalf("service unit missing config:\n%s", unit)
	}
	if strings.Contains(unit, `"--source"`) {
		t.Fatalf("config service should not bake source flag:\n%s", unit)
	}
}

func TestSystemdQuoteEscapesUnitValues(t *testing.T) {
	t.Parallel()

	got := systemdServiceUnit(ServiceConfig{
		Label:      "dev.example.xtap-sync",
		BinaryPath: `/tmp/bin/xtap"sync`,
		SourceDir:  `/tmp/source 100%`,
		RepoDir:    `/tmp/repo\path`,
	})

	for _, want := range []string{
		`"/tmp/bin/xtap\"sync"`,
		`"/tmp/source 100%%"`,
		`WorkingDirectory=/tmp/repo\\path`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("service unit missing escaped value %q:\n%s", want, got)
		}
	}
}

func TestSystemdIntervalDefaultsInvalidInterval(t *testing.T) {
	t.Parallel()

	if got := systemdInterval(time.Second); got != "1s" {
		t.Fatalf("systemdInterval(1s) = %q, want 1s", got)
	}
	if got := systemdInterval(0); got != "3600s" {
		t.Fatalf("systemdInterval(0) = %q, want 3600s", got)
	}
}
