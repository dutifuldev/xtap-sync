//go:build !darwin

package syncer

import "fmt"

func InstallLaunchdService(ServiceConfig) (string, error) {
	return "", fmt.Errorf("launchd service installation is only supported on macOS")
}

func UninstallLaunchdService(string) (string, error) {
	return "", fmt.Errorf("launchd service installation is only supported on macOS")
}
