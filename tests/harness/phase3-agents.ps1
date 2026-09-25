#Requires -Version 5.1
<#
.SYNOPSIS
  Ticket 3.H: drives a debate (Docs/protocol/debate.md) between two REAL
  headless coding-agent harnesses (Claude Code and agy), turn-driven
  (OD-P3-9): this script polls `agentnet debate <id> --json` and, whenever
  it is a side's turn, runs that side's agent headless ONCE with a plain
  prompt, then polls again. `-Harness standin` (the default) runs the
  scripted Go stand-in (tests/harness/standin -mode debate) instead of a
  real agent, for CI: it drives both an agreed debate and a forced
  escalation (`-disagree`, deterministic only with the stand-in, OD-P3-7/3.5).

.DESCRIPTION
  Reuses the Phase 2 scaffolding unchanged: a loopback relay and two
  agentnetd daemons (A, B) with separate --home directories and
  DORYLINAE_APPROVAL=terminal / DORYLINAE_DEBUG=1, so this SCRIPT (never the
  agent under test) reads each daemon's approval codes from its stderr and
  answers them on its stdin (Docs/protocol/approval.md §Headless machines);
  pairing and a team as in tests/harness/phase2-agents.ps1. The fixture is a
  small repository with two plausible designs of one function, so the agents
  have something to argue.

  For `-Harness real`: the first prompt run for A names the question and
  asks for a debate with at most 2 rounds; the first prompt for B says a
  teammate invited it to a debate; every later prompt (either side) is the
  plain "Your AgentNet debate with <peer> is waiting for you. Take your next
  step, then stop." Prompts never name an agentnet subcommand: the agent
  learns them from Docs/agents/snippet.md and --help. After both positions
  exist, THIS SCRIPT (not an agent) runs `agentnet debate <id> --constrain
  "..."` on A, and A's already-running approval pump answers the code, as a
  human would.

.PARAMETER RepoRoot
  Path to the repo checkout (worktree) this script builds and tests against.

.PARAMETER SkipBuild
  Skip `go build`; use the binaries already in <RepoRoot>/bin.

.PARAMETER OnlyRound
  Run only round 1 or round 2 (1 or 2). Default: both.

.PARAMETER Harness
  `standin` (default; the scripted Go program, free, used by weekly CI) or
  `real` (Claude Code and agy, each once as initiator and once as
  respondent, per the ticket; needs logged-in CLIs and paid API access --
  run by hand, never in CI).

.EXAMPLE
  pwsh tests/harness/phase3-agents.ps1                     # standin, CI-safe
  pwsh tests/harness/phase3-agents.ps1 -Harness real        # real agents
#>
[CmdletBinding()]
param(
    [string]$RepoRoot = "",
    [switch]$SkipBuild,
    [ValidateSet(1, 2)]
    [int]$OnlyRound = 0,
    [int]$RelayPortBase = 18897,
    [int]$AgentTimeoutSeconds = 420,
    [int]$TotalTimeoutSeconds = 1500,
    [ValidateSet("standin", "real")]
    [string]$Harness = "standin",
    [ValidateSet("claude", "agy", "codex")]
    [string]$InitiatorHarness,
    [ValidateSet("claude", "agy", "codex")]
    [string]$RespondentHarness,
    [int]$MaxAttempts = 1
)

$ErrorActionPreference = "Stop"
$scriptStart = Get-Date

# $PSScriptRoot can be empty under Windows PowerShell 5.1 (e.g. `powershell -File`
# on CI), so resolve the repo root here rather than in the param() default.
if (-not $RepoRoot) {
    $scriptDir = $PSScriptRoot
    if (-not $scriptDir -and $MyInvocation.MyCommand.Path) {
        $scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
    }
    if ($scriptDir) {
        $RepoRoot = (Resolve-Path (Join-Path $scriptDir "..\..")).Path
    } else {
        $RepoRoot = (& git rev-parse --show-toplevel).Trim()
    }
}

function Write-Step($msg) { Write-Host "[3.H] $msg" -ForegroundColor Cyan }
function Write-Fail($msg) { Write-Host "[3.H] FAIL: $msg" -ForegroundColor Red }
function Write-Ok($msg) { Write-Host "[3.H] OK: $msg" -ForegroundColor Green }

function Stop-ProcessTree {
    # See tests/harness/phase1-agents.ps1: Process.Kill($true) needs .NET
    # Core/5+, unavailable under Windows PowerShell 5.1's .NET Framework.
    param([System.Diagnostics.Process]$Process)
    if ($null -eq $Process) { return }
    try {
        if (-not $Process.HasExited) {
            & taskkill.exe /PID $Process.Id /T /F 2>&1 | Out-Null
        }
    } catch {}
}

function Format-ArgList {
    # System.Diagnostics.ProcessStartInfo.ArgumentList does not exist under
    # .NET Framework; build a single quoted Arguments string by hand.
    param([string[]]$ArgList)
    $parts = foreach ($a in $ArgList) {
        if ($a -match '[\s"]') { '"' + ($a -replace '"', '\"') + '"' } else { $a }
    }
    return ($parts -join ' ')
}

# ---------------------------------------------------------------------------
# Process helpers (unchanged from tests/harness/phase2-agents.ps1)
# ---------------------------------------------------------------------------

function Start-PipedProc {
    <# Starts a process with redirected stdin/stdout/stderr, all readable/
       writable by this script (used for agentnetd in terminal-approval mode:
       Docs/protocol/approval.md says the SCRIPT, never the agent, reads the
       codes from stderr and writes them to stdin). #>
    param([string]$FilePath, [string[]]$ArgList, [hashtable]$Env, [string]$WorkDir)
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $FilePath
    $psi.Arguments = Format-ArgList $ArgList
    $psi.WorkingDirectory = $WorkDir
    $psi.RedirectStandardInput = $true
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    $psi.UseShellExecute = $false
    foreach ($k in $Env.Keys) { $psi.Environment[$k] = $Env[$k] }
    $p = New-Object System.Diagnostics.Process
    $p.StartInfo = $psi
    $null = $p.Start()
    # Drain stdout (discarded) so the daemon can never block on a full pipe.
    $p.BeginOutputReadLine()
    return $p
}

