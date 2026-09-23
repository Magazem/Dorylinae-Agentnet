//go:build windows

package notify

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
	"unsafe"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
	"golang.org/x/sys/windows"
)

// windowReadyTimeout is "wait at most 10 s" for the Windows dialog
// (Docs/protocol/approval.md §The approval window, per-platform table).
const windowReadyTimeout = 10 * time.Second

// approvalWindowScript is fixed PowerShell/WinForms source. Tag, kind and
// summary reach it only through the environment (base64 UTF-16, like the
// toast), never interpolated into the script or passed on the command line
// (Docs/protocol/approval.md §The approval window, "Fixed program, fixed
// script"). The code is never sent to this script at all.
//
// It writes "ready" to stdout from the form's Shown event, then, once the
// human answers, exactly one final line: "approve <text>", "reject" or
// "dismiss" (window closed).
const approvalWindowScript = `
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Windows.Forms
$tag  = [System.Text.Encoding]::Unicode.GetString([Convert]::FromBase64String($env:AGENTNET_W_TAG))
$kind = [System.Text.Encoding]::Unicode.GetString([Convert]::FromBase64String($env:AGENTNET_W_KIND))
$sum  = [System.Text.Encoding]::Unicode.GetString([Convert]::FromBase64String($env:AGENTNET_W_SUMMARY))
$timeoutMs = [int]$env:AGENTNET_W_TIMEOUT_MS
$form = New-Object System.Windows.Forms.Form
$form.Text = "AgentNet approval $tag"
$form.TopMost = $true
$form.FormBorderStyle = 'FixedDialog'
$form.MinimizeBox = $false
$form.MaximizeBox = $false
$form.ClientSize = New-Object System.Drawing.Size(420,160)
$label = New-Object System.Windows.Forms.Label
$label.Text = $kind + ': ' + $sum
$label.AutoSize = $false
$label.Size = New-Object System.Drawing.Size(390,70)
$label.Location = New-Object System.Drawing.Point(15,10)
$form.Controls.Add($label)
$box = New-Object System.Windows.Forms.TextBox
$box.MaxLength = 6
$box.Size = New-Object System.Drawing.Size(120,24)
$box.Location = New-Object System.Drawing.Point(15,90)
$form.Controls.Add($box)
$guardUntil = [DateTime]::MinValue
$form.Add_Activated({ $script:guardUntil = (Get-Date).AddSeconds(1) })
$box.Add_KeyPress({ if ((Get-Date) -lt $script:guardUntil) { $_.Handled = $true } })
$approve = New-Object System.Windows.Forms.Button
$approve.Text = 'Approve'
$approve.Enabled = $false
$approve.Location = New-Object System.Drawing.Point(150,125)
$approve.Add_Click({ $script:answer = 'approve ' + $box.Text; $form.Close() })
$form.Controls.Add($approve)
$reject = New-Object System.Windows.Forms.Button
$reject.Text = 'Reject'
$reject.Location = New-Object System.Drawing.Point(260,125)
$reject.Add_Click({ $script:answer = 'reject'; $form.Close() })
$form.Controls.Add($reject)
$box.Add_TextChanged({ $approve.Enabled = ($box.Text.Length -eq 6) })
$form.AcceptButton = $null
$form.CancelButton = $null
$script:answer = 'dismiss'
$expireTimer = New-Object System.Windows.Forms.Timer
$expireTimer.Interval = [Math]::Max(1, $timeoutMs)
$expireTimer.Add_Tick({ $expireTimer.Stop(); $script:answer = 'dismiss'; $form.Close() })
$form.Add_Shown({ [Console]::Out.WriteLine('ready'); [Console]::Out.Flush(); $box.Focus(); $expireTimer.Start() })
[void]$form.ShowDialog()
$expireTimer.Stop()
[Console]::Out.WriteLine($script:answer)
`

// powershellPath is the fixed absolute path to Windows PowerShell 5.1,
// derived from GetSystemDirectory rather than %SystemRoot% or PATH
// (Docs/protocol/approval.md §The approval window, Windows row; review 29 L2).
func powershellPath() (string, error) {
	dir, err := windows.GetSystemDirectory()
	if err != nil {
		return "", err
	}
	return dir + `\WindowsPowerShell\v1.0\powershell.exe`, nil
}

