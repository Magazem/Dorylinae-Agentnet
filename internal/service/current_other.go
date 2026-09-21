//go:build !darwin && !linux && !windows

package service

// Current returns the backend for this operating system.
func Current() (Platform, error) { return nil, ErrUnsupported }
