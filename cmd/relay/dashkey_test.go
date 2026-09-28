package main

import (
	"encoding/base64"
	"path/filepath"
	"strings"
	"testing"
)

// dashKey is a syntactically valid base64url identity key (32 bytes) that
// starts with '-': about 1 in 64 real identity keys do, since base64url
// index 62 ('-') is a legal leading 6-bit group. Commands below bind it via
// accountsDB(t, dashKey), so `relay admin account unbind` on it is expected
// to succeed (exit 0), not fail with a usage error: reaching that success is
// proof the CLI parsed it as a value, not as a flag.
var dashKey = func() string {
	raw := make([]byte, 32)
	raw[0] = 0xf8 // top 6 bits 111110 = base64url index 62 = '-'
	return base64.RawURLEncoding.EncodeToString(raw)
}()

// dashDashKey starts with two consecutive '-' characters, covering keys
// that would look like a "--flag" long-form token, not just a "-x" short
// one.
var dashDashKey = func() string {
	raw := make([]byte, 32)
	raw[0] = 0xfb // top 6 bits 111110 = index 62 = '-'; low 2 bits 11
	raw[1] = 0xe0 // top 4 bits 1110; combined with byte 0's low 2 bits: 111110 = index 62 = '-'
	return base64.RawURLEncoding.EncodeToString(raw)
}()

func TestDashPrefixedKeyIsNotTreatedAsAFlag(t *testing.T) {
	if !strings.HasPrefix(dashKey, "-") {
		t.Fatalf("test setup: dashKey = %q, want a leading '-'", dashKey)
	}
	if !strings.HasPrefix(dashDashKey, "--") {
		t.Fatalf("test setup: dashDashKey = %q, want a leading '--'", dashDashKey)
	}

	for _, key := range []string{dashKey, dashDashKey} {
		key := key
		t.Run(key, func(t *testing.T) {
			cases := []struct {
				name string
				args func(db, journal string) []string
			}{
				{"key first", func(db, journal string) []string {
					return []string{"account", "unbind", key, "--db", db, "--security-journal", journal}
				}},
				{"key last", func(db, journal string) []string {
					return []string{"account", "unbind", "--db", db, "--security-journal", journal, key}
				}},
				{"-- form", func(db, journal string) []string {
					return []string{"account", "unbind", "--db", db, "--security-journal", journal, "--", key}
				}},
			}
			for _, c := range cases {
				t.Run(c.name, func(t *testing.T) {
					db := accountsDB(t, key) // fresh binding per subtest: unbind is destructive
					journal := filepath.Join(filepath.Dir(db), "journal.jsonl")
					args := c.args(db, journal)
					if code, out, errOut := admin(t, args...); code != 0 {
						t.Fatalf("%v: code %d, out %q, err %q", args, code, out, errOut)
					}
					if got := queryOne(t, db, `SELECT COUNT(*) FROM account_keys`); got != "0" {
						t.Fatalf("account_keys count = %q, want 0", got)
					}
				})
			}
		})
	}
}
