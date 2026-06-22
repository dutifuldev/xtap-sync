package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/dutifuldev/xtap-sync/internal/syncer"
)

const (
	defaultLabel         = "dev.xtap-sync"
	defaultCommitMessage = "sync xTap tweets"
)

type fileConfig struct {
	SourceDir     string `json:"source_dir"`
	RepoDir       string `json:"repo_dir"`
	Remote        string `json:"remote"`
	Branch        string `json:"branch"`
	CommitMessage string `json:"commit_message"`
	Push          *bool  `json:"push"`
	ServiceLabel  string `json:"service_label"`
	Interval      string `json:"interval"`
}

type syncSettings struct {
	ConfigPath    string
	SourceDir     string
	RepoDir       string
	Remote        string
	Branch        string
	CommitMessage string
	Push          bool
}

func main() {
	if err := run(context.Background(), os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "xtap-sync: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) < 2 {
		usage()
		return fmt.Errorf("missing command")
	}

	switch args[1] {
	case "sync":
		return runSync(ctx, args[2:], false)
	case "service-run":
		return runSync(ctx, args[2:], true)
	case "install-service":
		return runInstallService(args[2:])
	case "uninstall-service":
		return runUninstallService(args[2:])
	case "verify":
		return runVerify(args[2:])
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[1])
	}
}

func runSync(ctx context.Context, args []string, serviceMode bool) error {
	settings, err := parseSyncSettings(args)
	if err != nil {
		return err
	}

	result, err := syncer.Sync(ctx, syncer.SyncOptions{
		SourceDir:     settings.SourceDir,
		RepoDir:       settings.RepoDir,
		Remote:        settings.Remote,
		Branch:        settings.Branch,
		CommitMessage: settings.CommitMessage,
		Push:          settings.Push,
	})
	if err != nil {
		return err
	}

	if !serviceMode {
		fmt.Printf("source_records=%d repo_records=%d written_records=%d files=%d committed=%t pushed=%t\n",
			result.SourceRecords,
			result.RepoRecords,
			result.WrittenRecords,
			result.WrittenFiles,
			result.Committed,
			result.Pushed,
		)
	}

	return nil
}

func parseSyncSettings(args []string) (syncSettings, error) {
	flags := commandFlagSet("sync")
	values := registerSyncFlags(flags, true)
	if err := flags.Parse(args); err != nil {
		return syncSettings{}, err
	}
	return resolveSyncSettings(values)
}

type syncFlagValues struct {
	config  *string
	source  *string
	repo    *string
	remote  *string
	branch  *string
	message *string
	noPush  *bool
}

func resolveSyncSettings(values syncFlagValues) (syncSettings, error) {
	cfg, loaded, err := loadConfig(*values.config)
	if err != nil {
		return syncSettings{}, err
	}

	settings := syncSettings{
		SourceDir:     firstNonEmpty(*values.source, cfg.SourceDir, defaultSourceDir()),
		RepoDir:       firstNonEmpty(*values.repo, cfg.RepoDir, defaultRepoDir()),
		Remote:        firstNonEmpty(*values.remote, cfg.Remote, "origin"),
		Branch:        firstNonEmpty(*values.branch, cfg.Branch),
		CommitMessage: firstNonEmpty(*values.message, cfg.CommitMessage, defaultCommitMessage),
		Push:          true,
	}
	if loaded {
		settings.ConfigPath = *values.config
	}
	if cfg.Push != nil {
		settings.Push = *cfg.Push
	}
	if *values.noPush {
		settings.Push = false
	}

	var expandErr error
	settings.SourceDir, expandErr = expandPath(settings.SourceDir)
	if expandErr != nil {
		return syncSettings{}, expandErr
	}
	settings.RepoDir, expandErr = expandPath(settings.RepoDir)
	if expandErr != nil {
		return syncSettings{}, expandErr
	}
	return settings, nil
}

func runVerify(args []string) error {
	settings, err := parseVerifySettings(args)
	if err != nil {
		return err
	}

	report, err := syncer.Verify(settings.SourceDir, settings.RepoDir)
	if err != nil {
		return err
	}

	fmt.Printf("source_unique=%d repo_unique=%d missing=%d extra=%d\n",
		report.SourceUnique,
		report.RepoUnique,
		len(report.MissingIDs),
		len(report.ExtraIDs),
	)

	if len(report.MissingIDs) > 0 {
		return fmt.Errorf("repo is missing %d source tweets", len(report.MissingIDs))
	}
	return nil
}

func parseVerifySettings(args []string) (syncSettings, error) {
	flags := commandFlagSet("verify")
	values := registerSyncFlags(flags, false)
	if err := flags.Parse(args); err != nil {
		return syncSettings{}, err
	}
	return resolveSyncSettings(values)
}

func runInstallService(args []string) error {
	service, err := parseInstallService(args)
	if err != nil {
		return err
	}

	var path string
	switch runtime.GOOS {
	case "darwin":
		path, err = syncer.InstallLaunchdService(service)
	case "linux":
		path, err = syncer.InstallSystemdUserService(service)
	default:
		err = fmt.Errorf("install-service supports macOS launchd and linux systemd user services only")
	}
	if err != nil {
		return err
	}

	fmt.Printf("installed %s\n", path)
	return nil
}

