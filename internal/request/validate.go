package request

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	idPattern     = regexp.MustCompile(`^r-[0-9a-f]{32}$`)
	teamPattern   = regexp.MustCompile(`^t-[0-9a-f]{32}$`)
	keyPattern    = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	commitPattern = regexp.MustCompile(`^[0-9a-f]{7,64}$`)
	actionPattern = regexp.MustCompile(`^[a-z0-9._-]{1,64}$`)

	// branchBad holds the characters Docs/protocol/request.md §Artifacts
	// disallows in branch, beyond control characters and spaces.
	branchBad = "~^:?*[\\"
)

// ValidID reports whether s has the request id format ("r-" + 32 lowercase
// hex characters).
func ValidID(s string) bool { return idPattern.MatchString(s) }

// Validate checks every field rule in Docs/protocol/request.md §Request
// object and §Size limits, except the total body cap (CheckSize, which needs
// the canonical bytes). Both the sender (before request_submit) and the
// recipient (in Apply) call this on a fully decoded Request. It returns the
// first violation as a *FieldError.
func Validate(r *Request) error {
	if r.V != 1 {
		return fieldErr("v", "must be 1")
	}
	if !ValidID(r.ID) {
		return fieldErr("id", "must be \"r-\" followed by 32 lowercase hex characters")
	}
	if !keyPattern.MatchString(r.From) {
		return fieldErr("from", "must be a 43-character base64url identity key")
	}
	if !keyPattern.MatchString(r.To) {
		return fieldErr("to", "must be a 43-character base64url identity key")
	}
	if !teamPattern.MatchString(r.Team) {
		return fieldErr("team", "must be \"t-\" followed by 32 hex characters")
	}
	switch r.Type {
	case TypeReview, TypeTask, TypeQuestion, TypeDebate:
	default:
		return fieldErr("type", "must be review, task, question or debate")
	}
	if err := checkCodePoints("title", r.Title, minTitleCodePoints, maxTitleCodePoints, ""); err != nil {
		return err
	}
	if err := checkBriefBytes(r.Brief); err != nil {
		return err
	}
	if err := validateDebate(r); err != nil {
		return err
	}
	switch r.Urgency {
	case UrgencyLow, UrgencyNormal, UrgencyHigh, UrgencyBlocking:
	default:
		return fieldErr("urgency", "must be low, normal, high or blocking")
	}
	if r.UrgencyDeclared != "" {
		switch r.UrgencyDeclared {
		case UrgencyHigh, UrgencyBlocking:
		default:
			return fieldErr("urgency_declared", "must be high or blocking when present")
		}
		if r.Urgency != UrgencyNormal {
			return fieldErr("urgency_declared", "urgency must be normal when urgency_declared is set")
		}
	}
	needReason := r.UrgencyDeclared != "" || r.Urgency == UrgencyHigh || r.Urgency == UrgencyBlocking
	if needReason && r.UrgencyReason == "" {
		return fieldErr("urgency_reason", "is required when urgency_declared is set or urgency is high or blocking")
	}
	if r.UrgencyReason != "" {
		if err := checkCodePoints("urgency_reason", r.UrgencyReason, minUrgencyReasonCodePoints, maxUrgencyReasonCodePoints, ""); err != nil {
			return err
		}
	}
	if len(r.Artifacts) < minArtifacts || len(r.Artifacts) > maxArtifacts {
		return fieldErr("artifacts", "must hold 0-%d artifacts", maxArtifacts)
	}
	for i, a := range r.Artifacts {
		if err := validateArtifact("artifacts", i, a); err != nil {
			return err
		}
	}
	if r.RequestedGrant != nil {
		if err := validateGrant(r.RequestedGrant); err != nil {
			return err
		}
	}
	if r.Context != nil {
		if err := validateContext(r); err != nil {
			return err
		}
	}
	if r.Run != nil && !ValidRunCommand(r.Run.Command) {
		return fieldErr("run.command", "must be 1-64 characters from [a-z0-9._-]")
	}
	if r.Created.IsZero() {
		return fieldErr("created", "is required")
	}
	if !r.Deadline.IsZero() && !r.Deadline.After(r.Created) {
		return fieldErr("deadline", "must be later than created")
	}
	return nil
}

var commitmentPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// validateDebate checks the rules of Docs/protocol/debate.md §Request type
// debate: the debate member is present iff type is debate, with its value
// ranges; a debate carries no requested_grant or run; and its topic (the
// brief) also refuses C1 and U+2028/U+2029 (the debate text rule, review 43
// L1).
func validateDebate(r *Request) error {
	if r.Type != TypeDebate {
		if r.Debate != nil {
			return fieldErr("debate", "is allowed only when type is debate")
		}
		return nil
	}
	d := r.Debate
	if d == nil {
		return fieldErr("debate", "is required when type is debate")
	}
	if !commitmentPattern.MatchString(d.Commitment) {
		return fieldErr("debate.commitment", "must be 64 lowercase hex characters")
	}
	if d.Rounds < MinDebateRounds || d.Rounds > MaxDebateRounds {
		return fieldErr("debate.rounds", "must be %d-%d", MinDebateRounds, MaxDebateRounds)
	}
	if d.TurnTimeoutS < MinDebateTurnTimeout || d.TurnTimeoutS > MaxDebateTurnTimeout {
		return fieldErr("debate.turn_timeout_s", "must be %d-%d", MinDebateTurnTimeout, MaxDebateTurnTimeout)
	}
	if r.RequestedGrant != nil {
		return fieldErr("requested_grant", "is not allowed on a debate (debates carry no grants)")
	}
	if r.Run != nil {
		return fieldErr("run", "is not allowed on a debate")
	}
	if hasC1(r.Brief) || strings.ContainsRune(r.Brief, 0x2028) || strings.ContainsRune(r.Brief, 0x2029) {
		return fieldErr("brief", "must not contain control characters other than \\n and \\t")
	}
	return nil
}

