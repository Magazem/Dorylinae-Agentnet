package daemon_test

import (
	"strings"
	"testing"
)

// D22 (review 36 L5): each device shows a content-free desktop notification
// when its link becomes active, naming the other device and its role, in
// either confirmation order (the first confirmer activates on the mail, the
// second inside its own approval).
func TestDeviceLinkActivationNotifies(t *testing.T) {
	ctrl, help := newDevPair(t)
	activatePair(t, ctrl, help)
	want := map[*devNode]string{
		ctrl: "desktop is now linked as your helper|",
		help: "laptop is now linked as your controller|",
	}
	for n, text := range want {
		harnessWait(t, n.name+"'s activation notification", func() bool {
			for _, s := range n.shown.all() {
				if s == text {
					return true
				}
			}
			return false
		})
		count := 0
		for _, s := range n.shown.all() {
			if strings.Contains(s, "is now linked") {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("%s showed %d activation notifications: %v", n.name, count, n.shown.all())
		}
	}
	// It can be switched off like any event.
	var res map[string]any
	help.call("notify_set", map[string]any{"events": map[string]bool{"device.linked": false}}, &res)
}
