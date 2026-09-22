package presence

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
)

// roundTripJSON marshals v and reparses it with agentcard.ParseStrict, the
// same generic form (json.Number for integers) that mail.Opened.Msg.Body has.
func roundTripJSON(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	gen, err := agentcard.ParseStrict(raw)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := gen.(map[string]any)
	if !ok {
		t.Fatalf("not an object: %v", gen)
	}
	return m
}

func jsonNumber(n int) json.Number { return json.Number(strconv.Itoa(n)) }
