//go:build !android

package fyneui

func batteryOptimizationsIgnored() bool {
	return true
}

func requestIgnoreBatteryOptimizations() {
}

func setStatusBar(_ bool) {}
