//go:build windows

package service

// Current returns the backend for this operating system.
func Current() (Platform, error) { return Schtasks{}, nil }
