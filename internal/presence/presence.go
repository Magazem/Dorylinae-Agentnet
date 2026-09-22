// Package presence implements Docs/protocol/presence.md: the presence
// message body and its padding, the sealed heartbeat (reusing internal/mail),
// and the receiver's order/replay rule and store (migration 10). The sender
// loop (ticks, the visible set, agent/human edges, status --team) is ticket
// 1.2c, not here.
package presence

import (
	"time"
)

// Kind is the presence message kind, Docs/protocol/presence.md §Envelope.
const Kind = "presence"

// padBlock is the multiple of bytes the canonical signed plaintext must pad to.
const padBlock = 256

// windowBefore and windowAfter bound msg.created against the receiver clock,
// Docs/protocol/presence.md §Receiving step 3.
const (
	windowBefore = 10 * time.Minute
	windowAfter  = 10 * time.Minute
)

// Wire limits, Docs/protocol/presence.md §Body.
const (
	minInterval = 1
	maxInterval = 300
	maxEpochs   = 32
	maxPad      = 1023
)

// wireTimeFmt is the RFC 3339 UTC, whole-seconds format used for msg.created,
// matching the unexported one in package mail.
const wireTimeFmt = "2006-01-02T15:04:05Z"
