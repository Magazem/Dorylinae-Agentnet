package request

import "testing"

// R55-F10 A16 (OD-F10-5): the request artifact branch is display-only, so
// it is not tightened like the grant branch; displayTerm covers it. A
// branch holding U+202E is still accepted.
func TestArtifactBranchKeepsHiddenRunes(t *testing.T) {
	if err := checkBranch("artifacts[0].branch", "main"+string(rune(0x202E))); err != nil {
		t.Fatalf("artifact branch with U+202E refused: %v", err)
	}
}
