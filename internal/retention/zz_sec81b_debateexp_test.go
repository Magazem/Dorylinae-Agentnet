package retention

// Review 81b proof: a debate's experience record is not in the approved
// count, but a later batch of the same approved run removes it.

import (
	"context"
	"testing"
)

func TestSec81bDebateExperienceNotCounted(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	// The oldest finished request, with a closed debate and its experience
	// record (session = the debate's session, as debate/experience.go writes).
	request(t, db, "in", "P", rid(1), "completed", 40, `{"b":1}`)
	debate(t, db, sid(1), "in", "P", rid(1), "closed", 40, 1, 0)
	experience(t, db, sid(1), "respondent", 40)
	// Enough newer finished requests that the first batch has more.
	for i := 2; i <= MaxRequests+1; i++ {
		request(t, db, "in", "P", rid(i), "completed", 36, `{}`)
	}
	cutoff := Cutoff(now, MinOlderThan)
	dry, err := DryRun(ctx, db, cutoff, now)
	if err != nil {
		t.Fatal(err)
	}
	var total Counts
	for i := 0; i < 5; i++ {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		c, more, err := PruneTx(ctx, tx, cutoff, now)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		total = total.Add(c)
		if !more {
			break
		}
	}
	t.Logf("approved (dry run) experience_records=%d, removed=%d", dry.ExperienceRecords, total.ExperienceRecords)
	if total.ExperienceRecords > dry.ExperienceRecords {
		t.Errorf("removed %d experience records, approved count showed %d", total.ExperienceRecords, dry.ExperienceRecords)
	}
}
