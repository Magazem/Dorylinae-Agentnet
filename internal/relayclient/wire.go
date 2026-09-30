package relayclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/displaytext"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

// maxErrorMessage bounds a relay error frame's message, the "…" of a cut
// included (OD-R55F9-7: the pairing reason bound).
const maxErrorMessage = 200

// errorFrame is the one conversion of a relay error frame
// (Docs/protocol/envelope.md, "The daemon's reading"): a code outside the
// table becomes relay_error, the message one display-safe line of at most
// 200 bytes, and a ref that is not a valid envelope id is dropped. Every
// consumer sees only its result.
func errorFrame(c envelope.Control) envelope.ErrorFrame {
	code := c.Code
	if !envelope.KnownErrorCode(code) {
		code = envelope.CodeRelayError
	}
	ref := c.Ref
	if !envelope.ValidID(ref) {
		ref = ""
	}
	return envelope.ErrorFrame{Code: code, Message: displaytext.Line(c.Message, maxErrorMessage), Ref: ref}
}

// readControl reads one frame and requires it to be the control frame want.
// An error frame from the relay is returned as its converted
// envelope.ErrorFrame. No relay text is echoed in the other errors.
func readControl(ctx context.Context, conn *websocket.Conn, want string) (envelope.Control, error) {
	typ, frame, err := conn.Read(ctx)
	if err != nil {
		return envelope.Control{}, err
	}
	if typ != websocket.MessageText {
		return envelope.Control{}, errors.New("relay sent a binary frame")
	}
	f, err := envelope.Classify(frame)
	if err != nil || f.Control == nil {
		return envelope.Control{}, fmt.Errorf("expected %q frame from relay", want)
	}
	if f.Control.Op == envelope.OpError {
		return envelope.Control{}, errorFrame(*f.Control)
	}
	if f.Control.Op != want {
		return envelope.Control{}, errors.New("unexpected frame from relay")
	}
	return *f.Control, nil
}

func writeControl(ctx context.Context, conn *websocket.Conn, c envelope.Control) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, raw)
}
