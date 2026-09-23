//go:build linux

package notify

import (
	"math"
	"context"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

// notifyIface calls org.freedesktop.Notifications.Notify and
// CloseNotification. It is a package variable so tests can substitute a fake
// session bus without a real notification daemon (Docs/protocol/approval.md
// §Delivering the code, "the daemon calls ... in-process on the session bus
// ..., not through gdbus or notify-send").
var notifyIface dbusNotifier = liveNotifyIface{}

type dbusNotifier interface {
	Notify(ctx context.Context, appName string, replacesID uint32, icon, summary, body string, actions []string, hints map[string]dbus.Variant, expireMS int32) (uint32, error)
	CloseNotification(ctx context.Context, id uint32) error
}

type liveNotifyIface struct{}

// connectSessionBus connects to an existing session bus only. It never
// autolaunches one: godbus's ConnectSessionBus runs `dbus-launch` (found on
// PATH) when no bus address is known, which on a headless machine would start
// and leak a bus daemon per approval that no notification server listens on
// (review 26, L-3). No bus means approval_unavailable
// (Docs/protocol/approval.md §Headless machines).
func connectSessionBus(ctx context.Context) (*dbus.Conn, error) {
	conn, err := dbus.SessionBusPrivateNoAutoStartup(dbus.WithContext(ctx))
	if err != nil {
		return nil, err
	}
	if err := conn.Auth(nil); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := conn.Hello(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func (liveNotifyIface) Notify(ctx context.Context, appName string, replacesID uint32, icon, summary, body string, actions []string, hints map[string]dbus.Variant, expireMS int32) (uint32, error) {
	conn, err := connectSessionBus(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = conn.Close() }()
	obj := conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications")
	call := obj.CallWithContext(ctx, "org.freedesktop.Notifications.Notify", 0,
		appName, replacesID, icon, summary, body, actions, hints, expireMS)
	if call.Err != nil {
		return 0, call.Err
	}
	var id uint32
	if err := call.Store(&id); err != nil {
		return 0, err
	}
	return id, nil
}

func (liveNotifyIface) CloseNotification(ctx context.Context, id uint32) error {
	conn, err := connectSessionBus(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	obj := conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications")
	call := obj.CallWithContext(ctx, "org.freedesktop.Notifications.CloseNotification", 0, id)
	return call.Err
}

// notifIDs maps an approval id to the D-Bus notification id Notify returned,
// so Remove can close the right one.
var (
	notifIDsMu sync.Mutex
	notifIDs   = map[string]uint32{}
)

// showApproval calls Notify in-process (no subprocess, so the code can never
// appear in a child process's argv): a title and body the daemon builds
// itself, passed as Go function arguments over an existing D-Bus connection,
// never through a shell or another process's command line
// (Docs/protocol/approval.md §Delivering the code). No fallback: a failure
// here is reported to the caller as approval_unavailable. The body may be
// interpreted as markup and carries peer-supplied text (a peer's name), so
// &, < and > are escaped as for desktop events: otherwise markup could hide
// the real code or show a fake one, making the human type wrong codes and
// burn the daily wrong-code budget (review 26, M-3).
func showApproval(ctx context.Context, id string, expires time.Time, title, body string) error {
	ms := time.Until(expires).Milliseconds()
	ms = max(0, min(ms, math.MaxInt32))
	expireMS := int32(ms) // clamped to the int32 range above
	dbusID, err := notifyIface.Notify(ctx, "agentnet", 0, "", title, escapeMarkup(body), []string{}, map[string]dbus.Variant{}, expireMS)
	if err != nil {
		return err
	}
	notifIDsMu.Lock()
	notifIDs[id] = dbusID
	notifIDsMu.Unlock()
	return nil
}

func removeApproval(ctx context.Context, id string) {
	notifIDsMu.Lock()
	dbusID, ok := notifIDs[id]
	delete(notifIDs, id)
	notifIDsMu.Unlock()
	if !ok {
		return
	}
	_ = notifyIface.CloseNotification(ctx, dbusID)
}
