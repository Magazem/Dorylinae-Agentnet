//go:build windows

package notify

import (
	"context"
	"time"
)

// approvalGroup tags every approval toast so Remove can find it later
// (Docs/protocol/approval.md §Delivering the code, "carries a tag ... and
// group agentnet-approval").
const approvalGroup = "agentnet-approval"

// approvalToastScript mirrors windowsToastScript but adds Tag, Group and
// ExpirationTime, and reads title/body from the environment (as
// windowsToastScript already does), never the command line.
const approvalToastScript = `
$ErrorActionPreference = 'Stop'
$title = [System.Text.Encoding]::Unicode.GetString([Convert]::FromBase64String($env:AGENTNET_A_TITLE))
$body  = [System.Text.Encoding]::Unicode.GetString([Convert]::FromBase64String($env:AGENTNET_A_BODY))
$titleXml = [Security.SecurityElement]::Escape($title)
$bodyXml  = [Security.SecurityElement]::Escape($body)
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType=WindowsRuntime] | Out-Null
[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType=WindowsRuntime] | Out-Null
$template = "<toast><visual><binding template='ToastGeneric'><text>{0}</text><text>{1}</text></binding></visual></toast>" -f $titleXml, $bodyXml
$xml = New-Object Windows.Data.Xml.Dom.XmlDocument
$xml.LoadXml($template)
$toast = New-Object Windows.UI.Notifications.ToastNotification $xml
$toast.Tag = $env:AGENTNET_A_TAG
$toast.Group = $env:AGENTNET_A_GROUP
$toast.ExpirationTime = [DateTimeOffset]::Parse($env:AGENTNET_A_EXPIRES)
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('` + windowsAppUserModelID + `').Show($toast)
`

// approvalRemoveScript withdraws a toast by tag and group from the
// notification history (Docs/protocol/approval.md §Delivering the code,
// "the daemon removes it from the notification history").
const approvalRemoveScript = `
$ErrorActionPreference = 'Stop'
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType=WindowsRuntime] | Out-Null
[Windows.UI.Notifications.ToastNotificationManager]::History.Remove($env:AGENTNET_A_TAG, $env:AGENTNET_A_GROUP, '` + windowsAppUserModelID + `')
`

func showApproval(ctx context.Context, id string, expires time.Time, title, body string) error {
	env := []string{
		"AGENTNET_A_TITLE=" + utf16Base64(title),
		"AGENTNET_A_BODY=" + utf16Base64(body),
		"AGENTNET_A_TAG=" + id,
		"AGENTNET_A_GROUP=" + approvalGroup,
		"AGENTNET_A_EXPIRES=" + expires.UTC().Format(time.RFC3339),
	}
	args := []string{"-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", approvalToastScript}
	return run(ctx, "powershell.exe", args, env)
}

func removeApproval(ctx context.Context, id string) {
	env := []string{
		"AGENTNET_A_TAG=" + id,
		"AGENTNET_A_GROUP=" + approvalGroup,
	}
	args := []string{"-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", approvalRemoveScript}
	_ = run(ctx, "powershell.exe", args, env)
}
