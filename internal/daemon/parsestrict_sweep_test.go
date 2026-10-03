package daemon_test

// R55-F31 T12 (review 76 I2): the ParseStrict code rule says never to decode
// data that passed ParseStrict into a struct with encoding/json. After the
// sweep, capability/fetch.go no longer does it for the peer's grant token, and
// each of the six sites that decode our own stored canonical bodies says why
// that is safe, on the lines above the decode.

import (
	"os"
	"strings"
	"testing"
)

func TestParseStrictSweep(t *testing.T) {
	b, err := os.ReadFile("../capability/fetch.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, banned := range []string{"json.Unmarshal(token", "json.NewDecoder(bytes.NewReader(token", `Sig string ` + "`" + `json:"sig"` + "`"} {
		if strings.Contains(src, banned) {
			t.Errorf("capability/fetch.go decodes the verified token into a struct again (%q)", banned)
		}
	}

	const why1 = "// Decoding into a struct is safe here: this is our own stored canonical body, schema-checked"
	const why2 = "// by exact name before it was stored (ParseStrict code rule, review 76 I2)."
	for _, site := range []struct{ file, decode string }{
		{"../debate/view.go", "json.Unmarshal([]byte(r.closeBody.String), &cb)"},
		{"../debate/view.go", "json.Unmarshal([]byte(r.lastState.String), &ls)"},
		{"../debate/apply.go", "json.Unmarshal([]byte(r.lastState.String), &payload)"},
		{"../debate/constraint.go", "json.NewDecoder(bytes.NewReader([]byte(r.closeBody.String)))"},
		{"../worksession/receive.go", "json.Unmarshal([]byte(row.lastState.String), &payload)"},
		{"../request/cancel.go", "json.Unmarshal([]byte(row.lastReply.String), &payload)"},
	} {
		raw, err := os.ReadFile(site.file)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
		at := -1
		for i, l := range lines {
			if strings.Contains(l, site.decode) {
				at = i
			}
		}
		if at < 2 {
			t.Errorf("%s: the decode %q is not there any more", site.file, site.decode)
			continue
		}
		if !strings.Contains(lines[at-2], why1) || !strings.Contains(lines[at-1], why2) {
			t.Errorf("%s: the two lines above %q do not carry the safety comment:\n%s\n%s", site.file, site.decode, lines[at-2], lines[at-1])
		}
	}
}
