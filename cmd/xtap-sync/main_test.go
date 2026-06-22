package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunHelp(t *testing.T) {
	t.Parallel()

	if err := run(context.Background(), []string{"xtap-sync", "help"}); err != nil {
		t.Fatal(err)
	}
}

func TestRunRejectsMissingCommand(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), []string{"xtap-sync"})
	if err == nil || !strings.Contains(err.Error(), "missing command") {
		t.Fatalf("err = %v, want missing command", err)
	}
}

func TestRunRejectsUnknownCommand(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), []string{"xtap-sync", "unknown"})
	if err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("err = %v, want unknown command", err)
	}
}

func TestRunSyncAndVerifyCommands(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := filepath.Join(root, "source")
	repo := filepath.Join(root, "repo")
	mustMkdir(t, source)
	mustMkdir(t, repo)
	writeFile(t, filepath.Join(source, "tweets-2026-06-18.jsonl"), `{"id":"1","captured_at":"2026-06-18T10:00:00.000Z"}`+"\n")
	initGitRepo(t, repo)

	if err := run(context.Background(), []string{
		"xtap-sync", "sync",
		"--source", source,
		"--repo", repo,
		"--no-push",
	}); err != nil {
		t.Fatal(err)
	}

	if err := run(context.Background(), []string{
		"xtap-sync", "verify",
		"--source", source,
		"--repo", repo,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRunServiceRunCommand(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := filepath.Join(root, "source")
	repo := filepath.Join(root, "repo")
	mustMkdir(t, source)
	mustMkdir(t, repo)
	writeFile(t, filepath.Join(source, "tweets-2026-06-18.jsonl"), `{"id":"1","captured_at":"2026-06-18T10:00:00.000Z"}`+"\n")
	initGitRepo(t, repo)

	if err := run(context.Background(), []string{
		"xtap-sync", "service-run",
		"--source", source,
		"--repo", repo,
		"--no-push",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRunSyncAndVerifyUseConfigFile(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	repo := filepath.Join(root, "repo")
	config := filepath.Join(root, "config.json")
	mustMkdir(t, source)
	mustMkdir(t, repo)
	writeFile(t, filepath.Join(source, "tweets-2026-06-18.jsonl"), `{"id":"1","captured_at":"2026-06-18T10:00:00.000Z"}`+"\n")
	writeJSONConfig(t, config, map[string]any{
		"source_dir": filepath.ToSlash(source),
		"repo_dir":   filepath.ToSlash(repo),
		"push":       false,
	})
	initGitRepo(t, repo)

	if err := run(context.Background(), []string{
		"xtap-sync", "sync",
		"--config", config,
	}); err != nil {
		t.Fatal(err)
	}

	if err := run(context.Background(), []string{
		"xtap-sync", "verify",
		"--config", config,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRunSyncFlagsOverrideConfigFile(t *testing.T) {
	root := t.TempDir()
	configSource := filepath.Join(root, "config-source")
	flagSource := filepath.Join(root, "flag-source")
	configRepo := filepath.Join(root, "config-repo")
	flagRepo := filepath.Join(root, "flag-repo")
	config := filepath.Join(root, "config.json")
	for _, dir := range []string{configSource, flagSource, configRepo, flagRepo} {
		mustMkdir(t, dir)
	}
	writeFile(t, filepath.Join(configSource, "tweets-2026-06-18.jsonl"), `{"id":"config","captured_at":"2026-06-18T10:00:00.000Z"}`+"\n")
	writeFile(t, filepath.Join(flagSource, "tweets-2026-06-18.jsonl"), `{"id":"flag","captured_at":"2026-06-18T10:00:00.000Z"}`+"\n")
	writeJSONConfig(t, config, map[string]any{
		"source_dir": filepath.ToSlash(configSource),
		"repo_dir":   filepath.ToSlash(configRepo),
		"push":       false,
	})
	initGitRepo(t, flagRepo)

	if err := run(context.Background(), []string{
		"xtap-sync", "sync",
		"--config", config,
		"--source", flagSource,
		"--repo", flagRepo,
		"--no-push",
	}); err != nil {
		t.Fatal(err)
	}

	got := readFile(t, filepath.Join(flagRepo, "data", "tweets", "2026", "06", "tweets-2026-06-18.jsonl"))
	if !strings.Contains(got, `"id":"flag"`) || strings.Contains(got, `"id":"config"`) {
		t.Fatalf("flag repo content = %s, want only flag source record", got)
	}
}

func TestRunVerifyFailsForMissingTweets(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := filepath.Join(root, "source")
	repo := filepath.Join(root, "repo")
	mustMkdir(t, source)
	mustMkdir(t, repo)
	writeFile(t, filepath.Join(source, "tweets-2026-06-18.jsonl"), `{"id":"1","captured_at":"2026-06-18T10:00:00.000Z"}`+"\n")

	err := run(context.Background(), []string{
		"xtap-sync", "verify",
		"--source", source,
		"--repo", repo,
	})
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("err = %v, want missing tweet error", err)
	}
}

func TestRunRejectsBadConfig(t *testing.T) {
	t.Parallel()

	config := filepath.Join(t.TempDir(), "config.json")
	writeFile(t, config, `{"unknown":true}`+"\n")

	err := run(context.Background(), []string{"xtap-sync", "sync", "--config", config})
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("err = %v, want unknown field error", err)
	}
}

func TestRunSyncRejectsBadFlags(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), []string{"xtap-sync", "sync", "--bad"})
	if err == nil {
		t.Fatal("expected bad flag error")
	}
}

func TestRunVerifyRejectsBadFlags(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), []string{"xtap-sync", "verify", "--bad"})
	if err == nil {
		t.Fatal("expected bad flag error")
	}
}

func TestRunInstallAndUninstallRejectBadFlags(t *testing.T) {
	t.Parallel()

	if err := run(context.Background(), []string{"xtap-sync", "install-service", "--bad"}); err == nil {
		t.Fatal("expected install-service bad flag error")
	}
	if err := run(context.Background(), []string{"xtap-sync", "uninstall-service", "--bad"}); err == nil {
		t.Fatal("expected uninstall-service bad flag error")
	}
}

func TestDefaultSourceDirUsesEnvironment(t *testing.T) {
	t.Setenv("XTAP_SYNC_SOURCE_DIR", "/tmp/custom-xtap")

	if got := defaultSourceDir(); got != "/tmp/custom-xtap" {
		t.Fatalf("defaultSourceDir = %q, want /tmp/custom-xtap", got)
	}
}

func TestDefaultSourceDirFallsBackToHome(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XTAP_SYNC_SOURCE_DIR", "")
	t.Setenv("XTAP_OUTPUT_DIR", "")

	want := filepath.Join(root, "Downloads", "xtap")
	if got := defaultSourceDir(); got != want {
		t.Fatalf("defaultSourceDir = %q, want %q", got, want)
	}
}

func TestDefaultConfigPathUsesXDG(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))

	want := filepath.Join(root, "config", "xtap-sync", "config.json")
	if got := defaultConfigPath(); got != want {
		t.Fatalf("defaultConfigPath = %q, want %q", got, want)
	}
}

func TestExpandPathExpandsHomeAndEnvironment(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XTAP_TEST_DIR", "nested")

	got, err := expandPath("~/$XTAP_TEST_DIR")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "nested")
	if got != want {
		t.Fatalf("expandPath = %q, want %q", got, want)
	}
}

func TestUsageWritesSomething(t *testing.T) {
	t.Parallel()

	output := captureStderr(t, usage)
	if !strings.Contains(output, "xtap-sync sync") {
		t.Fatalf("usage output = %q", output)
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()

	old := os.Stderr
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = writer
	defer func() {
		os.Stderr = old
	}()

	fn()
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, reader); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	runGit(t, dir, "init", "-b", "main")
	runGit(t, dir, "config", "user.email", "test@example.invalid")
	runGit(t, dir, "config", "user.name", "Test User")
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeJSONConfig(t *testing.T, path string, config map[string]any) {
	t.Helper()
	content, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, string(content)+"\n")
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, string(out))
	}
}
