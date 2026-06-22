package syncer

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func InstallSystemdUserService(cfg ServiceConfig) (string, error) {
	if err := validateServiceConfig(cfg); err != nil {
		return "", err
	}
	cfg = withDefaultInterval(cfg)

	unitDir, servicePath, timerPath, err := prepareSystemdUserPaths(cfg.Label)
	if err != nil {
		return "", err
	}

	if err := os.WriteFile(servicePath, []byte(systemdServiceUnit(cfg)), 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(timerPath, []byte(systemdTimerUnit(cfg)), 0o644); err != nil {
		return "", err
	}
	if cfg.LoadService {
		return servicePath, enableSystemdUserTimer(cfg.Label)
	}

	_ = unitDir
	return servicePath, nil
}

func prepareSystemdUserPaths(label string) (string, string, string, error) {
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", "", "", err
		}
		configHome = filepath.Join(home, ".config")
	}

	unitDir := filepath.Join(configHome, "systemd", "user")
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		return "", "", "", err
	}

	return unitDir,
		filepath.Join(unitDir, label+".service"),
		filepath.Join(unitDir, label+".timer"),
		nil
}

func enableSystemdUserTimer(label string) error {
	timerName := label + ".timer"
	if err := exec.Command("systemctl", "--user", "daemon-reload").Run(); err != nil {
		return err
	}
	return exec.Command("systemctl", "--user", "enable", "--now", timerName).Run()
}

func UninstallSystemdUserService(label string) (string, error) {
	if label == "" {
		return "", fmt.Errorf("label is required")
	}

	unitDir, servicePath, timerPath, err := prepareSystemdUserPaths(label)
	if err != nil {
		return "", err
	}

	_ = exec.Command("systemctl", "--user", "disable", "--now", label+".timer").Run()
	if err := removeIfExists(timerPath); err != nil {
		return "", err
	}
	if err := removeIfExists(servicePath); err != nil {
		return "", err
	}
	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()

	_ = unitDir
	return servicePath, nil
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func systemdServiceUnit(cfg ServiceConfig) string {
	args := serviceRunArgs(cfg)

	var buf bytes.Buffer
	buf.WriteString("[Unit]\n")
	buf.WriteString("Description=xTap Sync\n\n")
	buf.WriteString("[Service]\n")
	buf.WriteString("Type=oneshot\n")
	fmt.Fprintf(&buf, "WorkingDirectory=%s\n", systemdPathValue(cfg.RepoDir))
	buf.WriteString("ExecStart=")
	for i, arg := range args {
		if i > 0 {
			buf.WriteByte(' ')
		}
		buf.WriteString(systemdQuote(arg))
	}
	buf.WriteString("\n")
	return buf.String()
}

func systemdTimerUnit(cfg ServiceConfig) string {
	var buf bytes.Buffer
	buf.WriteString("[Unit]\n")
	buf.WriteString("Description=Run xTap Sync periodically\n\n")
	buf.WriteString("[Timer]\n")
	buf.WriteString("OnStartupSec=1min\n")
	fmt.Fprintf(&buf, "OnUnitActiveSec=%s\n", systemdInterval(cfg.Interval))
	buf.WriteString("Persistent=true\n")
	fmt.Fprintf(&buf, "Unit=%s.service\n\n", cfg.Label)
	buf.WriteString("[Install]\n")
	buf.WriteString("WantedBy=timers.target\n")
	return buf.String()
}

func systemdInterval(interval time.Duration) string {
	seconds := int(interval.Round(time.Second).Seconds())
	if seconds <= 0 {
		seconds = int(time.Hour.Seconds())
	}
	return fmt.Sprintf("%ds", seconds)
}

func systemdPathValue(value string) string {
	return strings.NewReplacer(
		`\`, `\\`,
		" ", `\x20`,
		"%", "%%",
		"\n", `\n`,
		"\r", `\r`,
		"\t", `\t`,
	).Replace(value)
}

func systemdQuote(value string) string {
	if value == "" {
		return `""`
	}

	var buf bytes.Buffer
	buf.WriteByte('"')
	for _, r := range value {
		switch r {
		case '\\', '"':
			buf.WriteByte('\\')
			buf.WriteRune(r)
		case '%':
			buf.WriteString("%%")
		case '\n', '\r':
			buf.WriteByte(' ')
		default:
			buf.WriteRune(r)
		}
	}
	buf.WriteByte('"')
	return strings.TrimSpace(buf.String())
}