// validateContext checks the context files of Docs/protocol/consult.md
// §Context files and §Size limits (the total body cap is CheckSizeFor).
func validateContext(r *Request) error {
	if r.Type != TypeQuestion && r.Type != TypeDebate {
		return fieldErr("context", "is allowed only when type is question or debate")
	}
	if len(r.Context) < minContextFiles || len(r.Context) > maxContextFiles {
		return fieldErr("context", "must hold %d-%d files", minContextFiles, maxContextFiles)
	}
	for i, f := range r.Context {
		nameField := "context[" + itoa(i) + "].name"
		if err := checkBytes(nameField, f.Name, minContextNameBytes, maxContextNameBytes); err != nil {
			return err
		}
		if f.Name == "." || f.Name == ".." || strings.ContainsAny(f.Name, `/\`) || hasControl(f.Name, "") || hasC1(f.Name) {
			return fieldErr(nameField, "must be a base name without / \\ or control characters, not . or ..")
		}
		textField := "context[" + itoa(i) + "].text"
		if err := checkBytes(textField, f.Text, minContextTextBytes, maxContextTextBytes); err != nil {
			return err
		}
		if hasControl(f.Text, "\n\t") || hasC1(f.Text) {
			return fieldErr(textField, "must not contain control characters other than \\n and \\t")
		}
	}
	return nil
}

func validateArtifact(base string, i int, a Artifact) error {
	prefix := func(member string) string { return base + "[" + itoa(i) + "]." + member }
	if a.URL == "" && a.Branch == "" && a.Commit == "" && a.Path == "" {
		return fieldErr(base+"["+itoa(i)+"]", "must have at least one member")
	}
	if a.URL != "" {
		if err := checkURL(prefix("url"), a.URL); err != nil {
			return err
		}
	}
	if a.Branch != "" {
		if err := checkBranch(prefix("branch"), a.Branch); err != nil {
			return err
		}
	}
	if a.Commit != "" {
		if !commitPattern.MatchString(a.Commit) {
			return fieldErr(prefix("commit"), "must be 7-64 lowercase hex characters")
		}
	}
	if a.Path != "" {
		if err := checkBytes(prefix("path"), a.Path, minPathBytes, maxPathBytes); err != nil {
			return err
		}
		if hasControl(a.Path, "") {
			return fieldErr(prefix("path"), "must not contain control characters")
		}
	}
	return nil
}

func artifactIndex(base string, i int) string { return base + "[" + itoa(i) + "]" }
func artifactField(base string, i int, member string) string {
	return base + "[" + itoa(i) + "]." + member
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func checkURL(field, s string) error {
	if err := checkBytes(field, s, minURLBytes, maxURLBytes); err != nil {
		return err
	}
	lower := strings.ToLower(s)
	hasScheme := false
	for _, scheme := range [...]string{"https://", "http://", "ssh://", "git://"} {
		if strings.HasPrefix(lower, scheme) {
			hasScheme = true
			break
		}
	}
	if !hasScheme {
		return fieldErr(field, "must start with https://, http://, ssh:// or git://")
	}
	if strings.ContainsAny(s, " ") || hasControl(s, "") {
		return fieldErr(field, "must not contain spaces or control characters")
	}
	return nil
}

func checkBranch(field, s string) error {
	if err := checkBytes(field, s, minBranchBytes, maxBranchBytes); err != nil {
		return err
	}
	if strings.Contains(s, " ") || hasControl(s, "") || strings.ContainsAny(s, branchBad) {
		return fieldErr(field, "must not contain spaces, control characters or ~ ^ : ? * [ \\")
	}
	if strings.Contains(s, "..") {
		return fieldErr(field, "must not contain \"..\"")
	}
	return nil
}

func validateGrant(g *RequestedGrant) error {
	if !actionPattern.MatchString(g.Action) {
		return fieldErr("requested_grant.action", "must be 1-64 characters from [a-z0-9._-]")
	}
	if err := checkBytes("requested_grant.resource", g.Resource, minGrantResourceBytes, maxGrantResourceBytes); err != nil {
		return err
	}
	if hasControl(g.Resource, "") {
		return fieldErr("requested_grant.resource", "must not contain control characters")
	}
	if g.Note != "" {
		if err := checkCodePoints("requested_grant.note", g.Note, minGrantNoteCodePoints, maxGrantNoteCodePoints, ""); err != nil {
			return err
		}
	}
	return nil
}

// checkCodePoints validates s is valid UTF-8, its code point count is in
// [lo, hi], and it has no control characters except those in allowed.
func checkCodePoints(field, s string, lo, hi int, allowed string) error {
	if !utf8.ValidString(s) {
		return fieldErr(field, "must be valid UTF-8")
	}
	n := utf8.RuneCountInString(s)
	if n < lo || n > hi {
		return fieldErr(field, "must be %d-%d code points", lo, hi)
	}
	if hasControl(s, allowed) {
		return fieldErr(field, "must not contain control characters")
	}
	return nil
}

// checkBytes validates s is valid UTF-8 and its byte length is in [lo, hi].
func checkBytes(field, s string, lo, hi int) error {
	if !utf8.ValidString(s) {
		return fieldErr(field, "must be valid UTF-8")
	}
	if len(s) < lo || len(s) > hi {
		return fieldErr(field, "must be %d-%d bytes", lo, hi)
	}
	return nil
}

// checkBriefBytes validates the brief: 1-16384 bytes of UTF-8, no control
// characters except \n and \t.
func checkBriefBytes(s string) error {
	if err := checkBytes("brief", s, minBriefBytes, maxBriefBytes); err != nil {
		return err
	}
	if hasControl(s, "\n\t") {
		return fieldErr("brief", "must not contain control characters other than \\n and \\t")
	}
	return nil
}

// hasControl reports whether s has a control character (U+0000-U+001F,
// U+007F) not present in allowed.
// hasC1 reports whether s holds a C1 control character (U+0080-U+009F), such
// as U+009B (CSI), which some terminals honour like ESC [. Context files
// refuse them (Docs/protocol/consult.md: "no control characters"); the Phase 1
// fields keep hasControl alone.
func hasC1(s string) bool {
	for _, r := range s {
		if r >= 0x80 && r <= 0x9F {
			return true
		}
	}
	return false
}

func hasControl(s, allowed string) bool {
	for _, r := range s {
		if r == utf8.RuneError {
			return true
		}
		if (r <= 0x1F || r == 0x7F) && !strings.ContainsRune(allowed, r) {
			return true
		}
	}
	return false
}
