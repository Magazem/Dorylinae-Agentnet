//go:build !windows && !linux && !darwin

package notify

import (
	"context"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
)

func startDialog(context.Context, string, string, string, string, time.Time) (approval.WindowHandle, error) {
	h := newDialogHandle(func() {})
	h.markNotReady()
	return h, nil
}
