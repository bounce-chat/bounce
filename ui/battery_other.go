//go:build !android && !ios

package ui

func batteryOptimizationsIgnored() bool {
	return true
}

func requestIgnoreBatteryOptimizations() {
}
