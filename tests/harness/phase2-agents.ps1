#Requires -Version 5.1
<#
.SYNOPSIS
  Ticket 2.H: drives two REAL headless coding-agent harnesses (Claude Code and
  agy) through a live AgentNet request -> accept -> grant -> fetch -> consult
  -> result -> accept-result round trip, using only the plain-English agent
  snippet (Docs/agents/snippet.md) as instructions. Assertions come from
  `agentnet ... --json` output and the daemons' audit logs, never from agent
  prose. `-Harness standin` runs the scripted Go stand-in
  (tests/harness/standin) instead of a real agent, for CI (OD-P2-12).

.DESCRIPTION
  Starts a loopback relay and two agentnetd daemons (A, B) with separate
  --home directories and DORYLINAE_APPROVAL=terminal / DORYLINAE_DEBUG=1, so
  this SCRIPT (never the agent under test) reads each daemon's approval codes
  from its stderr and answers them on its stdin (Docs/protocol/approval.md
  §Headless machines). It pairs A and B, creates a team, writes a small
  fixture directory and a consult context file, then for each of two rounds
  (harness roles swapped) launches A and B CONCURRENTLY (unlike ticket 1.H's
  sequential sender/recipient, this round needs A to grant only after B
  accepts, and B to fetch only after A grants):
    - A is told to ask B to review the fixture directory and give B read
      access to it, then wait for the result and accept it, and separately to
      consult B with one context file;
    - B is told to check its inbox, accept, read the granted files through
      AgentNet, return a result, and answer the consult.
  Prompts never name an agentnet subcommand: the agent must find it from the
  snippet and --help.

.PARAMETER RepoRoot
  Path to the repo checkout (worktree) this script builds and tests against.

.PARAMETER Branch
  Branch name named in the review request. A string in the request body only.

.PARAMETER SkipBuild
  Skip `go build`; use the binaries already in <RepoRoot>/bin.

.PARAMETER OnlyRound
  Run only round 1 or round 2 (1 or 2). Default: both.

.PARAMETER Harness
  `standin` (default; the scripted Go program, free, used by weekly CI) or
  `real` (Claude Code and agy, each once as A and once as B, per the ticket;
  needs logged-in CLIs and paid API access -- run by hand, never in CI).

.EXAMPLE
  pwsh tests/harness/phase2-agents.ps1                     # standin, CI-safe
  pwsh tests/harness/phase2-agents.ps1 -Harness real        # real agents
