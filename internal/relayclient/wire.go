package relayclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

// readControl reads one frame and requires it to be the control frame want.
// An error frame from the relay is returned as an envelope.ErrorFrame error.
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
		return envelope.Control{}, envelope.ErrorFrame{Code: f.Control.Code, Message: f.Control.Message, Ref: f.Control.Ref}
	}
	if f.Control.Op != want {
		return envelope.Control{}, fmt.Errorf("expected %q frame from relay, got %q", want, f.Control.Op)
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
