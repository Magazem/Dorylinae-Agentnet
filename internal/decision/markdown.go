package decision

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Render renders a signed Decision file (decision.md §Markdown) to
// deterministic, repository-safe Markdown: UTF-8, LF line endings, one
// trailing newline, no render time, no locale-dependent formatting, fixed
// section order, entries in slot order. It is a pure function of d (the
// parsed "decision" member), hash and the signatures present, plus names
// (petnames, or fingerprints when there is none, e.g. `decision verify
// --md`). Every string that came from an agent or a human is rendered
// inert: fenced code blocks (multi-line) or inline code spans (single-line)
// only, never raw Markdown.
func Render(d map[string]any, hash string, sigInitiator, sigRespondent string, refused bool, names map[string]string, fingerprints map[string]string) ([]byte, error) {
	var b strings.Builder
	complete := sigInitiator != "" && sigRespondent != ""
	// Review 47 L7: B's own peer_refused row has NO signatures (B never
	// signs on a mismatch), so a single-signed export is not always the
	// initiator's: it may be neither side's, this side's own unsigned
	// derivation that it refused to sign as A's claim.
	anySigned := sigInitiator != "" || sigRespondent != ""

	id, _ := d["id"].(string)
	title, _ := strPath(d, "problem", "title")

	writeBanner(&b, complete, anySigned, refused)
	fmt.Fprintf(&b, "# Decision %s : %s\n", id, codeSpan(title))
	b.WriteString("\n")

	signedBy := "initiator and respondent"
	switch {
	case complete:
	case sigInitiator != "":
		signedBy = "initiator only"
	case sigRespondent != "":
		signedBy = "respondent only"
	default:
		signedBy = "none (unsigned)"
	}
	outcome, _ := d["outcome"].(string)
	reason, _ := d["reason"].(string)
	outcomeValue := fmt.Sprintf("%s (%s)", outcome, reason)
	outcomeLine := "Outcome: " + outcomeValue
	switch {
	case complete:
	case anySigned:
		outcomeLine = "Outcome claimed by the initiator: " + outcomeValue
	default:
		outcomeLine = "Outcome: this side's own unsigned record, not proven: " + outcomeValue
	}
	fmt.Fprintf(&b, "- %s ; Signed by: %s\n", outcomeLine, signedBy)

	initName, respName := names["initiator"], names["respondent"]
	initFP, respFP := fingerprints["initiator"], fingerprints["respondent"]
	fmt.Fprintf(&b, "- Participants: initiator %s (fingerprint %s), respondent %s (fingerprint %s)\n",
		codeSpan(initName), initFP, codeSpan(respName), respFP)

	session, _ := d["session"].(string)
	request, _ := d["request"].(string)
	team, _ := d["team"].(string)
	opened, _ := d["opened"].(string)
	closed, _ := d["closed"].(string)
	fmt.Fprintf(&b, "- Session %s, request %s, team %s, opened %s, closed %s, hash %s\n", session, request, team, opened, closed, hash)
	b.WriteString("\n")

	renderProblem(&b, d)
	renderInitialPositions(&b, d)
	renderRounds(&b, d)
	renderFinalPositions(&b, d)
	renderFinalAgreement(&b, d)
	renderRemainingDisagreement(&b, d)
	renderHumanDecisions(&b, d)
	renderAffectedArtifacts(&b, d)
	renderVerification(&b, id, hash, sigInitiator, sigRespondent)

	out := strings.TrimRight(b.String(), "\n") + "\n"
	return []byte(out), nil
}

// Banner is decision.md §Markdown's UNCONFIRMED banner: for a Decision that
// is not signed by both sides (awaiting_peer, peer_refused, or a verified
// file with complete: false, review 43 H1). anySigned distinguishes the
// ordinary single-signed (initiator-only) case from review 47 L7's B-side
// peer_refused row, which has no signature at all. refused adds the
// peer_refused sentence.
func writeBanner(b *strings.Builder, complete, anySigned, refused bool) {
	if complete {
		return
	}
	if anySigned {
		b.WriteString("> UNCONFIRMED: signed by the initiator only. The respondent's entries and the outcome below are the initiator's claim and are not proven.")
	} else {
		b.WriteString("> UNCONFIRMED: unsigned. This is this side's own record, not signed by either party, and is not proven.")
	}
	if refused {
		b.WriteString(" The respondent refused to sign: its record differs.")
	}
	b.WriteString("\n\n")
}

