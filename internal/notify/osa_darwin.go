//go:build darwin

package notify

import "encoding/base64"

// osaScriptHeader and osaEnvTextHandler let a fixed osascript read a value
// from its environment without any legacy text encoding: `system attribute`
// may decode the environment in the Mac's primary encoding and garble
// non-ASCII text (review 55 R55-203). The daemon passes each value as
// base64 of its UTF-8 bytes (osaEnv), which `system attribute` reads as
// plain ASCII, and the handler decodes it with Foundation as UTF-8. A value
// that is not valid base64 or UTF-8 raises an error, so osascript exits
// non-zero (R55-F6).
const osaScriptHeader = `use framework "Foundation"
use scripting additions
`

const osaEnvTextHandler = `
on envText(n)
	set d to current application's NSData's alloc()'s initWithBase64EncodedString:(system attribute n) options:0
	if d is missing value then error "AgentNet: " & n & " is not base64" number 1
	set s to current application's NSString's alloc()'s initWithData:d encoding:(current application's NSUTF8StringEncoding)
	if s is missing value then error "AgentNet: " & n & " is not UTF-8" number 1
	return s as text
end envText
`

// osaEnv is one NAME=value environment entry for envText.
func osaEnv(name, value string) string {
	return name + "=" + base64.StdEncoding.EncodeToString([]byte(value))
}
