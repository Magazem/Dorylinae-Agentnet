// Package approvaltext is the one approval-summary builder of
// Docs/protocol/approval.md §Approval summaries (R55-F5). Every approval
// kind has a facts type, taken by the handler from the object that will be
// performed, and a Build function that turns it into the exact text the
// window, the notification, the terminal line and approval_list show.
//
// Build is pure: the same facts give the same bytes. It reads no clock,
// locale, time zone, environment or database, so a Precondition can rebuild
// the text at confirm and compare it byte for byte with approvals.summary.
// Its output is display-safe (displaytext.Safe) and at most
// displaytext.MaxSummary code points, or it is ErrTooLong; it is never cut.
package approvaltext

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Magazem/Dorylinae-Agentnet/internal/displaytext"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

// ErrTooLong is returned when a summary would be longer than
// displaytext.MaxSummary code points (Docs/protocol/approval.md §Length):
// the handler refuses the call before anything is stored.
var ErrTooLong = errors.New("approvaltext: summary too long to show in full in the approval window")

// Peer identifies the other side by the key the action binds to and the
// current name of its peers row (Docs/protocol/approval.md §Sanitising, "The
// fingerprint"). Paired is false when the key is no longer in peers.
type Peer struct {
	Key    string
	Name   string
	Paired bool
}

// Grant is the facts of a grant approval: from the signed token Perform
// sends, plus the resolved path of the row and the session's request
// (Docs/protocol/approval.md §Contents per kind, review 58a M3).
type Grant struct {
	ID           string
	Action       string
	Peer         Peer
	Path         string
	Label        string
	Branch       string
	Scope        string
	Sensitive    bool
	Nbf, Exp     time.Time
	Session      string
	RequestType  string
	RequestTitle string
}

// Policy is the facts of a grant_policy approval.
type Policy struct {
	ID         string
	Action     string
	Peer       Peer
	Path       string
	Branch     string
	Scope      string
	Public     bool
	MaxExpires time.Duration
	Created    time.Time
	Until      time.Time
}

// Result is the facts of a release or accept_result approval: the session,
// its request and the result's status and sizes, never its content
// (OD-R55F5-8). SensitiveGrants (K) is used by release only.
type Result struct {
	Session         string
	Peer            Peer
	RequestID       string
	RequestType     string
	RequestTitle    string
	Round           int
	Seq             int
	Status          string
	ResultBytes     int
	OutputBytes     int
	Artifacts       int
	SensitiveGrants int
}

// Link is the facts of a device_link approval.
type Link struct {
	ID   string
	Peer Peer
	Role string // this device's role: "helper" or "controller"
}

// ScopeCommand is one command of a scope, with its repo resolved to a
// directory.
type ScopeCommand struct {
	Name     string
	Dir      string
	Argv     []string
	TimeoutS int
	Env      []string
}

// Scope is the facts of a device_scope approval: the resolved scope.
type Scope struct {
	Peer     Peer
	Types    []string
	Commands []ScopeCommand
	Expires  time.Time
}

// Constraint is the facts of a debate_constraint approval.
type Constraint struct {
	Session string
	Peer    Peer
	ID      string
	Text    string
}

// PeerVerify is the facts of a peer_verify approval.
type PeerVerify struct {
	Peer Peer
}

// TeamInvite is the facts of a team_invite approval: the team the owner
// would invite a new member to.
type TeamInvite struct {
	TeamID   string
	TeamName string
}

// q is displayQuote.
func q(s string) string { return displaytext.Quote(s) }

// plain writes a value that the protocol keeps to a small ASCII set (an
// action, a session id, a status, a request type, a command name): as is when
// it is, quoted otherwise, so the builder stays display-safe whatever it is
// given.
func plain(s string) string {
	if s == "" {
		return q(s)
	}
	for i := 0; i < len(s); i++ {
		if !plainByte(s[i]) {
			return q(s)
		}
	}
	return s
}

func plainByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	default:
		return c == '.' || c == '_' || c == '-' || c == ':'
	}
}

// peer renders <peer>: the full fingerprint first, then the quoted, cleaned
// name (Docs/protocol/approval.md §Sanitising, review 58a H1).
func peer(p Peer) (string, error) {
	fp, err := envelope.KeyFingerprint(p.Key)
	if err != nil {
		return "", fmt.Errorf("approvaltext: peer key: %w", err)
	}
	if !p.Paired {
		return "peer " + envelope.FormatFingerprint(fp) + " (no longer paired)", nil
	}
	return "peer " + envelope.FormatFingerprint(fp) + " named " + q(displaytext.Name(p.Name)), nil
}

