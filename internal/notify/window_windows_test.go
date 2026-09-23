//go:build windows

package notify

import "testing"

// The window-station half of the interactive-session check (review 30, M6)
// resolves through user32 without opening any window. Its value depends on
// how the test runs (WinSta0 on a desktop, Service-0x0-... under a service),
// so only the call itself is checked.
func TestWindowStationNameResolves(t *testing.T) {
	name, ok := windowStationName()
	if !ok || name == "" {
		t.Fatalf("windowStationName() = %q, %v", name, ok)
	}
}
