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

	plist := filepath.Join(root, "Library", "LaunchAgents", "dev.test.xtap-sync.plist")
	content := readFile(t, plist)
	for _, want := range []string{
		"<integer>1800</integer>",
		"<string>--source</string>",
		"<string>" + source + "</string>",
		"<string>--repo</string>",
		"<string>" + repo + "</string>",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("plist missing %q: %s", want, content)
		}
	}

	if err := run(context.Background(), []string{
		"xtap-sync", "uninstall-service",
		"--label", "dev.test.xtap-sync",
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(plist); !os.IsNotExist(err) {
		t.Fatalf("plist should be removed, err=%v", err)
	}
}

func TestRunInstallServiceUsesConfigFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)

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

	plist := filepath.Join(root, "Library", "LaunchAgents", "dev.test.xtap-sync-config.plist")
	content := readFile(t, plist)
	for _, want := range []string{
		"<integer>2700</integer>",
		"<string>--config</string>",
		"<string>" + config + "</string>",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("plist missing %q: %s", want, content)
		}
	}
}