// UTC renders <utc(t)>: the instant in UTC to the minute (OD-R55F5-3).
func UTC(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04") + " UTC"
}

// Dur renders <dur(d)>: whole units, largest first, at most two
// (Docs/protocol/approval.md §Contents per kind).
func Dur(d time.Duration) string {
	if d < time.Minute {
		return "less than 1 min"
	}
	units := []struct {
		n    time.Duration
		name string
	}{{24 * time.Hour, "d"}, {time.Hour, "h"}, {time.Minute, "min"}}
	var parts []string
	for i, u := range units {
		if d < u.n {
			continue
		}
		parts = append(parts, strconv.FormatInt(int64(d/u.n), 10)+" "+u.name)
		rest := d % u.n
		if i+1 < len(units) && rest >= units[i+1].n {
			parts = append(parts, strconv.FormatInt(int64(rest/units[i+1].n), 10)+" "+units[i+1].name)
		}
		break
	}
	return strings.Join(parts, " ")
}

// finish applies the length limit and the display-safe backstop.
func finish(s string) (string, error) {
	if utf8.RuneCountInString(s) > displaytext.MaxSummary {
		return "", ErrTooLong
	}
	if !displaytext.Safe(s) {
		return "", errors.New("approvaltext: summary is not display-safe")
	}
	return s, nil
}

// BuildGrant is the grant template.
func BuildGrant(f Grant) (string, error) {
	who, err := peer(f.Peer)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Grant %s to %s on %s (label %s)", plain(f.Action), who, q(f.Path), q(f.Label))
	if f.Branch != "" {
		fmt.Fprintf(&b, ", branch %s", q(f.Branch))
	}
	switch {
	case f.Scope != "":
		fmt.Fprintf(&b, ", only %s inside it", q(f.Scope))
	case f.Action == "git.read":
		b.WriteString(", the whole repository")
	default:
		b.WriteString(", the whole folder")
	}
	fmt.Fprintf(&b, ", for %s until %s, in session %s (your %s request %s). ",
		Dur(f.Exp.Sub(f.Nbf)), UTC(f.Exp), plain(f.Session), plain(f.RequestType), q(f.RequestTitle))
	if f.Sensitive {
		b.WriteString("Sensitive: results of this session stay quarantined until you release them.")
	} else {
		b.WriteString("PUBLIC: you state this repository is public; results of this session are NOT quarantined.")
	}
	b.WriteString(" Confirm only if you asked for exactly this.")
	return finish(b.String())
}

// BuildPolicy is the grant_policy template.
func BuildPolicy(f Policy) (string, error) {
	who, err := peer(f.Peer)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Allow grants with no further question until %s (%s): %s to %s on %s",
		UTC(f.Until), Dur(f.Until.Sub(f.Created)), plain(f.Action), who, q(f.Path))
	if f.Branch != "" {
		fmt.Fprintf(&b, ", branch %s", q(f.Branch))
	}
	if f.Scope != "" {
		fmt.Fprintf(&b, ", only paths inside %s", q(f.Scope))
	} else {
		b.WriteString(", any path in it")
	}
	fmt.Fprintf(&b, ", each grant for at most %s, ", Dur(f.MaxExpires))
	if f.Public {
		b.WriteString("PUBLIC grants only: results are NOT quarantined")
	} else {
		b.WriteString("sensitive grants only (results quarantined)")
	}
	b.WriteString(". Confirm only if you asked for exactly this.")
	return finish(b.String())
}

func resultHead(verb string, f Result) (string, error) {
	who, err := peer(f.Peer)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s %s for your %s request %s (session %s, round %d): status %s, %d bytes, %d bytes of output, %d artifact(s).",
		verb, who, plain(f.RequestType), q(f.RequestTitle), plain(f.Session), f.Round, plain(f.Status),
		f.ResultBytes, f.OutputBytes, f.Artifacts), nil
}

