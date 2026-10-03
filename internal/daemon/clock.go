package daemon

import (
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/notify"
)

// clockNow is f(), or time.Now() when f is nil. Handlers read the time
// through the Now of the store they call, which the daemon sets from
// Options.Now, so a test drives one clock (R55-F28, review 55 R55-151).
func clockNow(f func() time.Time) time.Time {
	if f == nil {
		return time.Now()
	}
	return f()
}

// webhookClock is wh's clock, or nil (time.Now) without a webhook.
func webhookClock(wh *notify.Webhook) func() time.Time {
	if wh == nil {
		return nil
	}
	return wh.Now
}