func parseInstallService(args []string) (syncer.ServiceConfig, error) {
	flags := commandFlagSet("install-service")
	values := registerSyncFlags(flags, true)
	intervalText := flags.String("interval", "", "sync interval")
	label := flags.String("label", "", "service label")
	noStart := flags.Bool("no-start", false, "write service files but do not load/start them")

	if err := flags.Parse(args); err != nil {
		return syncer.ServiceConfig{}, err
	}

	cfg, _, err := loadConfig(*values.config)
	if err != nil {
		return syncer.ServiceConfig{}, err
	}
	settings, err := resolveSyncSettings(values)
	if err != nil {
		return syncer.ServiceConfig{}, err
	}
	interval, err := parseInterval(firstNonEmpty(*intervalText, cfg.Interval, "1h"))
	if err != nil {
		return syncer.ServiceConfig{}, err
	}
	return buildServiceConfig(settings, cfg, serviceOverrides{
		label:    *label,
		interval: interval,
		noStart:  *noStart,
	})
}

type serviceOverrides struct {
	label    string
	interval time.Duration
	noStart  bool
}

func buildServiceConfig(settings syncSettings, cfg fileConfig, overrides serviceOverrides) (syncer.ServiceConfig, error) {
	bin, err := os.Executable()
	if err != nil {
		return syncer.ServiceConfig{}, err
	}
	repoAbs, err := filepath.Abs(settings.RepoDir)
	if err != nil {
		return syncer.ServiceConfig{}, err
	}

	return syncer.ServiceConfig{
		Label:         firstNonEmpty(overrides.label, cfg.ServiceLabel, defaultLabel),
		BinaryPath:    bin,
		ConfigPath:    settings.ConfigPath,
		SourceDir:     settings.SourceDir,
		RepoDir:       repoAbs,
		Remote:        settings.Remote,
		Branch:        settings.Branch,
		CommitMessage: settings.CommitMessage,
		NoPush:        !settings.Push,
		Interval:      overrides.interval,
		LoadService:   !overrides.noStart,
	}, nil
}

func runUninstallService(args []string) error {
	flags := flag.NewFlagSet("uninstall-service", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)

	label := flags.String("label", defaultLabel, "service label")
	if err := flags.Parse(args); err != nil {
		return err
	}

	var path string
	var err error
	switch runtime.GOOS {
	case "darwin":
		path, err = syncer.UninstallLaunchdService(*label)
	case "linux":
		path, err = syncer.UninstallSystemdUserService(*label)
	default:
		err = fmt.Errorf("uninstall-service supports macOS launchd and linux systemd user services only")
	}
	if err != nil {
		return err
	}

	fmt.Printf("uninstalled %s\n", path)
	return nil
}

func commandFlagSet(name string) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	return flags
}

func registerSyncFlags(flags *flag.FlagSet, includeGit bool) syncFlagValues {
	values := syncFlagValues{
		config:  flags.String("config", defaultConfigPath(), "JSON configuration file"),
		source:  flags.String("source", "", "xTap output directory"),
		repo:    flags.String("repo", "", "target git repository directory"),
		remote:  new(string),
		branch:  new(string),
		message: new(string),
		noPush:  new(bool),
	}
	if includeGit {
		values.remote = flags.String("remote", "", "git remote to fetch and push")
		values.branch = flags.String("branch", "", "git branch to sync; defaults to current branch")
		values.message = flags.String("message", "", "git commit message")
		values.noPush = flags.Bool("no-push", false, "do not push after committing")
	}
	return values
}

func loadConfig(path string) (fileConfig, bool, error) {
	if strings.TrimSpace(path) == "" {
		return fileConfig{}, false, nil
	}

	path, err := expandPath(path)
	if err != nil {
		return fileConfig{}, false, err
	}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return fileConfig{}, false, nil
	}
	if err != nil {
		return fileConfig{}, false, err
	}
	defer func() {
		_ = file.Close()
	}()

	var cfg fileConfig
	dec := json.NewDecoder(file)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return fileConfig{}, false, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, true, nil
}

func parseInterval(value string) (time.Duration, error) {
	interval, err := time.ParseDuration(value)
	if err != nil {
		return 0, err
	}
	if interval <= 0 {
		return 0, fmt.Errorf("interval must be positive")
	}
	return interval, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func expandPath(path string) (string, error) {
	path = os.ExpandEnv(path)
	if path == "~" {
		return userHomeDir()
	}
	if strings.HasPrefix(path, "~/") {
		home, err := userHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}

func defaultConfigPath() string {
	if value := os.Getenv("XTAP_SYNC_CONFIG"); value != "" {
		return value
	}
	if value := os.Getenv("XDG_CONFIG_HOME"); value != "" {
		return filepath.Join(value, "xtap-sync", "config.json")
	}
	home, err := userHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "xtap-sync", "config.json")
}

func defaultSourceDir() string {
	if value := os.Getenv("XTAP_SYNC_SOURCE_DIR"); value != "" {
		return value
	}
	if value := os.Getenv("XTAP_OUTPUT_DIR"); value != "" {
		return value
	}
	home, err := userHomeDir()
	if err != nil {
		return "Downloads/xtap"
	}
	return filepath.Join(home, "Downloads", "xtap")
}

func defaultRepoDir() string {
	if value := os.Getenv("XTAP_SYNC_REPO_DIR"); value != "" {
		return value
	}
	return "."
}

func userHomeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return home, nil
}

func usage() {
	fmt.Fprintln(os.Stderr, `Usage:
  xtap-sync sync [--config FILE] [--source DIR] [--repo DIR] [--no-push]
  xtap-sync service-run [--config FILE] [--source DIR] [--repo DIR]
  xtap-sync install-service [--config FILE] [--source DIR] [--repo DIR] [--interval 1h]
  xtap-sync uninstall-service
  xtap-sync verify [--config FILE] [--source DIR] [--repo DIR]`)
}
