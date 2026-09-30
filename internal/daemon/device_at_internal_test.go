package daemon

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/device"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
)

// apply runs one mail from C through kind k and returns Apply's error.
func (e *offerEnv) apply(k mail.Kind, kind string, body map[string]any) error {
	e.t.Helper()
	ctx := context.Background()
	op := &mail.Opened{Msg: mail.Msg{From: "C", Kind: kind, Body: body}}
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := k.Apply(ctx, tx, op); err != nil {
		return err
	}
	return tx.Commit()
}

// Review 68 A8 (R55-152): device.link and device.unlink "at" is a wire time
// that must round-trip exactly; an offset, a fraction or a lower-case z is
// bad_body.
func TestDeviceAtIsStrictWireTime(t *testing.T) {
	e := newOfferEnv(t)
	base := e.now.Add(-time.Minute).UTC()
	day := base.Format("2006-01-02T15:04:05")
	bad := []string{day + "+00:00", day + ".5Z", day + "z"}
	link := deviceLinkKind(e.ds, "H", e.hooks())
	unlink := deviceUnlinkKind(e.ds, nil)
	for _, at := range bad {
		offer := e.offer(base)
		offer["at"] = at
		if err := e.apply(link, device.KindLink, offer); !errors.Is(err, mail.ErrBadBody) {
			t.Errorf("device.link at=%q: err = %v, want bad_body", at, err)
		}
		if err := e.apply(unlink, device.KindUnlink, map[string]any{"at": at}); !errors.Is(err, mail.ErrBadBody) {
			t.Errorf("device.unlink at=%q: err = %v, want bad_body", at, err)
		}
	}
	good := day + "Z"
	e.intent()
	offer := e.offer(base)
	offer["at"] = good
	if err := e.apply(link, device.KindLink, offer); err != nil {
		t.Fatalf("device.link at=%q: %v", good, err)
	}
	if n := e.count(`SELECT COUNT(*) FROM device_links WHERE state = 'active'`); n != 1 {
		t.Fatal("a valid offer did not activate the link")
	}
	if err := e.apply(unlink, device.KindUnlink, map[string]any{"at": good}); err != nil {
		t.Fatalf("device.unlink at=%q: %v", good, err)
	}
}
