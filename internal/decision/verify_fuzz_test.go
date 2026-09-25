package decision_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/debate"
	"github.com/Magazem/Dorylinae-Agentnet/internal/decision"
)

// FuzzVerifyAndRender (review 48): `decision verify [--md]` runs offline on
// any file a user is handed. Whatever the bytes, Verify must not panic, a
// valid result must carry the initiator's signature (and the respondent's
// exactly when complete), and rendering it must not panic or leave template
// syntax. The seeds are the spec vector, single- and double-signed.
func FuzzVerifyAndRender(f *testing.F) {
	v := readVector(f)
	f.Add(signedFile(f, vectorObject(f, v), map[string]string{"initiator": v.sigI, "respondent": v.sigR}, v.hash))
	f.Add(signedFile(f, vectorObject(f, v), map[string]string{"initiator": v.sigI}, v.hash))
	f.Add([]byte(`{"decision":{},"hash":"","signatures":{}}`))
	schema := debate.DecisionSchema()
	f.Fuzz(func(t *testing.T, data []byte) {
		res := decision.Verify(data, schema)
		if !res.Valid {
			return
		}
		if res.SigInitiator == "" || res.Complete != (res.SigRespondent != "") {
			t.Fatalf("valid result with signatures %q/%q, complete %v", res.SigInitiator, res.SigRespondent, res.Complete)
		}
		var d map[string]any
		dec := json.NewDecoder(bytes.NewReader(res.Decision))
		dec.UseNumber()
		if err := dec.Decode(&d); err != nil {
			t.Fatalf("canonical decision does not parse: %v", err)
		}
		md, err := decision.Render(d, res.Hash, res.SigInitiator, res.SigRespondent, false, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(md, []byte("{{")) || bytes.Contains(md, []byte("{%")) {
			t.Fatal("rendered Markdown holds template syntax")
		}
	})
}
