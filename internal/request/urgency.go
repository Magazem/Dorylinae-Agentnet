package request

import "time"

// Urgency guards (OD-P1-4, Docs/protocol/request.md §Urgency guards (1.7)):
// 5 high and 2 blocking requests per sender per rolling 7 days, enforced on
// both the sender (a courtesy, global across peers) and the receiver (per
// sender, tamper-proof). Weighting the resulting priority by the sender's
// acceptance-as-urgent rate (OD-P1-5) is internal/request/priority.go and
// internal/request/inbox.go.

// urgencyBudgetWindow is the rolling window both budgets use.
const urgencyBudgetWindow = 7 * 24 * time.Hour

// budgetLimit returns the count limit and its note text for an urgency that
// can be budgeted (high or blocking).
func budgetLimit(urgency string) (limit int, budget string) {
	if urgency == UrgencyBlocking {
		return 2, "2 blocking requests"
	}
	return 5, "5 high requests"
}

// submitUrgencyNote is the submit result's urgency_note when the sender-side
// budget downgraded urgency (Docs/protocol/request.md §Urgency guards (1.7)).
func submitUrgencyNote(urgency string) string {
	_, budget := budgetLimit(urgency)
	return "sent as normal: your weekly budget of " + budget + " is used"
}

// urgencyNote derives the inbox's urgency_note from downgradedBy and the
// declared urgency (Docs/protocol/request.md §Urgency guards (1.7)).
func urgencyNote(downgradedBy, declared string) string {
	if downgradedBy == "" {
		return ""
	}
	_, budget := budgetLimit(declared)
	if downgradedBy == "sender" {
		return "sent as normal: the sender's weekly budget of " + budget + " was used"
	}
	return "shown as normal: this sender has used its weekly budget of " + budget + " to you"
}