// BuildRelease is the release template (OD-R55F5-7).
func BuildRelease(f Result) (string, error) {
	head, err := resultHead("Release the quarantined result of", f)
	if err != nil {
		return "", err
	}
	reason := "This peer held a sensitive grant in another session in the last 7 days."
	if f.SensitiveGrants > 0 {
		reason = fmt.Sprintf("This session had %d sensitive grant(s).", f.SensitiveGrants)
	}
	return finish(head + " Your agent will then be able to read it. " + reason + " Confirm only if you mean to release this result.")
}

// BuildAcceptResult is the accept_result template (OD-R55F5-8).
func BuildAcceptResult(f Result) (string, error) {
	head, err := resultHead("Accept the result of", f)
	if err != nil {
		return "", err
	}
	return finish(head + " This closes the request as accepted. Confirm only if you checked this result.")
}

// BuildLink is the device_link template (D44/D9: the human compares the
// fingerprint).
func BuildLink(f Link) (string, error) {
	who, err := peer(f.Peer)
	if err != nil {
		return "", err
	}
	var part string
	switch f.Role {
	case "helper":
		part = "That device will be able to run, on this device, the commands of a scope you set later."
	case "controller":
		part = "This device will be able to ask that device to run the commands of its scope."
	default:
		return "", fmt.Errorf("approvaltext: unknown role %q", f.Role)
	}
	return finish(fmt.Sprintf("Link this device as the %s of %s. %s Compare all five groups of this fingerprint with what 'agentnet identity' shows on the other device. Confirm only if all of them match and you started this on both devices.",
		f.Role, who, part))
}

// BuildScope is the device_scope template: the command count and names come
// first, so a command below the fold is still named at the top (review 58a
// M1).
func BuildScope(f Scope) (string, error) {
	who, err := peer(f.Peer)
	if err != nil {
		return "", err
	}
	names := make([]string, len(f.Commands))
	for i, c := range f.Commands {
		names[i] = plain(c.Name)
	}
	types := make([]string, len(f.Types))
	for i, t := range f.Types {
		types[i] = plain(t)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Let %s run %d command(s) (%s) on this device until %s, for %s requests:",
		who, len(f.Commands), strings.Join(names, ", "), UTC(f.Expires), strings.Join(types, ", "))
	for _, c := range f.Commands {
		argv := make([]string, len(c.Argv))
		for i, a := range c.Argv {
			argv[i] = q(a)
		}
		fmt.Fprintf(&b, " [%s] in %s runs [%s] (timeout %d s", plain(c.Name), q(c.Dir), strings.Join(argv, ","), c.TimeoutS)
		if len(c.Env) > 0 {
			env := make([]string, len(c.Env))
			for i, e := range c.Env {
				env[i] = plain(e)
			}
			fmt.Fprintf(&b, ", env %s", strings.Join(env, " "))
		}
		b.WriteString(");")
		if utf8.RuneCountInString(b.String()) > displaytext.MaxSummary {
			return "", ErrTooLong
		}
	}
	b.WriteString(" Confirm only if you set this scope yourself.")
	return finish(b.String())
}

// BuildConstraint is the debate_constraint template.
func BuildConstraint(f Constraint) (string, error) {
	who, err := peer(f.Peer)
	if err != nil {
		return "", err
	}
	return finish(fmt.Sprintf("Add a human constraint to the debate %s with %s: %s. It is signed into the Decision as a human decision. Confirm only if you wrote this constraint yourself.",
		plain(f.Session), who, q(f.Text)))
}

// BuildPeerVerify is the peer_verify template: the fingerprint is grouped and
// shown in full by the shared peer renderer (D9).
func BuildPeerVerify(f PeerVerify) (string, error) {
	who, err := peer(f.Peer)
	if err != nil {
		return "", err
	}
	if !f.Peer.Paired {
		return "", errors.New("approvaltext: peer_verify needs a paired peer")
	}
	return finish(fmt.Sprintf("Mark %s as verified. Compare all five groups of this fingerprint with what 'agentnet identity' shows on that peer's machine. Confirm only if all of them match and you started this yourself.", who))
}

// BuildTeamInvite is the team_invite template.
func BuildTeamInvite(f TeamInvite) (string, error) {
	return finish(fmt.Sprintf("Create a one-time invite code for the team %s (%s). Whoever redeems it joins the team, and every member's daemon will then trust them as a team member. Confirm only if you asked for this invite yourself.",
		plain(f.TeamName), plain(f.TeamID)))
}