function Stop-PipedProc {
    param([System.Diagnostics.Process]$Process)
    if ($null -eq $Process) { return }
    try { if (-not $Process.HasExited) { $Process.StandardInput.Close() } } catch {}
    try {
        if (-not $Process.HasExited) {
            $Process.CloseMainWindow() | Out-Null
            Start-Sleep -Milliseconds 300
            if (-not $Process.HasExited) { Stop-ProcessTree -Process $Process }
        }
    } catch {}
}

function Invoke-CliJson {
    param([string]$AgentnetExe, [string]$HomeDir, [string[]]$CliArgs, [int]$TimeoutMs = 8000)
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $AgentnetExe
    $psi.Arguments = Format-ArgList ($CliArgs + "--json")
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    $psi.UseShellExecute = $false
    $psi.Environment["DORYLINAE_HOME"] = $HomeDir
    $p = [System.Diagnostics.Process]::Start($psi)
    $stdout = $p.StandardOutput.ReadToEnd()
    $stderr = $p.StandardError.ReadToEnd()
    if (-not $p.WaitForExit($TimeoutMs)) { Stop-ProcessTree -Process $p; throw "agentnet $($CliArgs -join ' ') timed out" }
    $obj = $null
    if ($stdout.Trim().Length -gt 0) {
        try { $obj = $stdout | ConvertFrom-Json } catch { }
    }
    return [pscustomobject]@{ ExitCode = $p.ExitCode; Stdout = $stdout; Stderr = $stderr; Json = $obj }
}

function Wait-Until {
    param([scriptblock]$Cond, [int]$TimeoutSeconds = 30, [int]$PollMs = 500, [string]$What = "condition")
    $sw = [Diagnostics.Stopwatch]::StartNew()
    while ($sw.Elapsed.TotalSeconds -lt $TimeoutSeconds) {
        $r = & $Cond
        if ($r) { return $r }
        Start-Sleep -Milliseconds $PollMs
    }
    throw "timed out waiting for $What"
}

function Get-Python3 {
    foreach ($cand in @("python3", "python")) {
        $c = Get-Command $cand -ErrorAction SilentlyContinue
        if ($c) {
            $v = & $c.Source -c "import sqlite3" 2>$null
            if ($LASTEXITCODE -eq 0) { return $c.Source }
        }
    }
    return $null
}

function Get-AuditActions {
    param([string]$Python, [string]$DbPath)
    if (-not (Test-Path $DbPath)) { return @() }
    $code = "import sqlite3,sys; con=sqlite3.connect(sys.argv[1]); print(chr(10).join(r[0] for r in con.execute('select action from audit_events order by id')))"
    $out = & $Python -c $code $DbPath 2>$null
    if ($LASTEXITCODE -ne 0) { return @() }
    return @($out -split "`n" | Where-Object { $_.Trim().Length -gt 0 })
}

# ---------------------------------------------------------------------------
# Approval pump (2.2d headless machines, unchanged): reads "AgentNet approval
# a-XXXXXX: <summary>. Code NNNNNN. ..." off a daemon's stderr and writes
# "<tag> <code>" to its stdin. Runs as a background thread per daemon so the
# script does not block draining stderr while the agents run. Used here both
# for grant-shaped approvals (none in a debate, OD-P3-4) and for the
# `debate_constraint` approval (3.4): same mechanism, no debate-specific code.
# ---------------------------------------------------------------------------

Add-Type -TypeDefinition @"
using System;
using System.Diagnostics;
using System.IO;
using System.Text.RegularExpressions;
using System.Threading;
public class ApprovalPump {
    public static Thread Start(Process daemon, string logPath) {
        Thread t = new Thread(delegate() {
            Regex re = new Regex(@"AgentNet approval (a-[0-9a-f]+): .*?Code ([0-9]+)[.]");
            try {
                string line;
                while ((line = daemon.StandardError.ReadLine()) != null) {
                    try { File.AppendAllText(logPath, line + Environment.NewLine); } catch (IOException) {}
                    Match m = re.Match(line);
                    if (m.Success) {
                        byte[] b = new System.Text.UTF8Encoding(false).GetBytes("\n" + m.Groups[1].Value + " " + m.Groups[2].Value + "\n");
                        daemon.StandardInput.BaseStream.Write(b, 0, b.Length);
                        daemon.StandardInput.BaseStream.Flush(); try { File.AppendAllText(logPath, "SENT: " + BitConverter.ToString(b) + Environment.NewLine); } catch (IOException) {}
                    }
                }
            } catch (Exception e) { try { File.AppendAllText(logPath, "PUMP ERROR: " + e + Environment.NewLine); } catch (IOException) {} }
        });
        t.IsBackground = true;
        t.Start();
        return t;
    }
}
"@

function Start-ApprovalPump {
    param([System.Diagnostics.Process]$DaemonProcess, [string]$LogPath)
    return [ApprovalPump]::Start($DaemonProcess, $LogPath)
}

# ---------------------------------------------------------------------------
# Snippet (unchanged helper from tests/harness/phase2-agents.ps1)
# ---------------------------------------------------------------------------

function Get-SnippetBody {
    $snippetFile = Join-Path $RepoRoot "Docs\agents\snippet.md"
    $raw = Get-Content -Raw -Path $snippetFile
    if ($raw -match '(?s)```markdown\r?\n(.*?)\r?\n```') { return $Matches[1] }
    throw "could not extract the fenced snippet block from $snippetFile"
}

# ---------------------------------------------------------------------------
# Real-agent process helpers (unchanged from tests/harness/phase2-agents.ps1,
# used here for ONE turn at a time rather than one long-running session:
# OD-P3-9 (a) runs the agent whose turn it is, once per turn).
# ---------------------------------------------------------------------------