func renderProblem(b *strings.Builder, d map[string]any) {
	writeHeading(b, 2, "Problem")
	topic, _ := strPath(d, "problem", "topic")
	writeFence(b, topic)
	if ctx, ok := arrPath(d, "problem", "context"); ok {
		writeLine(b, "Context files:")
		for _, cv := range ctx {
			c, _ := cv.(map[string]any)
			name, _ := c["name"].(string)
			bytes := numString(c["bytes"])
			sha, _ := c["sha256"].(string)
			fmt.Fprintf(b, "- %s (%s bytes, sha256 %s)\n", codeSpan(name), bytes, sha)
		}
		b.WriteString("\n")
	}
}

func renderInitialPositions(b *strings.Builder, d map[string]any) {
	writeHeading(b, 2, "Initial positions")
	pos, _ := d["positions"].(map[string]any)
	for _, role := range []string{RoleInitiator, RoleRespondent} {
		p, _ := pos[role].(map[string]any)
		initial, _ := p["initial"].(map[string]any)
		if initial == nil {
			continue
		}
		writeHeading(b, 3, titleCase(role))
		renderPositionBody(b, initial)
	}
}

// Every block below ends with a blank line (review 48 M2): without one, a
// label after a list is a lazy continuation of the list's last item
// ("Evidence:" rendered inside the last assumption, and the evidence items
// joining the assumptions list), and a label after a one-line paragraph
// joins that paragraph.
func renderPositionBody(b *strings.Builder, p map[string]any) {
	if claim, ok := p["claim"].(string); ok {
		writeLine(b, "Claim: "+codeSpan(claim))
		b.WriteString("\n")
	}
	if assumptions, ok := p["assumptions"].([]any); ok && len(assumptions) > 0 {
		writeLine(b, "Assumptions:")
		for _, a := range assumptions {
			s, _ := a.(string)
			fmt.Fprintf(b, "- %s\n", codeSpan(s))
		}
		b.WriteString("\n")
	}
	if evidence, ok := p["evidence"].([]any); ok && len(evidence) > 0 {
		writeLine(b, "Evidence:")
		renderEvidenceList(b, evidence)
		b.WriteString("\n")
	}
	if ra, ok := p["rejected_alternatives"].([]any); ok && len(ra) > 0 {
		writeLine(b, "Rejected alternatives:")
		for _, x := range ra {
			o, _ := x.(map[string]any)
			option, _ := o["option"].(string)
			reason, _ := o["reason"].(string)
			fmt.Fprintf(b, "- %s — %s\n", codeSpan(option), codeSpan(reason))
		}
		b.WriteString("\n")
	}
	if arg, ok := p["argument"].(string); ok {
		writeLine(b, "Argument:")
		writeFence(b, arg)
	}
}

func renderEvidenceList(b *strings.Builder, evidence []any) {
	for _, ev := range evidence {
		e, _ := ev.(map[string]any)
		kind, _ := e["kind"].(string)
		ref, _ := e["ref"].(string)
		line := fmt.Sprintf("- %s: %s", codeSpan(kind), codeSpan(ref))
		if note, ok := e["note"].(string); ok && note != "" {
			line += " (note: " + codeSpan(note) + ")"
		}
		b.WriteString(line + "\n")
	}
}

func renderRounds(b *strings.Builder, d map[string]any) {
	rounds, ok := d["rounds"].([]any)
	if !ok || len(rounds) == 0 {
		return
	}
	writeHeading(b, 2, "Rounds")
	for _, rv := range rounds {
		r, _ := rv.(map[string]any)
		n := numString(r["n"])
		writeHeading(b, 3, "Round "+n)
		for _, role := range []string{RoleInitiator, RoleRespondent} {
			mv, ok := r[role].(map[string]any)
			if !ok {
				continue
			}
			writeHeading(b, 4, titleCase(role))
			renderMove(b, mv)
		}
	}
}

