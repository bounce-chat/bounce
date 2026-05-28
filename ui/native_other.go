//go:build !android

package ui

func batteryOptimizationsIgnored() bool {
	return true
}

func requestIgnoreBatteryOptimizations() {
}

func setStatusBar(_ bool) {}
