//go:build !linux

package syncer

import "fmt"

func InstallSystemdUserService(ServiceConfig) (string, error) {
	return "", fmt.Errorf("systemd user service installation is only supported on linux")
}

func UninstallSystemdUserService(string) (string, error) {
	return "", fmt.Errorf("systemd user service installation is only supported on linux")
}