func renderMove(b *strings.Builder, mv map[string]any) {
	challenges, _ := mv["challenges"].([]any)
	revision, hasRevision := mv["revision"].(map[string]any)
	if len(challenges) == 0 && !hasRevision {
		writeLine(b, "(pass)")
		b.WriteString("\n")
	}
	for i, cv := range challenges {
		c, _ := cv.(map[string]any)
		targets, _ := c["targets"].([]any)
		tstrs := make([]string, len(targets))
		for j, t := range targets {
			s, _ := t.(string)
			tstrs[j] = codeSpan(s)
		}
		fmt.Fprintf(b, "Challenge %d targets: %s\n\n", i+1, strings.Join(tstrs, ", "))
		if arg, ok := c["argument"].(string); ok {
			writeFence(b, arg)
		}
		if evidence, ok := c["evidence"].([]any); ok && len(evidence) > 0 {
			writeLine(b, "Evidence:")
			renderEvidenceList(b, evidence)
			b.WriteString("\n")
		}
	}
	if hasRevision {
		writeLine(b, "Revision:")
		b.WriteString("\n")
		renderPositionBody(b, revision)
	}
}

func renderFinalPositions(b *strings.Builder, d map[string]any) {
	pos, _ := d["positions"].(map[string]any)
	var hasFinal bool
	for _, role := range []string{RoleInitiator, RoleRespondent} {
		p, _ := pos[role].(map[string]any)
		if _, ok := p["final"]; ok {
			hasFinal = true
		}
	}
	if !hasFinal {
		return
	}
	writeHeading(b, 2, "Final positions")
	for _, role := range []string{RoleInitiator, RoleRespondent} {
		p, _ := pos[role].(map[string]any)
		f, ok := p["final"].(map[string]any)
		if !ok {
			continue
		}
		writeHeading(b, 3, titleCase(role))
		renderPositionBody(b, f)
	}
}

func renderFinalAgreement(b *strings.Builder, d map[string]any) {
	fa, ok := d["final_agreement"].(map[string]any)
	if !ok {
		return
	}
	writeHeading(b, 2, "Final agreement")
	if decisionText, ok := fa["decision"].(string); ok {
		writeLine(b, "Decision: "+codeSpan(decisionText))
		b.WriteString("\n")
	}
	if points, ok := fa["points"].([]any); ok && len(points) > 0 {
		writeLine(b, "Points:")
		for _, p := range points {
			s, _ := p.(string)
			fmt.Fprintf(b, "- %s\n", codeSpan(s))
		}
		b.WriteString("\n")
	}
	if arg, ok := fa["argument"].(string); ok {
		writeLine(b, "Argument:")
		writeFence(b, arg)
	}
}

func renderRemainingDisagreement(b *strings.Builder, d map[string]any) {
	list, ok := d["remaining_disagreement"].([]any)
	if !ok || len(list) == 0 {
		return
	}
	writeHeading(b, 2, "Remaining disagreement")
	for _, dv := range list {
		x, _ := dv.(map[string]any)
		point, _ := x["point"].(string)
		init, _ := x["initiator"].(string)
		resp, _ := x["respondent"].(string)
		fmt.Fprintf(b, "- %s — initiator: %s; respondent: %s\n", codeSpan(point), codeSpan(init), codeSpan(resp))
	}
	b.WriteString("\n")
}

func renderHumanDecisions(b *strings.Builder, d map[string]any) {
	list, ok := d["human_decisions"].([]any)
	if !ok || len(list) == 0 {
		return
	}
	writeHeading(b, 2, "Human decisions and constraints")
	for _, hv := range list {
		h, _ := hv.(map[string]any)
		id, _ := h["id"].(string)
		by, _ := h["by"].(string)
		at, _ := h["at"].(string)
		text, _ := h["text"].(string)
		machine := "the initiator's"
		if by == RoleRespondent {
			machine = "the respondent's"
		}
		fmt.Fprintf(b, "- %s (%s, %s): %s — approved on %s machine; the other side cannot check this\n",
			id, by, at, codeSpan(text), machine)
	}
	b.WriteString("\n")
}

