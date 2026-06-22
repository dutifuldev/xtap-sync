//go:build !darwin

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

func InstallLaunchdService(ServiceConfig) (string, error) {
	return "", fmt.Errorf("launchd service installation is only supported on macOS")
}

func UninstallLaunchdService(string) (string, error) {
	return "", fmt.Errorf("launchd service installation is only supported on macOS")
}
