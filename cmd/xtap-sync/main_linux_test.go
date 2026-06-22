package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunInstallAndUninstallServiceNoStart(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, ".config"))

	repo := filepath.Join(root, "repo")
	source := filepath.Join(root, "source")
	mustMkdir(t, repo)
	mustMkdir(t, source)

	if err := run(context.Background(), []string{
		"xtap-sync", "install-service",
		"--source", source,
		"--repo", repo,
		"--interval", "30m",
		"--label", "dev.test.xtap-sync",
		"--no-start",
	}); err != nil {
		t.Fatal(err)
	}

	unitDir := filepath.Join(root, ".config", "systemd", "user")
	service := filepath.Join(unitDir, "dev.test.xtap-sync.service")
	timer := filepath.Join(unitDir, "dev.test.xtap-sync.timer")
	serviceContent := readFile(t, service)
	timerContent := readFile(t, timer)
	for _, want := range []string{
		`WorkingDirectory=` + repo,
		`"--source" "` + source + `"`,
		`"--repo" "` + repo + `"`,
	} {
		if !strings.Contains(serviceContent, want) {
			t.Fatalf("service unit missing %q: %s", want, serviceContent)
		}
	}
	for _, want := range []string{
		"OnUnitActiveSec=1800s",
		"Unit=dev.test.xtap-sync.service",
		"WantedBy=timers.target",
	} {
		if !strings.Contains(timerContent, want) {
			t.Fatalf("timer unit missing %q: %s", want, timerContent)
		}
	}

	if err := run(context.Background(), []string{
		"xtap-sync", "uninstall-service",
		"--label", "dev.test.xtap-sync",
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(service); !os.IsNotExist(err) {
		t.Fatalf("service unit should be removed, err=%v", err)
	}
	if _, err := os.Stat(timer); !os.IsNotExist(err) {
		t.Fatalf("timer unit should be removed, err=%v", err)
	}
}

func TestRunInstallServiceUsesConfigFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, ".config"))

	repo := filepath.Join(root, "repo")
	source := filepath.Join(root, "source")
	config := filepath.Join(root, ".config", "xtap-sync", "config.json")
	mustMkdir(t, repo)
	mustMkdir(t, source)
	writeJSONConfig(t, config, map[string]any{
		"source_dir":    filepath.ToSlash(source),
		"repo_dir":      filepath.ToSlash(repo),
		"push":          false,
		"interval":      "45m",
		"service_label": "dev.test.xtap-sync-config",
	})

	if err := run(context.Background(), []string{
		"xtap-sync", "install-service",
		"--config", config,
		"--no-start",
	}); err != nil {
		t.Fatal(err)
	}

	service := filepath.Join(root, ".config", "systemd", "user", "dev.test.xtap-sync-config.service")
	timer := filepath.Join(root, ".config", "systemd", "user", "dev.test.xtap-sync-config.timer")
	serviceContent := readFile(t, service)
	timerContent := readFile(t, timer)
	for _, want := range []string{
		`"--config" "` + config + `"`,
	} {
		if !strings.Contains(serviceContent, want) {
			t.Fatalf("service unit missing %q: %s", want, serviceContent)
		}
	}
	if strings.Contains(serviceContent, `"--source"`) {
		t.Fatalf("config service should not bake source flag:\n%s", serviceContent)
	}
	if !strings.Contains(timerContent, "OnUnitActiveSec=2700s") {
		t.Fatalf("timer unit missing interval: %s", timerContent)
	}
}