func renderAffectedArtifacts(b *strings.Builder, d map[string]any) {
	list, ok := d["affected_artifacts"].([]any)
	if !ok || len(list) == 0 {
		return
	}
	writeHeading(b, 2, "Affected artifacts")
	for _, av := range list {
		a, _ := av.(map[string]any)
		keys := make([]string, 0, len(a))
		for k := range a {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			s := fmt.Sprint(a[k])
			if sv, ok := a[k].(string); ok {
				s = sv
			}
			parts = append(parts, k+": "+codeSpan(s))
		}
		fmt.Fprintf(b, "- %s\n", strings.Join(parts, ", "))
	}
	b.WriteString("\n")
}

func renderVerification(b *strings.Builder, id, hash, sigI, sigR string) {
	writeHeading(b, 2, "Verification")
	// A list, not bare lines (one paragraph joined by soft breaks), and the
	// signatures in code spans: base64url has '_' and '-', and "-_x…y_-"
	// inside a signature is emphasis, which drops the underscores from the
	// rendered (and copied) text (review 48 L4).
	writeLine(b, "- Hash: `"+hash+"`")
	if sigI != "" {
		writeLine(b, "- Signature (initiator): `"+sigI+"`")
	}
	if sigR != "" {
		writeLine(b, "- Signature (respondent): `"+sigR+"`")
	}
	b.WriteString("\n")
	fmt.Fprintf(b, "Verify with `agentnet decision verify %s.json`\n", id)
}

// --- helpers ---

func writeHeading(b *strings.Builder, level int, text string) {
	b.WriteString(strings.Repeat("#", level) + " " + text + "\n\n")
}

func writeLine(b *strings.Builder, s string) {
	b.WriteString(s + "\n")
}

// codeSpan is decision.md §Markdown's single-line rule: an inline code span
// whose delimiter is a backtick run one longer than the longest run inside
// s (after Visible and TemplateInert), padded with one space on each side.
func codeSpan(s string) string {
	v := TemplateInert(Visible(s, false))
	n := longestBackticks(v) + 1
	delim := strings.Repeat("`", n)
	return delim + " " + v + " " + delim
}

// writeFence is decision.md §Markdown's multi-line rule: a fenced code
// block whose fence is a run of backticks one longer than the longest run
// inside s (after Visible and TemplateInert), at least three, no info
// string, at column 0.
func writeFence(b *strings.Builder, s string) {
	v := TemplateInert(Visible(s, true))
	n := longestBackticks(v) + 1
	if n < 3 {
		n = 3
	}
	fence := strings.Repeat("`", n)
	b.WriteString(fence + "\n")
	b.WriteString(v)
	if !strings.HasSuffix(v, "\n") {
		b.WriteString("\n")
	}
	b.WriteString(fence + "\n\n")
}

func longestBackticks(s string) int {
	longest, cur := 0, 0
	for _, r := range s {
		if r == '`' {
			cur++
			if cur > longest {
				longest = cur
			}
		} else {
			cur = 0
		}
	}
	return longest
}

// titleCase upper-cases the first byte of an ASCII role name ("initiator",
// "respondent"): a local replacement for the deprecated strings.Title.
func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func numString(v any) string {
	switch n := v.(type) {
	case json.Number:
		return n.String()
	case float64:
		return strconv.FormatFloat(n, 'f', -1, 64)
	default:
		return fmt.Sprint(v)
	}
}

func strPath(d map[string]any, keys ...string) (string, bool) {
	var cur any = d
	for _, k := range keys {
		m, ok := cur.(map[string]any)
		if !ok {
			return "", false
		}
		cur, ok = m[k]
		if !ok {
			return "", false
		}
	}
	s, ok := cur.(string)
	return s, ok
}

func arrPath(d map[string]any, keys ...string) ([]any, bool) {
	var cur any = d
	for _, k := range keys {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[k]
		if !ok {
			return nil, false
		}
	}
	a, ok := cur.([]any)
	return a, ok
}
