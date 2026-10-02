package main

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/Magazem/Dorylinae-Agentnet/internal/displaytext"
)

// writeJSON writes v as json.NewEncoder(w).Encode(v) does (HTML escaping on,
// a trailing newline), with every hidden rune from U+007F up written as a
// \u escape (displaytext.JSON; Docs/protocol/approval.md §Sanitising, JSON
// output, R55-F10). The decoded value is unchanged. It is the encoder of
// every human-facing --json output; identity --json is excluded (review 82b
// F4).
func writeJSON(w io.Writer, v any) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(v); err != nil {
		return
	}
	_, _ = w.Write(displaytext.JSON(buf.Bytes()))
}
