// Package team implements Docs/protocol/team.md: the team store, the
// owner-side operations, roster construction, and the application mail kinds
// team.roster, team.join and team.leave.
package team

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"regexp"
)

// Local membership states (teams.state). The wire team.state carries only
// active and dissolved; left and removed are local-only views.
const (
	StateActive    = "active"
	StateLeft      = "left"
	StateRemoved   = "removed"
	StateDissolved = "dissolved"
)

// MaxMembers is the largest a team's roster may be.
const MaxMembers = 32

// storeTimeFmt is the internal bookkeeping timestamp format (created/updated),
// matching mail.StoreTimeFmt.
const storeTimeFmt = "2006-01-02T15:04:05.000Z"

// wireTimeFmt is the RFC 3339 UTC, whole-seconds format used on the wire
// (team.epoch's members[].added, and msg.created elsewhere).
const wireTimeFmt = "2006-01-02T15:04:05Z"

var (
	idPattern   = regexp.MustCompile(`^t-[0-9a-f]{32}$`)
	namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
)

// Errors returned by the owner-side operations.
var (
	ErrExists           = errors.New("team: a local active team already has this name")
	ErrNotFound         = errors.New("team: no such team")
	ErrNotOwner         = errors.New("team: not the owner")
	ErrOwnerCannotLeave = errors.New("team: the owner cannot leave")
	ErrNoSuchMember     = errors.New("team: not a member")
	ErrFull             = errors.New("team: already has 32 members")
)

// Team is one row of the teams table.
type Team struct {
	ID      string
	Name    string
	Owner   string
	Epoch   int64
	State   string
	Created string
	Updated string
}

// Member is one row of team_members.
type Member struct {
	Key   string
	Added string
}

// NewID returns a fresh team id: "t-" and 32 lowercase hex characters, 16
// bytes from crypto/rand.
func NewID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // crypto/rand.Read never fails
	return "t-" + hex.EncodeToString(b)
}

// ValidID reports whether s has the team id format.
func ValidID(s string) bool { return idPattern.MatchString(s) }

// ValidName reports whether s is a valid team name.
func ValidName(s string) bool { return namePattern.MatchString(s) }
