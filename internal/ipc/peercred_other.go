//go:build !windows && !linux && !darwin

package ipc

func peerUID(int) (int, error) { return 0, errPeerCredUnsupported }