function Start-Agent {
    param([string]$Tool, [string]$Prompt, [string]$WorkDir, [string]$BinDir, [string]$HomeDir, [int]$TimeoutSeconds, [string]$LogPrefix)

    $env = @{ DORYLINAE_HOME = $HomeDir; PATH = "$BinDir;$env:PATH" }
    switch ($Tool) {
        "claude" {
            $exe = Join-Path $env:USERPROFILE ".local\bin\claude.exe"
            if (-not (Test-Path $exe)) { $cmd = Get-Command claude -ErrorAction SilentlyContinue; if ($cmd) { $exe = $cmd.Source } }
            if (-not (Test-Path $exe)) { return @{ Ran = $false; Reason = "claude executable not found" } }
            $argList = @($Prompt, "-p", "--restricted", "--tools", "PowerShell", "--allowedTools", "PowerShell(agentnet *)", "PowerShell(Start-Sleep *)",
                "--permission-prompts", "none", "--output-format", "json",
                "--strict-mcp-config", "--mcp-config", '{"mcpServers":{}}', "--setting-sources", "project")
        }
        "codex" {
            $cmd = Get-Command codex -ErrorAction SilentlyContinue
            if (-not $cmd) { return @{ Ran = $false; Reason = "codex executable not found" } }
            $exe = $cmd.Source
            $argList = @("exec", "--skip-git-repo-check", "--sandbox", "workspace-write", "-C", $WorkDir, "--json", $Prompt)
        }
        "agy" {
            $exe = Join-Path $env:LOCALAPPDATA "agy\bin\agy.exe"
            if (-not (Test-Path $exe)) { $cmd = Get-Command agy -ErrorAction SilentlyContinue; if ($cmd) { $exe = $cmd.Source } }
            if (-not (Test-Path $exe)) { return @{ Ran = $false; Reason = "agy executable not found" } }
            # --sandbox is deliberately NOT used: it raises a Windows UAC prompt
            # headless mode cannot answer on a machine without admin rights
            # (confirmed for ticket 1.H; tests/harness/README.md).
            $argList = @("-p", $Prompt, "--output-format", "json", "--print-timeout", "${TimeoutSeconds}s",
                "--dangerously-skip-permissions", "--disable-slash-commands", "--add-dir", $WorkDir)
        }
        default { return @{ Ran = $false; Reason = "unknown harness $Tool" } }
    }

    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $exe
    $psi.Arguments = Format-ArgList $argList
    $psi.WorkingDirectory = $WorkDir
    $psi.RedirectStandardInput = $true
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    $psi.UseShellExecute = $false
    foreach ($k in $env.Keys) { $psi.Environment[$k] = $env[$k] }
    $p = [System.Diagnostics.Process]::Start($psi)
    # A headless agent may wait for stdin to close (claude -p does); give it EOF.
    $p.StandardInput.Close()
    return @{ Ran = $true; Tool = $Tool; Process = $p; Out = $p.StandardOutput.ReadToEndAsync(); Err = $p.StandardError.ReadToEndAsync(); LogPrefix = $LogPrefix; TimeoutSeconds = $TimeoutSeconds; Reason = "started" }
}

function Wait-Agent {
    param($Handle)
    if (-not $Handle.Ran) { return $Handle }
    $Tool = $Handle.Tool; $p = $Handle.Process; $LogPrefix = $Handle.LogPrefix; $TimeoutSeconds = $Handle.TimeoutSeconds
    $finished = $p.WaitForExit($TimeoutSeconds * 1000)
    if (-not $finished) {
        Stop-ProcessTree -Process $p
        Set-Content -Path "$LogPrefix.timeout.log" -Value "timed out after $TimeoutSeconds s"
        try { Set-Content -Path "$LogPrefix.stdout.log" -Value $Handle.Out.Result -Encoding utf8; Set-Content -Path "$LogPrefix.stderr.log" -Value $Handle.Err.Result -Encoding utf8 } catch {}
        return @{ Ran = $false; Reason = "$Tool timed out after $TimeoutSeconds s" }
    }
    $out = $Handle.Out.Result
    $err = $Handle.Err.Result
    Set-Content -Path "$LogPrefix.stdout.log" -Value $out -Encoding utf8
    Set-Content -Path "$LogPrefix.stderr.log" -Value $err -Encoding utf8

    $blockedPatterns = @("usage limit", "rate limit", "not logged in", "authentic", "quota")
    $combined = "$out`n$err"
    foreach ($pat in $blockedPatterns) {
        if ($combined -match [regex]::Escape($pat)) {
            return @{ Ran = $false; Reason = "$Tool reported: $pat (see $LogPrefix.std{out,err}.log)" }
        }
    }
    if ($p.ExitCode -ne 0) {
        return @{ Ran = $false; Reason = "$Tool exited $($p.ExitCode) (see $LogPrefix.std{out,err}.log)" }
    }
    return @{ Ran = $true; Reason = "ok" }
}

function Invoke-AgentTurn {
    <# Runs one real-agent turn and returns @{Ok=$bool; Reason=...}. #>
    param([string]$Tool, [string]$Prompt, [string]$WorkDir, [string]$BinDir, [string]$HomeDir, [int]$TimeoutSeconds, [string]$LogPrefix)
    $h = Start-Agent -Tool $Tool -Prompt $Prompt -WorkDir $WorkDir -BinDir $BinDir -HomeDir $HomeDir -TimeoutSeconds $TimeoutSeconds -LogPrefix $LogPrefix
    $r = Wait-Agent $h
    if (-not $r.Ran) { return @{ Ok = $false; Reason = $r.Reason } }
    return @{ Ok = $true; Reason = "ok" }
}

# ---------------------------------------------------------------------------
# One round
# ---------------------------------------------------------------------------

