package syncer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInstallAndUninstallLaunchdServiceNoLoad(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)

	plistPath, err := InstallLaunchdService(ServiceConfig{
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

	wantPath := filepath.Join(root, "Library", "LaunchAgents", "dev.example.xtap-sync.plist")
	if plistPath != wantPath {
		t.Fatalf("plistPath = %q, want %q", plistPath, wantPath)
	}

	content, err := os.ReadFile(plistPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "<integer>900</integer>") {
		t.Fatalf("plist missing interval: %s", content)
	}
	if _, err := os.Stat(filepath.Join(root, "Library", "Logs", "xtap-sync")); err != nil {
		t.Fatalf("log directory missing: %v", err)
	}

	removed, err := UninstallLaunchdService("dev.example.xtap-sync")
	if err != nil {
		t.Fatal(err)
	}
	if removed != wantPath {
		t.Fatalf("removed = %q, want %q", removed, wantPath)
	}
	if _, err := os.Stat(wantPath); !os.IsNotExist(err) {
		t.Fatalf("plist should be removed, err=%v", err)
	}
}

func TestInstallLaunchdServiceDefaultsInterval(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)

	plistPath, err := InstallLaunchdService(ServiceConfig{
		Label:       "dev.example.xtap-sync-default",
		BinaryPath:  "/tmp/bin/xtap-sync",
		SourceDir:   "/tmp/source",
		RepoDir:     "/tmp/repo",
		LoadService: false,
	})
	if err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(plistPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "<integer>3600</integer>") {
		t.Fatalf("plist missing default interval: %s", content)
	}
}

func TestInstallLaunchdServiceLoadsWithLaunchctl(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	logPath := filepath.Join(root, "launchctl.log")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	launchctlPath := filepath.Join(binDir, "launchctl")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + logPath + "\n"
	if err := os.WriteFile(launchctlPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", root)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	plistPath, err := InstallLaunchdService(ServiceConfig{
		Label:       "dev.example.xtap-sync-load",
		BinaryPath:  "/tmp/bin/xtap-sync",
		SourceDir:   "/tmp/source",
		RepoDir:     "/tmp/repo",
		LoadService: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	log := readFile(t, logPath)
	for _, want := range []string{
		"bootout gui/",
		"bootstrap gui/",
		plistPath,
		"kickstart -k gui/",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("launchctl log missing %q:\n%s", want, log)
		}
	}
}

func TestInstallLaunchdServiceValidatesFields(t *testing.T) {
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

			_, err := InstallLaunchdService(tt.cfg)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestLaunchdPlistEscapesAndIncludesServiceArguments(t *testing.T) {
	t.Parallel()

	plist := launchdPlist(ServiceConfig{
		Label:      "dev.example.xtap-sync",
		BinaryPath: "/tmp/bin/xtap-sync",
		SourceDir:  "/tmp/source & data",
		RepoDir:    "/tmp/repo",
		Remote:     "upstream",
		NoPush:     true,
		Interval:   2 * time.Hour,
	}, "/tmp/logs")

	for _, want := range []string{
		"<string>dev.example.xtap-sync</string>",
		"<string>service-run</string>",
		"<string>--source</string>",
		"<string>/tmp/source &amp; data</string>",
		"<string>--repo</string>",
		"<string>/tmp/repo</string>",
		"<string>--remote</string>",
		"<string>upstream</string>",
		"<string>--no-push</string>",
		"<integer>7200</integer>",
		"<string>/tmp/logs/sync.out.log</string>",
		"<string>/tmp/logs/sync.err.log</string>",
	} {
		if !strings.Contains(plist, want) {
			t.Fatalf("plist missing %q:\n%s", want, plist)
		}
	}
}

func TestLaunchdPlistUsesConfigWhenProvided(t *testing.T) {
	t.Parallel()

	plist := launchdPlist(ServiceConfig{
		Label:      "dev.example.xtap-sync",
		BinaryPath: "/tmp/bin/xtap-sync",
		ConfigPath: "/tmp/config.json",
		SourceDir:  "/tmp/source",
		RepoDir:    "/tmp/repo",
	}, "/tmp/logs")

	for _, want := range []string{
		"<string>--config</string>",
		"<string>/tmp/config.json</string>",
	} {
		if !strings.Contains(plist, want) {
			t.Fatalf("plist missing %q:\n%s", want, plist)
		}
	}
	if strings.Contains(plist, "<string>--source</string>") {
		t.Fatalf("config service should not bake source flag:\n%s", plist)
	}
}

func TestLaunchdPlistDefaultsInvalidInterval(t *testing.T) {
	t.Parallel()

	plist := launchdPlist(ServiceConfig{
		Label:      "dev.example.xtap-sync",
		BinaryPath: "/tmp/bin/xtap-sync",
		SourceDir:  "/tmp/source",
		RepoDir:    "/tmp/repo",
	}, "/tmp/logs")

	if !strings.Contains(plist, "<integer>3600</integer>") {
		t.Fatalf("plist should default to hourly interval:\n%s", plist)
	}
}

func TestServiceIntervalHelpers(t *testing.T) {
	t.Parallel()

	if got := withDefaultInterval(ServiceConfig{}).Interval; got != time.Hour {
		t.Fatalf("default interval = %s, want 1h", got)
	}
	if got := withDefaultInterval(ServiceConfig{Interval: time.Nanosecond}).Interval; got != time.Nanosecond {
		t.Fatalf("positive interval changed to %s", got)
	}
	if got := launchdIntervalSeconds(time.Second); got != 1 {
		t.Fatalf("launchdIntervalSeconds(1s) = %d, want 1", got)
	}
	if got := launchdIntervalSeconds(0); got != 3600 {
		t.Fatalf("launchdIntervalSeconds(0) = %d, want 3600", got)
	}
}
