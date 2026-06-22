package syncer

import (
	"fmt"
	"time"
)

type ServiceConfig struct {
	Label         string
	BinaryPath    string
	ConfigPath    string
	SourceDir     string
	RepoDir       string
	Remote        string
	Branch        string
	CommitMessage string
	NoPush        bool
	Interval      time.Duration
	LoadService   bool
}

func validateServiceConfig(cfg ServiceConfig) error {
	if cfg.Label == "" {
		return fmt.Errorf("label is required")
	}
	if cfg.BinaryPath == "" {
		return fmt.Errorf("binary path is required")
	}
	if cfg.SourceDir == "" {
		return fmt.Errorf("source directory is required")
	}
	if cfg.RepoDir == "" {
		return fmt.Errorf("repo directory is required")
	}
	return nil
}

func withDefaultInterval(cfg ServiceConfig) ServiceConfig {
	if cfg.Interval <= 0 {
		cfg.Interval = time.Hour
	}
	return cfg
}

func serviceRunArgs(cfg ServiceConfig) []string {
	args := []string{
		cfg.BinaryPath,
		"service-run",
	}
	if cfg.ConfigPath != "" {
		return append(args, "--config", cfg.ConfigPath)
	}

	args = append(args,
		"--source", cfg.SourceDir,
		"--repo", cfg.RepoDir,
		"--remote", stringDefault(cfg.Remote, "origin"),
		"--message", stringDefault(cfg.CommitMessage, "sync xTap tweets"),
	)
	if cfg.Branch != "" {
		args = append(args, "--branch", cfg.Branch)
	}
	if cfg.NoPush {
		args = append(args, "--no-push")
	}
	return args
}

func stringDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
