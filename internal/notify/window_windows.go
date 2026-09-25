//go:build windows

package notify

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
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
$form.ClientSize = New-Object System.Drawing.Size(520,300)
$label = New-Object System.Windows.Forms.TextBox
$label.Multiline = $true
$label.ReadOnly = $true
$label.TabStop = $false
$label.WordWrap = $true
$label.ScrollBars = 'Vertical'
$label.Text = $kind + ': ' + $sum
$label.Size = New-Object System.Drawing.Size(490,200)
$label.Location = New-Object System.Drawing.Point(15,10)
$form.Controls.Add($label)
$box = New-Object System.Windows.Forms.TextBox
$box.MaxLength = 6
$box.Size = New-Object System.Drawing.Size(120,24)
$box.Location = New-Object System.Drawing.Point(15,225)
$form.Controls.Add($box)
$guardUntil = [DateTime]::MinValue
$form.Add_Activated({ $script:guardUntil = (Get-Date).AddSeconds(1) })
$box.Add_KeyPress({ if ((Get-Date) -lt $script:guardUntil) { $_.Handled = $true } })
$approve = New-Object System.Windows.Forms.Button
$approve.Text = 'Approve'
$approve.Enabled = $false
$approve.Location = New-Object System.Drawing.Point(250,260)
$approve.Add_Click({ $script:answer = 'approve ' + $box.Text; $form.Close() })
$form.Controls.Add($approve)
$reject = New-Object System.Windows.Forms.Button
$reject.Text = 'Reject'
$reject.Location = New-Object System.Drawing.Point(360,260)
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
// ... and the process's window station to be WinSta0; otherwise the window
// is missing"). x/sys/windows lacks the two user32 calls for the window
// station, so they are bound through a lazy system DLL (review 30, M6).
func inInteractiveSession() bool {
	var sessionID uint32
	pid := os.Getpid()                                                                              // always positive
	if err := windows.ProcessIdToSessionId(uint32(pid), &sessionID); err != nil || sessionID == 0 { //nolint:gosec // os.Getpid() is always positive
		return false
	}
	name, ok := windowStationName()
	return ok && strings.EqualFold(name, "WinSta0")
}

var (
	user32                       = windows.NewLazySystemDLL("user32.dll")
	procGetProcessWindowStation  = user32.NewProc("GetProcessWindowStation")
	procGetUserObjectInformation = user32.NewProc("GetUserObjectInformationW")
)

// uoiName is UOI_NAME for GetUserObjectInformationW.
const uoiName = 2

// windowStationName returns the name of this process's window station.
func windowStationName() (string, bool) {
	if procGetProcessWindowStation.Find() != nil || procGetUserObjectInformation.Find() != nil {
		return "", false
	}
	h, _, _ := procGetProcessWindowStation.Call()
	if h == 0 {
		return "", false
	}
	var buf [256]uint16
	var needed uint32
	r, _, _ := procGetUserObjectInformation.Call(h, uoiName,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)*2), uintptr(unsafe.Pointer(&needed))) //nolint:gosec // fixed local buffers for the Win32 call
	if r == 0 {
		return "", false
	}
	return windows.UTF16ToString(buf[:]), true
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
	// Any failure to create, configure or assign the job kills the process
	// and counts as not ready (Docs/protocol/approval.md, review 29 L1;
	// review 30, M5: a dialog outside the job would outlive the daemon).
	job, err := assignKillOnCloseJob(cmd.Process.Pid)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		h := newDialogHandle(func() {})
		h.markNotReady()
		return h, nil
	}
	// The job handle is closed exactly once, by whichever of Kill and the
	// exit watcher comes first: a second CloseHandle could close an
	// unrelated handle that reused the value (review 30, M4).
	var jobOnce sync.Once
	closeJob := func() { jobOnce.Do(func() { _ = windows.CloseHandle(job) }) }

	handle := newDialogHandle(func() {
		_ = cmd.Process.Kill()
		closeJob()
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
		closeJob()
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

// assignKillOnCloseJob puts pid in a new Job object with KILL_ON_JOB_CLOSE,
// so the dialog dies with the daemon (Docs/protocol/approval.md §The
// approval window, "Lifetime"). Every step must succeed.
func assignKillOnCloseJob(pid int) (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	infoPtr := uintptr(unsafe.Pointer(&info)) //nolint:gosec // fixed struct pointer for the Win32 job-info call
	if _, err := windows.SetInformationJobObject(job, uint32(windows.JobObjectExtendedLimitInformation),
		infoPtr, uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	proc, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid)) //nolint:gosec // os/exec PIDs are always positive
	if err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	defer func() { _ = windows.CloseHandle(proc) }()
	if err := windows.AssignProcessToJobObject(job, proc); err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}
