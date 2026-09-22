package request

// UrgencyBase maps an effective urgency to the priority formula's base
// (Docs/protocol/request.md §Effective priority).
func UrgencyBase(urgency string) int {
	switch urgency {
	case UrgencyLow:
		return 1
	case UrgencyNormal:
		return 2
	case UrgencyHigh:
		return 3
	case UrgencyBlocking:
		return 4
	default:
		return 0
	}
}

// Priority is the effective priority formula (OD-P1-5,
// Docs/protocol/request.md §Effective priority):
//
//	priority = base * 1000                                      if base <= 2
//	priority = 2000 + ((base-2) * 1000 * (a+2)) / (n+2)         if base >= 3   (integer division)
//
// n is the sender's count of high/blocking in rows with a first response
// over the last 30 days, and a is how many of those were accepted.
func Priority(base, n, a int) int {
	if base <= 2 {
		return base * 1000
	}
	return 2000 + ((base-2)*1000*(a+2))/(n+2)
}