#>
[CmdletBinding()]
param(
    [string]$RepoRoot = "",
    [string]$Branch = "p2/example-branch",
    [switch]$SkipBuild,
    [ValidateSet(1, 2)]
    [int]$OnlyRound = 0,
    [int]$RelayPortBase = 18887,
    [int]$AgentTimeoutSeconds = 420,
    [int]$TotalTimeoutSeconds = 900,
    [ValidateSet("standin", "real")]
    [string]$Harness = "standin",
    [ValidateSet("claude", "agy", "codex")]
    [string]$SenderHarness,
    [ValidateSet("claude", "agy", "codex")]
    [string]$RecipientHarness,
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

function Write-Step($msg) { Write-Host "[2.H] $msg" -ForegroundColor Cyan }
function Write-Fail($msg) { Write-Host "[2.H] FAIL: $msg" -ForegroundColor Red }
function Write-Ok($msg) { Write-Host "[2.H] OK: $msg" -ForegroundColor Green }

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
# Process helpers
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
# Approval pump (2.2d headless machines): reads "AgentNet approval a-XXXXXX:
# <summary>. Code NNNNNN. ..." off a daemon's stderr and writes "<tag> <code>"
# to its stdin. Runs as a background job per daemon so the script does not
# block draining stderr while the agents run.
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
# Snippet
# ---------------------------------------------------------------------------

function Get-SnippetBody {
    $snippetFile = Join-Path $RepoRoot "Docs\agents\snippet.md"
    $raw = Get-Content -Raw -Path $snippetFile
    if ($raw -match '(?s)```markdown\r?\n(.*?)\r?\n```') { return $Matches[1] }
    throw "could not extract the fenced snippet block from $snippetFile"
}

# ---------------------------------------------------------------------------
# One round
# ---------------------------------------------------------------------------

function Invoke-Round {
    param(
        [int]$RoundNum,
        [string]$SenderTool,
        [string]$RecipientTool,
        [string]$AgentnetExe,
        [string]$StandinExe,
        [string]$RunDir,
        [int]$RelayPort,
        [string]$Python
    )

    $result = [ordered]@{ Round = $RoundNum; Sender = $SenderTool; Recipient = $RecipientTool; Pass = $false; Reason = "" }

    $relayHome = Join-Path $RunDir "relay"
    $aHome = Join-Path $RunDir "a-home"
    $bHome = Join-Path $RunDir "b-home"
    $aWork = Join-Path $RunDir "a-work"
    $bWork = Join-Path $RunDir "b-work"
    $fixtureDir = Join-Path $RunDir "fixture"
    $contextFile = Join-Path $RunDir "consult-context.md"
    foreach ($d in @($relayHome, $aHome, $bHome, $aWork, $bWork, $fixtureDir)) { New-Item -ItemType Directory -Force -Path $d | Out-Null }
    Set-Content -Path (Join-Path $fixtureDir "NOTES.txt") -Value "Fixture file for the 2.H headless harness round trip." -Encoding utf8
    Set-Content -Path $contextFile -Value "Context for the 2.H consult: this repo's fixture directory holds one small text file." -Encoding utf8

    $relayExe = Join-Path $RepoRoot "bin\relay.exe"
    $daemonExe = Join-Path $RepoRoot "bin\agentnetd.exe"

    $handles = @{}
    $pumps = @()
    try {
        Write-Step "round $RoundNum ($SenderTool -> $RecipientTool): starting relay on 127.0.0.1:$RelayPort"
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

        Write-Step "creating team t2h on A and inviting B"
        $team = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $aHome -CliArgs @("team", "create", "t2h")
        if (-not $team.Json -or -not $team.Json.ok) { throw "team create failed: $($team.Stdout)" }
        $invite = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $aHome -CliArgs @("team", "invite", "t2h")
        $join = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $bHome -CliArgs @("team", "join", $invite.Json.code)
        if (-not $join.Json -or -not $join.Json.ok) { throw "team join failed: $($join.Stdout)" }
        Wait-Until -What "roster to reach 2 members on A" -TimeoutSeconds 20 -Cond {
            $show = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $aHome -CliArgs @("team", "show", "t2h")
            $show.Json -and $show.Json.team -and @($show.Json.team.members).Count -ge 2
        } | Out-Null
        Write-Ok "team t2h has both members"

        $idemReview = "t2h-r$RoundNum-review-$([guid]::NewGuid().ToString('N').Substring(0,10))"
        $idemConsult = "t2h-r$RoundNum-consult-$([guid]::NewGuid().ToString('N').Substring(0,10))"

        if ($SenderTool -eq "standin") {
            Write-Step "invoking stand-in A and B concurrently"
            $aArgs = @("-agentnet", $AgentnetExe, "-home", $aHome, "-role", "a", "-peer", "agent-b",
                "-fixture", $fixtureDir, "-context", $contextFile, "-branch", $Branch,
                "-review-key", $idemReview, "-consult-key", $idemConsult, "-timeout", "$AgentTimeoutSeconds")
            $bArgs = @("-agentnet", $AgentnetExe, "-home", $bHome, "-role", "b", "-timeout", "$AgentTimeoutSeconds")
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
            $aSnippetFile = if ($SenderTool -eq "claude") { "CLAUDE.md" } else { "AGENTS.md" }
            $bSnippetFile = if ($RecipientTool -eq "claude") { "CLAUDE.md" } else { "AGENTS.md" }
            Set-Content -Path (Join-Path $aWork $aSnippetFile) -Value $snippet -Encoding utf8
            Set-Content -Path (Join-Path $bWork $bSnippetFile) -Value $snippet -Encoding utf8

            $senderPrompt = @"
You are working with a teammate whose AgentNet peer name is agent-b, on the shared team t2h.
Ask agent-b's agent, over AgentNet, to review the directory $fixtureDir (branch $Branch) and
give agent-b read access to that directory (fs.read) on the same work session. Use exactly
this idempotency key for the review request so a retry never sends it twice: $idemReview
Wait for agent-b's result (poll every few seconds, up to several minutes). If the result is held
back for your release, release it (that asks a human, who is answered for you) and then accept the
result. Separately, consult agent-b with one context file ($contextFile), asking "Is the
fixture file readable and non-empty?"; use exactly this idempotency key: $idemConsult
Wait for the consult's answer the same way and accept it too. Then stop.
To wait, run Start-Sleep -Seconds 5 as its own command and then check again; never chain commands.
Run agentnet --help directly as your first command; do not check whether it exists first
(for example with Get-Command, where, which or Test-Path), and do not chain it with any
other command.
"@
            $recipientPrompt = @"
Check your AgentNet inbox for anything waiting for you. You should find two items: one asking
for a review with a grant of read access to a directory, and one consult question.
For the review: accept it, wait until you have read access, list and read the granted file(s)
with agentnet fetch, then submit a result with status pass, a one-line summary and a short
note describing what you read.
For the consult: answer the question with a short result (no need to accept it separately;
answering it accepts it).
Then stop.
To wait, run Start-Sleep -Seconds 5 as its own command and then check again; never chain commands.
Run agentnet --help directly as your first command; do not check whether it exists first
(for example with Get-Command, where, which or Test-Path), and do not chain it with any
other command.
"@
            $binDir = Join-Path $RepoRoot "bin"
            Write-Step "invoking A ($SenderTool) and B ($RecipientTool) concurrently"
            $aHandle = Start-Agent -Tool $SenderTool -Prompt $senderPrompt -WorkDir $aWork -BinDir $binDir -HomeDir $aHome -TimeoutSeconds $AgentTimeoutSeconds -LogPrefix (Join-Path $RunDir "sender-$SenderTool")
            $bHandle = Start-Agent -Tool $RecipientTool -Prompt $recipientPrompt -WorkDir $bWork -BinDir $binDir -HomeDir $bHome -TimeoutSeconds $AgentTimeoutSeconds -LogPrefix (Join-Path $RunDir "recipient-$RecipientTool")
            $aRun = Wait-Agent $aHandle; $bRun = Wait-Agent $bHandle
            if (-not $aRun -or -not $aRun.Ran) { $result.Reason = "sender ($SenderTool) could not run: $($aRun.Reason)"; return [pscustomobject]$result }
            if (-not $bRun -or -not $bRun.Ran) { $result.Reason = "recipient ($RecipientTool) could not run: $($bRun.Reason)"; return [pscustomobject]$result }
        }

        # --- assertions, independent of agent/stand-in prose ------------------
        Write-Step "asserting outcome from --json and audit logs"
        Wait-Until -What "exactly one review request on A" -TimeoutSeconds 30 -Cond {
            $lst = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $aHome -CliArgs @("request", "list")
            $lst.Json -and @($lst.Json.requests | Where-Object { $_.type -eq "review" }).Count -eq 1
        } | Out-Null
        Wait-Until -What "exactly one question request on A" -TimeoutSeconds 30 -Cond {
            $lst = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $aHome -CliArgs @("request", "list")
            $lst.Json -and @($lst.Json.requests | Where-Object { $_.type -eq "question" }).Count -eq 1
        } | Out-Null

        $sessionsA = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $aHome -CliArgs @("sessions", "--role", "requester")
        Wait-Until -What "both sessions closed with outcome accepted" -TimeoutSeconds 60 -Cond {
            $s = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $aHome -CliArgs @("sessions", "--role", "requester")
            $s.Json -and @($s.Json.sessions | Where-Object { $_.state -eq "closed" -and $_.outcome -eq "accepted" }).Count -ge 2
        } | Out-Null
        $sessionsA = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $aHome -CliArgs @("sessions", "--role", "requester")
        $closedAccepted = @($sessionsA.Json.sessions | Where-Object { $_.state -eq "closed" -and $_.outcome -eq "accepted" })
        if ($closedAccepted.Count -lt 2) { $result.Reason = "expected 2 sessions closed/accepted on A, found $($closedAccepted.Count)"; return [pscustomobject]$result }

        $grants = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $aHome -CliArgs @("grants", "--issued")
        $reviewGrants = @($grants.Json.grants)
        if ($reviewGrants.Count -lt 1) { $result.Reason = "expected at least 1 issued grant on A, found $($reviewGrants.Count)"; return [pscustomobject]$result }
        Wait-Until -What "the grant to be revoked after session close" -TimeoutSeconds 30 -Cond {
            $g = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $aHome -CliArgs @("grants", "--issued")
            @($g.Json.grants | Where-Object { $_.state -eq "revoked" }).Count -ge 1
        } | Out-Null

        if (-not $Python) { $result.Reason = "no python3 with sqlite3 module available to read audit_events"; return [pscustomobject]$result }
        $auditA = Get-AuditActions -Python $Python -DbPath (Join-Path $aHome "dorylinae.db")
        $auditB = Get-AuditActions -Python $Python -DbPath (Join-Path $bHome "dorylinae.db")
        $needed = @("request.submit", "request.in", "request.accept", "ws.close", "grant.create", "grant.fetch")
        $missing = $needed | Where-Object { ($auditA + $auditB) -notcontains $_ }
        if ($missing.Count -gt 0) { $result.Reason = "audit log missing: $($missing -join ', ') (A: $($auditA -join ','); B: $($auditB -join ','))"; return [pscustomobject]$result }

        $result.Pass = $true
        $result.Reason = "ok"
        return [pscustomobject]$result
    } finally {
                Stop-PipedProc -Process $handles.daemonA
        Stop-PipedProc -Process $handles.daemonB
        try { if (-not $handles.relay.HasExited) { Stop-ProcessTree -Process $handles.relay } } catch {}
    }
}

function Start-Agent {
    <# Runs one headless real-agent turn (claude or agy). See
       tests/harness/phase1-agents.ps1 for the tool-specific flag notes this
       reuses unchanged. #>
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
    <# Waits for a Start-Agent handle. Ran=$false means the harness itself could
       not be exercised (auth/usage-limit, timeout, non-zero exit) as opposed to
       an assertion failure after a real run. #>
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
if ($Harness -eq "standin" -and -not (Test-Path $standinExe)) { Write-Fail "standin.exe not found at $standinExe"; exit 1 }

$python = Get-Python3
if (-not $python) { Write-Host "[2.H] WARNING: no python3 with sqlite3 found; audit assertions will fail" -ForegroundColor Yellow }

$rootRun = Join-Path ([IO.Path]::GetTempPath()) "phase2-agents-$([guid]::NewGuid().ToString('N').Substring(0,8))"
New-Item -ItemType Directory -Force -Path $rootRun | Out-Null
Write-Step "run directory: $rootRun"

if ($Harness -eq "standin") {
    $rounds = @(@{ Num = 1; Sender = "standin"; Recipient = "standin" })
    if ($OnlyRound -ne 0) { $rounds = $rounds | Where-Object { $_.Num -eq $OnlyRound } }
} elseif ($SenderHarness -or $RecipientHarness) {
    if (-not ($SenderHarness -and $RecipientHarness)) { Write-Fail "-SenderHarness and -RecipientHarness must be given together"; exit 2 }
    $rounds = @(@{ Num = 1; Sender = $SenderHarness; Recipient = $RecipientHarness })
} else {
    $rounds = @(
        @{ Num = 1; Sender = "claude"; Recipient = "agy" },
        @{ Num = 2; Sender = "agy"; Recipient = "claude" }
    )
    if ($OnlyRound -ne 0) { $rounds = $rounds | Where-Object { $_.Num -eq $OnlyRound } }
}

$results = @()
$attemptLog = @()
foreach ($r in $rounds) {
    $port = $RelayPortBase + $r.Num
    $passCount = 0
    $res = $null
    for ($attempt = 1; $attempt -le $MaxAttempts; $attempt++) {
        $runDir = Join-Path $rootRun "round$($r.Num)-attempt$attempt"
        New-Item -ItemType Directory -Force -Path $runDir | Out-Null
        $res = Invoke-Round -RoundNum $r.Num -SenderTool $r.Sender -RecipientTool $r.Recipient `
            -AgentnetExe $agentnetExe -StandinExe $standinExe -RunDir $runDir -RelayPort $port -Python $python
        $attemptLog += [pscustomobject]@{ Round = $r.Num; Attempt = $attempt; Pass = $res.Pass; Reason = $res.Reason }
        if ($res.Pass) { $passCount++; Write-Ok "round $($r.Num) attempt $attempt ($($r.Sender) -> $($r.Recipient)): PASS"; break }
        else { Write-Fail "round $($r.Num) attempt $attempt ($($r.Sender) -> $($r.Recipient)): $($res.Reason)" }
    }
    $results += $res

    if ((Get-Date) - $scriptStart -gt [TimeSpan]::FromSeconds($TotalTimeoutSeconds)) {
        Write-Fail "total run time exceeded $TotalTimeoutSeconds s; stopping"
        break
    }
}

Write-Host ""
Write-Host "=== 2.H summary ===" -ForegroundColor Cyan
foreach ($res in $results) {
    $status = if ($res.Pass) { "PASS" } else { "FAIL" }
    $roundAttempts = @($attemptLog | Where-Object { $_.Round -eq $res.Round })
    $roundPasses = @($roundAttempts | Where-Object { $_.Pass }).Count
    Write-Host ("  round {0}: {1} -> {2}: {3} ({4}) [{5}/{6} attempts passed]" -f `
        $res.Round, $res.Sender, $res.Recipient, $status, $res.Reason, $roundPasses, $roundAttempts.Count)
}
$elapsed = (Get-Date) - $scriptStart
Write-Host ("  elapsed: {0:N1}s (limit {1}s)" -f $elapsed.TotalSeconds, $TotalTimeoutSeconds)
Write-Host "  logs: $rootRun"

$allPass = ($results.Count -gt 0) -and (($results | Where-Object { -not $_.Pass }).Count -eq 0)
$withinTime = $elapsed.TotalSeconds -le $TotalTimeoutSeconds
if ($allPass -and $withinTime) { exit 0 } else { exit 1 }
