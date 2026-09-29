package notify

import "testing"

// Review 55 R55-105 (C12-04): only an approval's code notification is
// removable; outcome and lock notices are not recorded for removal.
func TestRemovableApprovalIDs(t *testing.T) {
	for id, want := range map[string]bool{
		"a-1234":                true,
		"outcome-a-1234":        false,
		"lock-20260929T120000Z": false,
	} {
		if got := removable(id); got != want {
			t.Errorf("removable(%q) = %v, want %v", id, got, want)
		}
	}
}
