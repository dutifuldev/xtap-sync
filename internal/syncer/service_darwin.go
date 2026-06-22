package syncer

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

func InstallLaunchdService(cfg ServiceConfig) (string, error) {
	if err := validateServiceConfig(cfg); err != nil {
		return "", err
	}
	cfg = withDefaultInterval(cfg)

	plistPath, logDir, err := prepareLaunchdPaths(cfg.Label)
	if err != nil {
		return "", err
	}

	if err := writeLaunchdPlist(plistPath, cfg, logDir); err != nil {
		return "", err
	}
	if cfg.LoadService {
		return plistPath, loadLaunchdService(cfg.Label, plistPath)
	}
	return plistPath, nil
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

func prepareLaunchdPaths(label string) (string, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}

	logDir := filepath.Join(home, "Library", "Logs", "xtap-sync")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return "", "", err
	}

	launchAgents := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(launchAgents, 0o755); err != nil {
		return "", "", err
	}

	return filepath.Join(launchAgents, label+".plist"), logDir, nil
}

func writeLaunchdPlist(plistPath string, cfg ServiceConfig, logDir string) error {
	plist := launchdPlist(cfg, logDir)
	return os.WriteFile(plistPath, []byte(plist), 0o644)
}

func loadLaunchdService(label, plistPath string) error {
	uid := fmt.Sprintf("gui/%d", os.Getuid())
	_ = exec.Command("launchctl", "bootout", uid+"/"+label).Run()
	if err := exec.Command("launchctl", "bootstrap", uid, plistPath).Run(); err != nil {
		return err
	}
	_ = exec.Command("launchctl", "kickstart", "-k", uid+"/"+label).Run()
	return nil
}

func UninstallLaunchdService(label string) (string, error) {
	if label == "" {
		return "", fmt.Errorf("label is required")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	plistPath := filepath.Join(home, "Library", "LaunchAgents", label+".plist")
	uid := fmt.Sprintf("gui/%d", os.Getuid())
	_ = exec.Command("launchctl", "bootout", uid+"/"+label).Run()

	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return "", err
	}

	return plistPath, nil
}

func launchdPlist(cfg ServiceConfig, logDir string) string {
	seconds := launchdIntervalSeconds(cfg.Interval)

	args := []string{
		cfg.BinaryPath,
		"service-run",
	}
	if cfg.ConfigPath != "" {
		args = append(args, "--config", cfg.ConfigPath)
	} else {
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
	}

	var buf bytes.Buffer
	buf.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	buf.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	buf.WriteString(`<plist version="1.0">` + "\n")
	buf.WriteString("<dict>\n")
	writeKeyString(&buf, "Label", cfg.Label)
	buf.WriteString("  <key>ProgramArguments</key>\n")
	buf.WriteString("  <array>\n")
	for _, arg := range args {
		buf.WriteString("    <string>")
		writeEscapedXML(&buf, arg)
		buf.WriteString("</string>\n")
	}
	buf.WriteString("  </array>\n")
	writeKeyString(&buf, "WorkingDirectory", cfg.RepoDir)
	buf.WriteString("  <key>StartInterval</key>\n")
	fmt.Fprintf(&buf, "  <integer>%d</integer>\n", seconds)
	buf.WriteString("  <key>RunAtLoad</key>\n")
	buf.WriteString("  <true/>\n")
	writeKeyString(&buf, "StandardOutPath", filepath.Join(logDir, "sync.out.log"))
	writeKeyString(&buf, "StandardErrorPath", filepath.Join(logDir, "sync.err.log"))
	buf.WriteString("</dict>\n")
	buf.WriteString("</plist>\n")
	return buf.String()
}

func stringDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func launchdIntervalSeconds(interval time.Duration) int {
	seconds := int(interval.Round(time.Second).Seconds())
	if seconds <= 0 {
		return int(time.Hour.Seconds())
	}
	return seconds
}

func writeKeyString(buf *bytes.Buffer, key, value string) {
	buf.WriteString("  <key>")
	writeEscapedXML(buf, key)
	buf.WriteString("</key>\n")
	buf.WriteString("  <string>")
	writeEscapedXML(buf, value)
	buf.WriteString("</string>\n")
}

func writeEscapedXML(buf *bytes.Buffer, value string) {
	_ = xml.EscapeText(buf, []byte(value))
}
