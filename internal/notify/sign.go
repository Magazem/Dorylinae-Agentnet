package notify

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
)

// Sign computes the Standard-Webhooks-style signature
// (Docs/protocol/notify.md §Signature):
//
//	signed_content = "v1:" + timestamp + ":" + id + ":" + body
//	signature      = base64url_nopad(HMAC-SHA256(secret, signed_content))
//
// timestamp is decimal unix seconds and body is the exact bytes sent.
func Sign(secret []byte, id string, timestamp int64, body []byte) string {
	signed := signedContent(id, timestamp, body)
	mac := hmac.New(sha256.New, secret)
	mac.Write(signed)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func signedContent(id string, timestamp int64, body []byte) []byte {
	prefix := fmt.Sprintf("v1:%d:%s:", timestamp, id)
	out := make([]byte, 0, len(prefix)+len(body))
	out = append(out, prefix...)
	out = append(out, body...)
	return out
}

// Verify recomputes the signature over id/timestamp/body and compares it in
// constant time against sig (the value of the "v1=" component of the
// Dorylinae-Signature header). It is provided for receiver implementations
// and tests; the daemon only signs (Docs/protocol/notify.md
// §Signature "Receiver verification").
func Verify(secret []byte, id string, timestamp int64, body []byte, sig string) bool {
	want := Sign(secret, id, timestamp, body)
	return subtle.ConstantTimeCompare([]byte(want), []byte(sig)) == 1
}

// ParseSignatureHeader extracts the base64url signature from a
// "Dorylinae-Signature: v1=<sig>" header value.
func ParseSignatureHeader(header string) (sig string, ok bool) {
	const prefix = "v1="
	if len(header) <= len(prefix) || header[:len(prefix)] != prefix {
		return "", false
	}
	return header[len(prefix):], true
}

// FormatTimestamp renders unix seconds as the decimal string used in the
// Dorylinae-Webhook-Timestamp header and the signed content.
func FormatTimestamp(unixSeconds int64) string {
	return strconv.FormatInt(unixSeconds, 10)
}