function Invoke-Round {
    param(
        [int]$RoundNum,
        [string]$InitiatorTool,
        [string]$RespondentTool,
        [string]$Scenario,       # "agreed" or "escalated" (standin only)
        [string]$AgentnetExe,
        [string]$StandinExe,
        [string]$RunDir,
        [int]$RelayPort,
        [string]$Python
    )

    $result = [ordered]@{ Round = $RoundNum; Initiator = $InitiatorTool; Respondent = $RespondentTool; Scenario = $Scenario; Pass = $false; Reason = ""; Outcome = ""; Turns = 0; DurationSeconds = 0 }
    $roundStart = Get-Date

    $relayHome = Join-Path $RunDir "relay"
    $aHome = Join-Path $RunDir "a-home"
    $bHome = Join-Path $RunDir "b-home"
    $aWork = Join-Path $RunDir "a-work"
    $bWork = Join-Path $RunDir "b-work"
    $fixtureDir = Join-Path $RunDir "fixture"
    foreach ($d in @($relayHome, $aHome, $bHome, $aWork, $bWork, $fixtureDir)) { New-Item -ItemType Directory -Force -Path $d | Out-Null }

    # A small repository with two plausible designs of one function, so the
    # agents have something to argue (ticket 3.H).
    $notesPath = Join-Path $fixtureDir "NOTES.md"
    Set-Content -Path $notesPath -Encoding utf8 -Value @'
# Two candidate designs for `Debounce`

This tiny repo has two candidate implementations of a `Debounce(fn, delay)`
helper that should call `fn` only after `delay` has passed with no new calls.

## Design A (`design_a.go`): a single timer, reset on every call

Simple: one `time.Timer`, `Stop()` and re-`Reset()` it on every call. Easy to
read, but every call takes a lock to touch the shared timer.

## Design B (`design_b.go`): a generation counter, no timer reset

Each call bumps an atomic generation counter and starts its own
`time.AfterFunc`; when it fires, it only calls `fn` if its generation is still
the newest. No shared timer to reset, but it spawns one goroutine per call
under heavy load.

Pick whichever you think is the better default and defend it.
'@
    Set-Content -Path (Join-Path $fixtureDir "design_a.go") -Encoding utf8 -Value @'
package debounce

import (
	"sync"
	"time"
)

// Design A: one timer, reset on every call.
func Debounce(fn func(), delay time.Duration) func() {
	var mu sync.Mutex
	var timer *time.Timer
	return func() {
		mu.Lock()
		defer mu.Unlock()
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(delay, fn)
	}
}
'@
    Set-Content -Path (Join-Path $fixtureDir "design_b.go") -Encoding utf8 -Value @'
package debounce

import (
	"sync/atomic"
	"time"
)

// Design B: a generation counter, no shared timer to reset.
func Debounce(fn func(), delay time.Duration) func() {
	var gen int64
	return func() {
		g := atomic.AddInt64(&gen, 1)
		time.AfterFunc(delay, func() {
			if atomic.LoadInt64(&gen) == g {
				fn()
			}
		})
	}
}
'@

    $relayExe = Join-Path $RepoRoot "bin\relay.exe"
    $daemonExe = Join-Path $RepoRoot "bin\agentnetd.exe"

    $handles = @{}
    $pumps = @()
    try {
        Write-Step "round $RoundNum ($InitiatorTool -> $RespondentTool, $Scenario): starting relay on 127.0.0.1:$RelayPort"
        $relayPsi = New-Object System.Diagnostics.ProcessStartInfo
        $relayPsi.FileName = $relayExe
        $relayPsi.Arguments = Format-ArgList @("--listen", "127.0.0.1:$RelayPort", "--queue-db", (Join-Path $relayHome "relay-queue.db"))
        $relayPsi.WorkingDirectory = $relayHome
        $relayPsi.RedirectStandardOutput = $true
        $relayPsi.RedirectStandardError = $true
        $relayPsi.UseShellExecute = $false
        $handles.relay = [System.Diagnostics.Process]::Start($relayPsi)
        Start-Sleep -Milliseconds 500
        if ($handles.relay.HasExited) { throw "relay exited immediately" }

        $relayUrl = "ws://127.0.0.1:$RelayPort"
        Write-Step "starting daemon A (agent-a) and daemon B (agent-b) with DORYLINAE_APPROVAL=terminal DORYLINAE_DEBUG=1"
        $handles.daemonA = Start-PipedProc -FilePath $daemonExe -ArgList @("--relay", $relayUrl) `
            -Env @{ DORYLINAE_HOME = $aHome; DORYLINAE_AGENT_NAME = "agent-a"; DORYLINAE_APPROVAL = "terminal"; DORYLINAE_DEBUG = "1" } -WorkDir $aHome
        $handles.daemonB = Start-PipedProc -FilePath $daemonExe -ArgList @("--relay", $relayUrl) `
            -Env @{ DORYLINAE_HOME = $bHome; DORYLINAE_AGENT_NAME = "agent-b"; DORYLINAE_APPROVAL = "terminal"; DORYLINAE_DEBUG = "1" } -WorkDir $bHome

        $pumps += Start-ApprovalPump -DaemonProcess $handles.daemonA -LogPath (Join-Path $RunDir "daemonA.approvals.log")
        $pumps += Start-ApprovalPump -DaemonProcess $handles.daemonB -LogPath (Join-Path $RunDir "daemonB.approvals.log")

        Wait-Until -What "daemon A ready" -TimeoutSeconds 15 -Cond {
            $r = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $aHome -CliArgs @("status")
            $r.ExitCode -eq 0
        } | Out-Null
        Wait-Until -What "daemon B ready" -TimeoutSeconds 15 -Cond {
            $r = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $bHome -CliArgs @("status")
            $r.ExitCode -eq 0
        } | Out-Null
        Write-Ok "both daemons answer status"

        Write-Step "pairing A and B"
        $newCode = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $aHome -CliArgs @("pair", "--new")
        if (-not $newCode.Json -or -not $newCode.Json.ok) { throw "pair --new failed: $($newCode.Stdout) $($newCode.Stderr)" }
        $code = $newCode.Json.code
        $redeem = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $bHome -CliArgs @("pair", $code)
        if (-not $redeem.Json -or $redeem.Json.state -ne "complete") { throw "pair redeem did not complete: $($redeem.Stdout)" }
        Write-Ok "paired"

        Write-Step "creating team t3h on A and inviting B"
        $team = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $aHome -CliArgs @("team", "create", "t3h")
        if (-not $team.Json -or -not $team.Json.ok) { throw "team create failed: $($team.Stdout)" }
        $invite = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $aHome -CliArgs @("team", "invite", "t3h")
        $join = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $bHome -CliArgs @("team", "join", $invite.Json.code)
        if (-not $join.Json -or -not $join.Json.ok) { throw "team join failed: $($join.Stdout)" }
        Wait-Until -What "roster to reach 2 members on A" -TimeoutSeconds 20 -Cond {
            $show = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $aHome -CliArgs @("team", "show", "t3h")
            $show.Json -and $show.Json.team -and @($show.Json.team.members).Count -ge 2
        } | Out-Null
        Write-Ok "team t3h has both members"

        $idemKey = "t3h-r$RoundNum-debate-$([guid]::NewGuid().ToString('N').Substring(0,10))"
        $constraintText = "Keep the debounce helper dependency-free (standard library only)."

        if ($InitiatorTool -eq "standin") {
            Write-Step "invoking stand-in A and B concurrently ($Scenario)"
            $disagreeFlag = @()
            if ($Scenario -eq "escalated") { $disagreeFlag = @("-disagree") }
            $aArgs = @("-mode", "debate", "-agentnet", $AgentnetExe, "-home", $aHome, "-role", "a", "-peer", "agent-b",
                "-topic", "Which Debounce design should we use: a single reset timer, or a generation counter?",
                "-claim", "Use design A (a single reset timer).",
                "-argument", "It is easier to read and audit, and the lock it takes is uncontended in the common case.",
                "-rounds", "2", "-debate-key", $idemKey, "-timeout", "$AgentTimeoutSeconds")
            $bArgs = @("-mode", "debate", "-agentnet", $AgentnetExe, "-home", $bHome, "-role", "b",
                "-claim", "Use design B (a generation counter).",
                "-argument", "It never blocks on a shared timer, which matters under heavy call rates.",
                "-timeout", "$AgentTimeoutSeconds") + $disagreeFlag
            $aPsi = New-Object System.Diagnostics.ProcessStartInfo
            $aPsi.FileName = $StandinExe; $aPsi.Arguments = Format-ArgList $aArgs
            $aPsi.RedirectStandardOutput = $true; $aPsi.RedirectStandardError = $true; $aPsi.UseShellExecute = $false
            $bPsi = New-Object System.Diagnostics.ProcessStartInfo
            $bPsi.FileName = $StandinExe; $bPsi.Arguments = Format-ArgList $bArgs
            $bPsi.RedirectStandardOutput = $true; $bPsi.RedirectStandardError = $true; $bPsi.UseShellExecute = $false
            $aProc = [System.Diagnostics.Process]::Start($aPsi)
            $bProc = [System.Diagnostics.Process]::Start($bPsi)
            $aOut = $aProc.StandardOutput.ReadToEndAsync(); $aErr = $aProc.StandardError.ReadToEndAsync()
            $bOut = $bProc.StandardOutput.ReadToEndAsync(); $bErr = $bProc.StandardError.ReadToEndAsync()

            # Run the human constraint on A once both positions exist, concurrently
            # with the stand-ins driving the rest of the debate.
            $sessionId = $null
            Wait-Until -What "the debate session id to appear on A" -TimeoutSeconds 30 -Cond {
                $lst = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $aHome -CliArgs @("debates")
                if ($lst.Json -and @($lst.Json.debates).Count -ge 1) { $script:sessionId = $lst.Json.debates[0].session; return $true }
                return $false
            } | Out-Null
            $sessionId = $script:sessionId
            Wait-Until -What "both positions to exist (phase past positions)" -TimeoutSeconds ([Math]::Min($AgentTimeoutSeconds, 60)) -Cond {
                $show = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $aHome -CliArgs @("debate", $sessionId)
                $show.Json -and $show.Json.debate -and @("invited", "positions") -notcontains $show.Json.debate.phase
            } | Out-Null
            $constrainResp = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $aHome -CliArgs @("debate", $sessionId, "--constrain", $constraintText)
            if (-not $constrainResp.Json -or -not $constrainResp.Json.ok) { throw "--constrain failed: $($constrainResp.Stdout) $($constrainResp.Stderr)" }

            $aFinished = $aProc.WaitForExit($AgentTimeoutSeconds * 1000)
            $bFinished = $bProc.WaitForExit($AgentTimeoutSeconds * 1000)
            Set-Content -Path (Join-Path $RunDir "standin-a.stdout.log") -Value $aOut.Result -Encoding utf8
            Set-Content -Path (Join-Path $RunDir "standin-a.stderr.log") -Value $aErr.Result -Encoding utf8
            Set-Content -Path (Join-Path $RunDir "standin-b.stdout.log") -Value $bOut.Result -Encoding utf8
            Set-Content -Path (Join-Path $RunDir "standin-b.stderr.log") -Value $bErr.Result -Encoding utf8
            if (-not $aFinished) { Stop-ProcessTree -Process $aProc }
            if (-not $bFinished) { Stop-ProcessTree -Process $bProc }
            if (-not $aFinished -or -not $bFinished) { $result.Reason = "stand-in timed out (a finished=$aFinished, b finished=$bFinished)"; return [pscustomobject]$result }
            if ($aProc.ExitCode -ne 0) { $result.Reason = "stand-in A exited $($aProc.ExitCode): $($aErr.Result)"; return [pscustomobject]$result }
            if ($bProc.ExitCode -ne 0) { $result.Reason = "stand-in B exited $($bProc.ExitCode): $($bErr.Result)"; return [pscustomobject]$result }
        } else {
            $snippet = Get-SnippetBody
            $aSnippetFile = if ($InitiatorTool -eq "claude") { "CLAUDE.md" } else { "AGENTS.md" }
            $bSnippetFile = if ($RespondentTool -eq "claude") { "CLAUDE.md" } else { "AGENTS.md" }
            Set-Content -Path (Join-Path $aWork $aSnippetFile) -Value $snippet -Encoding utf8
            Set-Content -Path (Join-Path $bWork $bSnippetFile) -Value $snippet -Encoding utf8
            Copy-Item -Path $notesPath -Destination (Join-Path $aWork "NOTES.md") -Force
            Copy-Item -Path $notesPath -Destination (Join-Path $bWork "NOTES.md") -Force

            $binDir = Join-Path $RepoRoot "bin"
            $startPrompt = "You are working with a teammate whose AgentNet peer name is agent-b, on the shared team t3h. Read NOTES.md in your working directory: it describes two candidate designs of one function. Start a debate with agent-b about which design is better, arguing for whichever you judge stronger, with at most 2 rounds. Use exactly this idempotency key so a retry never starts it twice: $idemKey Then stop; you will be asked to take further steps in the same debate later."
            $joinPrompt = "A teammate's agent (agent-b) is you; a teammate (agent-a) invited you to an AgentNet debate. Read NOTES.md in your working directory: it describes two candidate designs of one function. Check for the debate and take your next step in it, arguing for whichever design you judge stronger. Then stop; you will be asked to take further steps in the same debate later."
            $followUpPrompt = "Your AgentNet debate with your teammate is waiting for you. Take your next step, then stop."

            $sessionId = $null
            $aStarted = $false
            $bJoined = $false
            $constraintDone = $false
            $turnCount = 0
            $maxTurns = 14
            $deadline = (Get-Date).AddSeconds([Math]::Min($AgentTimeoutSeconds * 8, $TotalTimeoutSeconds - 60))

            while ($true) {
                if ((Get-Date) -gt $deadline) { $result.Reason = "turn-driven loop exceeded its deadline (phase not yet closed)"; return [pscustomobject]$result }
                if ($turnCount -ge $maxTurns) { $result.Reason = "gave up after $maxTurns agent turns without the debate closing"; return [pscustomobject]$result }

                if (-not $aStarted) {
                    Write-Step "turn $($turnCount+1): initiator ($InitiatorTool) starts the debate"
                    $r = Invoke-AgentTurn -Tool $InitiatorTool -Prompt $startPrompt -WorkDir $aWork -BinDir $binDir -HomeDir $aHome -TimeoutSeconds $AgentTimeoutSeconds -LogPrefix (Join-Path $RunDir "turn$($turnCount+1)-a-start")
                    $turnCount++
                    if (-not $r.Ok) { $result.Reason = "initiator ($InitiatorTool) could not start the debate: $($r.Reason)"; return [pscustomobject]$result }
                    $aStarted = $true
                    continue
                }

                if (-not $sessionId) {
                    $lst = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $aHome -CliArgs @("debates")
                    if ($lst.Json -and @($lst.Json.debates).Count -ge 1) { $sessionId = $lst.Json.debates[0].session }
                    else { Start-Sleep -Milliseconds 1000; continue }
                }

                $aView = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $aHome -CliArgs @("debate", $sessionId)
                if (-not $aView.Json -or -not $aView.Json.debate) { Start-Sleep -Milliseconds 1000; continue }
                $phase = $aView.Json.debate.phase
                if (@("closed", "broken") -contains $phase) { break }

                if (-not $bJoined) {
                    $bList = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $bHome -CliArgs @("debates", "--phase", "invited")
                    if ($bList.Json -and @($bList.Json.debates).Count -ge 1) {
                        Write-Step "turn $($turnCount+1): respondent ($RespondentTool) joins the debate"
                        $r = Invoke-AgentTurn -Tool $RespondentTool -Prompt $joinPrompt -WorkDir $bWork -BinDir $binDir -HomeDir $bHome -TimeoutSeconds $AgentTimeoutSeconds -LogPrefix (Join-Path $RunDir "turn$($turnCount+1)-b-join")
                        $turnCount++
                        if (-not $r.Ok) { $result.Reason = "respondent ($RespondentTool) could not join the debate: $($r.Reason)"; return [pscustomobject]$result }
                        $bJoined = $true
                        continue
                    }
                    Start-Sleep -Milliseconds 1000
                    continue
                }

                if (-not $constraintDone -and @("invited", "positions") -notcontains $phase) {
                    Write-Step "adding a human constraint on A (the script, not an agent, as OD-P3-3 requires)"
                    $constrainResp = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $aHome -CliArgs @("debate", $sessionId, "--constrain", $constraintText)
                    if (-not $constrainResp.Json -or -not $constrainResp.Json.ok) { $result.Reason = "--constrain failed: $($constrainResp.Stdout) $($constrainResp.Stderr)"; return [pscustomobject]$result }
                    $constraintDone = $true
                    Start-Sleep -Milliseconds 1500
                    continue
                }

                if ($aView.Json.debate.turn -eq "you") {
                    Write-Step "turn $($turnCount+1): initiator ($InitiatorTool)'s turn (expect $($aView.Json.debate.expect))"
                    $r = Invoke-AgentTurn -Tool $InitiatorTool -Prompt $followUpPrompt -WorkDir $aWork -BinDir $binDir -HomeDir $aHome -TimeoutSeconds $AgentTimeoutSeconds -LogPrefix (Join-Path $RunDir "turn$($turnCount+1)-a")
                    $turnCount++
                    if (-not $r.Ok) { $result.Reason = "initiator ($InitiatorTool) turn failed: $($r.Reason)"; return [pscustomobject]$result }
                    continue
                }

                $bView = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $bHome -CliArgs @("debate", $sessionId)
                if ($bView.Json -and $bView.Json.debate -and $bView.Json.debate.turn -eq "you") {
                    Write-Step "turn $($turnCount+1): respondent ($RespondentTool)'s turn (expect $($bView.Json.debate.expect))"
                    $r = Invoke-AgentTurn -Tool $RespondentTool -Prompt $followUpPrompt -WorkDir $bWork -BinDir $binDir -HomeDir $bHome -TimeoutSeconds $AgentTimeoutSeconds -LogPrefix (Join-Path $RunDir "turn$($turnCount+1)-b")
                    $turnCount++
                    if (-not $r.Ok) { $result.Reason = "respondent ($RespondentTool) turn failed: $($r.Reason)"; return [pscustomobject]$result }
                    continue
                }

                Start-Sleep -Milliseconds 1000
            }
            $result.Turns = $turnCount
        }

        # --- assertions, independent of agent/stand-in prose --------------
        Write-Step "asserting outcome from --json and audit logs"
        if (-not $sessionId) {
            $lst = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $aHome -CliArgs @("debates")
            if (-not $lst.Json -or @($lst.Json.debates).Count -lt 1) { $result.Reason = "no debate found on A after the run"; return [pscustomobject]$result }
            $sessionId = $lst.Json.debates[0].session
        }

        $listA = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $aHome -CliArgs @("request", "list")
        $debateReqs = @($listA.Json.requests | Where-Object { $_.type -eq "debate" })
        if ($debateReqs.Count -ne 1) { $result.Reason = "expected exactly one debate request on A, found $($debateReqs.Count)"; return [pscustomobject]$result }

        Wait-Until -What "debate closed on A" -TimeoutSeconds 60 -Cond {
            $s = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $aHome -CliArgs @("debate", $sessionId)
            $s.Json -and $s.Json.debate -and @("closed", "broken") -contains $s.Json.debate.phase
        } | Out-Null
        $finalA = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $aHome -CliArgs @("debate", $sessionId)
        $phase = $finalA.Json.debate.phase
        $outcome = $finalA.Json.debate.outcome
        $result.Outcome = $outcome
        if ($phase -ne "closed") { $result.Reason = "debate ended phase $phase, not closed"; return [pscustomobject]$result }
        if (@("agreed", "escalated") -notcontains $outcome) { $result.Reason = "unexpected outcome $outcome"; return [pscustomobject]$result }
        if ($finalA.Json.debate.rounds.current -gt 2) { $result.Reason = "rounds.current $($finalA.Json.debate.rounds.current) exceeds 2"; return [pscustomobject]$result }

        Wait-Until -What "debate closed on B" -TimeoutSeconds 60 -Cond {
            $s = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $bHome -CliArgs @("debate", $sessionId)
            $s.Json -and $s.Json.debate -and @("closed", "broken") -contains $s.Json.debate.phase
        } | Out-Null

        $decisionId = $finalA.Json.debate.decision.id
        if (-not $decisionId) { $result.Reason = "no decision id on the closed debate"; return [pscustomobject]$result }
        $decisionJsonPath = Join-Path $RunDir "decision.json"
        $exportResp = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $aHome -CliArgs @("decision", $decisionId, "--out", $decisionJsonPath, "--force")
        if ($exportResp.ExitCode -ne 0 -or -not (Test-Path $decisionJsonPath)) { $result.Reason = "decision --json --out failed: $($exportResp.Stdout) $($exportResp.Stderr)"; return [pscustomobject]$result }

        $verifyPsi = New-Object System.Diagnostics.ProcessStartInfo
        $verifyPsi.FileName = $AgentnetExe
        $verifyPsi.Arguments = Format-ArgList @("decision", "verify", $decisionJsonPath, "--json")
        $verifyPsi.RedirectStandardOutput = $true; $verifyPsi.RedirectStandardError = $true; $verifyPsi.UseShellExecute = $false
        $verifyProc = [System.Diagnostics.Process]::Start($verifyPsi)
        $verifyOut = $verifyProc.StandardOutput.ReadToEnd()
        $verifyProc.WaitForExit(10000) | Out-Null
        if ($verifyProc.ExitCode -ne 0) { $result.Reason = "decision verify exited $($verifyProc.ExitCode) (want 0, two signatures): $verifyOut"; return [pscustomobject]$result }
        $verifyJson = $verifyOut | ConvertFrom-Json
        if (-not $verifyJson.complete) { $result.Reason = "decision verify: not signed by both sides: $verifyOut"; return [pscustomobject]$result }

        # Invoke-CliJson always appends --json, which --md refuses to combine
        # with (Docs/cli/decision.md: "--md and --json exclude each other"),
        # so this one call is made directly.
        $decisionMdPath = Join-Path $RunDir "decision.md"
        $mdPsi = New-Object System.Diagnostics.ProcessStartInfo
        $mdPsi.FileName = $AgentnetExe
        $mdPsi.Arguments = Format-ArgList @("decision", $decisionId, "--md", "--out", $decisionMdPath, "--force")
        $mdPsi.RedirectStandardOutput = $true; $mdPsi.RedirectStandardError = $true; $mdPsi.UseShellExecute = $false
        $mdPsi.Environment["DORYLINAE_HOME"] = $aHome
        $mdProc = [System.Diagnostics.Process]::Start($mdPsi)
        $mdOut = $mdProc.StandardOutput.ReadToEnd(); $mdErr = $mdProc.StandardError.ReadToEnd()
        $mdProc.WaitForExit(10000) | Out-Null
        if ($mdProc.ExitCode -ne 0 -or -not (Test-Path $decisionMdPath)) { $result.Reason = "decision --md --out failed ($($mdProc.ExitCode)): $mdOut $mdErr"; return [pscustomobject]$result }

        $decisionRaw = Get-Content -Raw -Path $decisionJsonPath | ConvertFrom-Json
        $humanDecisions = @($decisionRaw.decision.human_decisions)
        if ($humanDecisions.Count -lt 1) { $result.Reason = "expected the human constraint in human_decisions, found none"; return [pscustomobject]$result }

        if (-not $Python) { $result.Reason = "no python3 with sqlite3 module available to read audit_events"; return [pscustomobject]$result }
        $auditA = Get-AuditActions -Python $Python -DbPath (Join-Path $aHome "dorylinae.db")
        $auditB = Get-AuditActions -Python $Python -DbPath (Join-Path $bHome "dorylinae.db")
        $needed = @("request.submit", "request.in", "request.accept")
        $missing = $needed | Where-Object { ($auditA + $auditB) -notcontains $_ }
        if ($missing.Count -gt 0) { $result.Reason = "audit log missing: $($missing -join ', ') (A: $($auditA -join ','); B: $($auditB -join ','))"; return [pscustomobject]$result }

        foreach ($h in @(@{ Name = "A"; Home = $aHome }, @{ Name = "B"; Home = $bHome })) {
            $logVerify = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $h.Home -CliArgs @("log", "--verify")
            if ($logVerify.ExitCode -ne 0) { $result.Reason = "log --verify on $($h.Name) exited $($logVerify.ExitCode): $($logVerify.Stdout) $($logVerify.Stderr)"; return [pscustomobject]$result }
        }

        $result.Pass = $true
        $result.Reason = "ok"
        $result.DurationSeconds = [Math]::Round(((Get-Date) - $roundStart).TotalSeconds, 1)
        return [pscustomobject]$result
    } finally {
        Stop-PipedProc -Process $handles.daemonA
        Stop-PipedProc -Process $handles.daemonB
        try { if (-not $handles.relay.HasExited) { Stop-ProcessTree -Process $handles.relay } } catch {}
    }
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

if (-not $SkipBuild) {
    Write-Step "building agentnet, agentnetd, relay, and the stand-in"
    $env:PATH = "$env:USERPROFILE\tools\go\bin;$env:PATH"
    Push-Location $RepoRoot
    try {
        & go build -o bin/ ./cmd/... 2>&1 | Tee-Object -Variable buildOut | Out-Null
        if ($LASTEXITCODE -ne 0) { Write-Fail "go build failed:`n$buildOut"; exit 1 }
        & go build -o bin/standin.exe ./tests/harness/standin 2>&1 | Tee-Object -Variable standinOut | Out-Null
        if ($LASTEXITCODE -ne 0) { Write-Fail "go build (standin) failed:`n$standinOut"; exit 1 }
    } finally { Pop-Location }
}

$agentnetExe = Join-Path $RepoRoot "bin\agentnet.exe"
$standinExe = Join-Path $RepoRoot "bin\standin.exe"
if (-not (Test-Path $agentnetExe)) { Write-Fail "agentnet.exe not found at $agentnetExe"; exit 1 }
if (-not (Test-Path $standinExe)) { Write-Fail "standin.exe not found at $standinExe"; exit 1 }

$python = Get-Python3
if (-not $python) { Write-Host "[3.H] WARNING: no python3 with sqlite3 found; audit assertions will fail" -ForegroundColor Yellow }

$rootRun = Join-Path ([IO.Path]::GetTempPath()) "phase3-agents-$([guid]::NewGuid().ToString('N').Substring(0,8))"
New-Item -ItemType Directory -Force -Path $rootRun | Out-Null
Write-Step "run directory: $rootRun"

if ($Harness -eq "standin") {
    # 3.5/OD-P3-7: the forced escalation is deterministic only with the
    # stand-in, so the weekly CI job covers both an agreed debate and a
    # forced escalation here (real-agent rounds never force disagreement).
    $rounds = @(
        @{ Num = 1; Initiator = "standin"; Respondent = "standin"; Scenario = "agreed" },
        @{ Num = 2; Initiator = "standin"; Respondent = "standin"; Scenario = "escalated" }
    )
    if ($OnlyRound -ne 0) { $rounds = $rounds | Where-Object { $_.Num -eq $OnlyRound } }
} elseif ($InitiatorHarness -or $RespondentHarness) {
    if (-not ($InitiatorHarness -and $RespondentHarness)) { Write-Fail "-InitiatorHarness and -RespondentHarness must be given together"; exit 2 }
    $rounds = @(@{ Num = 1; Initiator = $InitiatorHarness; Respondent = $RespondentHarness; Scenario = "agreed" })
} else {
    $rounds = @(
        @{ Num = 1; Initiator = "claude"; Respondent = "agy"; Scenario = "agreed" },
        @{ Num = 2; Initiator = "agy"; Respondent = "claude"; Scenario = "agreed" }
    )
    if ($OnlyRound -ne 0) { $rounds = $rounds | Where-Object { $_.Num -eq $OnlyRound } }
}

$results = @()
$attemptLog = @()
foreach ($r in $rounds) {
    $port = $RelayPortBase + $r.Num
    $res = $null
    for ($attempt = 1; $attempt -le $MaxAttempts; $attempt++) {
        $runDir = Join-Path $rootRun "round$($r.Num)-attempt$attempt"
        New-Item -ItemType Directory -Force -Path $runDir | Out-Null
        $res = Invoke-Round -RoundNum $r.Num -InitiatorTool $r.Initiator -RespondentTool $r.Respondent -Scenario $r.Scenario `
            -AgentnetExe $agentnetExe -StandinExe $standinExe -RunDir $runDir -RelayPort $port -Python $python
        $attemptLog += [pscustomobject]@{ Round = $r.Num; Attempt = $attempt; Pass = $res.Pass; Reason = $res.Reason }
        if ($res.Pass) { Write-Ok "round $($r.Num) attempt $attempt ($($r.Initiator) -> $($r.Respondent), $($r.Scenario)): PASS (outcome=$($res.Outcome), $($res.DurationSeconds)s)"; break }
        else { Write-Fail "round $($r.Num) attempt $attempt ($($r.Initiator) -> $($r.Respondent), $($r.Scenario)): $($res.Reason)" }
    }
    $results += $res

    if ((Get-Date) - $scriptStart -gt [TimeSpan]::FromSeconds($TotalTimeoutSeconds)) {
        Write-Fail "total run time exceeded $TotalTimeoutSeconds s; stopping"
        break
    }
}

Write-Host ""
Write-Host "=== 3.H summary ===" -ForegroundColor Cyan
foreach ($res in $results) {
    $status = if ($res.Pass) { "PASS" } else { "FAIL" }
    Write-Host ("  round {0}: {1} -> {2} ({3}): {4} ({5}) outcome={6} turns={7}" -f `
        $res.Round, $res.Initiator, $res.Respondent, $res.Scenario, $status, $res.Reason, $res.Outcome, $res.Turns)
}
$elapsed = (Get-Date) - $scriptStart
Write-Host ("  elapsed: {0:N1}s (limit {1}s)" -f $elapsed.TotalSeconds, $TotalTimeoutSeconds)
Write-Host "  logs: $rootRun"

$allPass = ($results.Count -gt 0) -and (($results | Where-Object { -not $_.Pass }).Count -eq 0)
$withinTime = $elapsed.TotalSeconds -le $TotalTimeoutSeconds
if ($allPass -and $withinTime) { exit 0 } else { exit 1 }
