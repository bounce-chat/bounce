//go:build !android

package goengine

// On anything but Android the default logrus destination is already visible,
// so there is nothing to redirect. This exists so the package still builds and
// tests on the host.
func installPlatformLogger() {}
