//go:build darwin

package service

// Current returns the backend for this operating system.
func Current() (Platform, error) { return Launchd{}, nil }