// inInteractiveSession reports whether this process can show a window a
// human would see: not session 0 (Docs/protocol/approval.md §The approval
// window, Windows row: "the check first requires ProcessIdToSessionId != 0
// ...; otherwise the window is missing"). The additional window-station
// check (WinSta0) from the same sentence needs user32 bindings
// golang.org/x/sys/windows does not export; the session check alone already
// catches session 0 and an SSH logon, the cases the spec calls out, and the
// manual check (tests/phase2-manual.md) covers the rest on a real desktop.
func inInteractiveSession() bool {
	var sessionID uint32
	pid := os.Getpid()                                                                              // always positive
	if err := windows.ProcessIdToSessionId(uint32(pid), &sessionID); err != nil || sessionID == 0 { //nolint:gosec // os.Getpid() is always positive
		return false
	}
	return true
}

func startDialog(ctx context.Context, _, tag, kind, summary string, expires time.Time) (approval.WindowHandle, error) {
	if !inInteractiveSession() {
		h := newDialogHandle(func() {})
		h.markNotReady()
		return h, nil
	}
	psPath, err := powershellPath()
	if err != nil {
		h := newDialogHandle(func() {})
		h.markNotReady()
		return h, nil
	}
	timeoutMs := time.Until(expires).Milliseconds()
	if timeoutMs < 1 {
		timeoutMs = 1
	}
	env := append(os.Environ(),
		"AGENTNET_W_TAG="+utf16Base64(tag),
		"AGENTNET_W_KIND="+utf16Base64(kind),
		"AGENTNET_W_SUMMARY="+utf16Base64(summary),
		fmt.Sprintf("AGENTNET_W_TIMEOUT_MS=%d", timeoutMs),
	)
	cmd := exec.CommandContext(ctx, psPath, //nolint:gosec // fixed absolute path; args are fixed flags, script is constant text
		"-NoProfile", "-NonInteractive", "-STA", "-WindowStyle", "Hidden", "-Command", approvalWindowScript)
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		h := newDialogHandle(func() {})
		h.markNotReady()
		return h, nil
	}
	if err := cmd.Start(); err != nil {
		h := newDialogHandle(func() {})
		h.markNotReady()
		return h, nil
	}

	// A Job object with KILL_ON_JOB_CLOSE, so the dialog dies with the
	// daemon even if this handle is dropped without an explicit Kill
	// (Docs/protocol/approval.md §The approval window, "Lifetime", Windows).
	job, jerr := windows.CreateJobObject(nil, nil)
	jobOK := jerr == nil
	if jobOK {
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
			BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
				LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
			},
		}
		infoPtr := uintptr(unsafe.Pointer(&info)) //nolint:gosec // fixed struct pointer for the Win32 job-info call
		_, _ = windows.SetInformationJobObject(job, uint32(windows.JobObjectExtendedLimitInformation),
			infoPtr, uint32(unsafe.Sizeof(info)))
		pid := cmd.Process.Pid                                                                                           // always positive
		procHandle, oerr := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid)) //nolint:gosec // os/exec PIDs are always positive
		if oerr == nil {
			if aerr := windows.AssignProcessToJobObject(job, procHandle); aerr != nil {
				// A failed assignment kills the process and counts as not
				// ready (Docs/protocol/approval.md, review 29 L1).
				_ = windows.CloseHandle(procHandle)
				_ = windows.CloseHandle(job)
				_ = cmd.Process.Kill()
				h := newDialogHandle(func() {})
				h.markNotReady()
				return h, nil
			}
			_ = windows.CloseHandle(procHandle)
		}
	}

	handle := newDialogHandle(func() {
		_ = cmd.Process.Kill()
		if jobOK {
			_ = windows.CloseHandle(job)
		}
	})

	go func() {
		sc := bufio.NewScanner(stdout)
		first := true
		for sc.Scan() {
			line := sc.Text()
			if first {
				first = false
				if line == "ready" {
					handle.markReady()
					continue
				}
			}
			handle.deliver(parseAnswerLine(line))
			break
		}
		_ = cmd.Wait()
		handle.markNotReady() // no-op if already ready
		handle.deliver(dialogAnswer{kind: "dismiss"})
		if jobOK {
			_ = windows.CloseHandle(job)
		}
	}()

	// Ready within 10 s (Docs/protocol/approval.md, Windows row).
	go func() {
		t := time.NewTimer(windowReadyTimeout)
		defer t.Stop()
		select {
		case <-handle.readyCh:
		case <-t.C:
			handle.markNotReady()
		}
	}()

	return handle, nil
}
