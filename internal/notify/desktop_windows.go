//go:build windows

package notify

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"unicode/utf16"
)

// windowsAppUserModelID is PowerShell's own AUMID, required for a toast shown
// by a script host (Docs/protocol/notify.md §Desktop).
const windowsAppUserModelID = `{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe`

// windowsToastScript is fixed PowerShell source: it never contains peer
// text. The title and body arrive through the environment (base64,
// UTF-16LE) and are XML-escaped inside the script, so peer text never
// appears on the command line or in the script source
// (Docs/protocol/notify.md §Desktop). The toast XML uses single-quoted
// attributes so the script needs no backtick escaping.
const windowsToastScript = `
$ErrorActionPreference = 'Stop'
$title = [System.Text.Encoding]::Unicode.GetString([Convert]::FromBase64String($env:AGENTNET_N_TITLE))
$body  = [System.Text.Encoding]::Unicode.GetString([Convert]::FromBase64String($env:AGENTNET_N_BODY))
$titleXml = [Security.SecurityElement]::Escape($title)
$bodyXml  = [Security.SecurityElement]::Escape($body)
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType=WindowsRuntime] | Out-Null
[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType=WindowsRuntime] | Out-Null
$template = "<toast><visual><binding template='ToastGeneric'><text>{0}</text><text>{1}</text></binding></visual></toast>" -f $titleXml, $bodyXml
$xml = New-Object Windows.Data.Xml.Dom.XmlDocument
$xml.LoadXml($template)
$toast = New-Object Windows.UI.Notifications.ToastNotification $xml
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('` + windowsAppUserModelID + `').Show($toast)
`

// showDesktop runs a fixed PowerShell script that shows a toast, with title
// and body passed through the environment, never the command line
// (Docs/protocol/notify.md §Desktop). It needs the interactive session; the
// Task Scheduler task runs "only when user is logged on".
func showDesktop(ctx context.Context, title, body string) error {
	env := []string{
		"AGENTNET_N_TITLE=" + utf16Base64(title),
		"AGENTNET_N_BODY=" + utf16Base64(body),
	}
	args := []string{"-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", windowsToastScript}
	return run(ctx, "powershell.exe", args, env)
}

// utf16Base64 encodes s as UTF-16LE, then standard base64.
func utf16Base64(s string) string {
	units := utf16.Encode([]rune(s))
	raw := make([]byte, len(units)*2)
	for i, u := range units {
		binary.LittleEndian.PutUint16(raw[i*2:], u)
	}
	return base64.StdEncoding.EncodeToString(raw)
}
