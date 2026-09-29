package pathid

import (
	"os"
	"testing"
)

// The data volume root is a mount point reached across a firmlink, with no
// device change seen from "..": statfs finds it (review 61 F7-S1).
func TestIsRootDataVolume(t *testing.T) {
	const data = "/System/Volumes/Data"
	if _, err := os.Stat(data); err != nil {
		t.Skipf("no data volume: %v", err)
	}
	if ok, err := IsRoot(data); err != nil || !ok {
		t.Errorf("IsRoot(%q) = %v, %v; want true", data, ok, err)
	}
	if ok, err := IsRoot(data + "/Users"); err != nil || ok {
		t.Errorf("IsRoot(%q) = %v, %v; want false", data+"/Users", ok, err)
	}
}
