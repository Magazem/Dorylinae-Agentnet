//go:build !windows && !linux && !darwin

package ipc

import "errors"

// errPeerCredUnsupported is what peerUID returns without a peer credential
// call; Dial then refuses every socket, since it cannot tell who serves it.
var errPeerCredUnsupported = errors.New("peer credentials not supported")

func peerUID(int) (int, error) { return 0, errPeerCredUnsupported }
