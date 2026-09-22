#Requires -Version 5.1
<#
.SYNOPSIS
  Ticket 1.H: drives two REAL headless coding-agent harnesses (Claude Code and
  Codex CLI) through a live AgentNet request -> inbox -> accept -> complete
  round trip, using only the plain-English agent snippet (Docs/agents/snippet.md)
  as instructions. Assertions come from `agentnet ... --json` output and the
  daemons' audit logs, never from agent prose.

.DESCRIPTION
  Starts a loopback relay and two agentnetd daemons (A, B) with separate
  --home directories, pairs them, creates a team, then for each of two rounds
  (harness roles swapped) launches a real `claude -p` / `codex exec` process
  in a scratch working directory that holds only the snippet file, tells the
  sender agent in plain words to ask the other agent for a review with a
  stated --idempotency-key, and tells the recipient agent to check its inbox,
  accept it, do nothing else, then complete it with a note and a D14 result.

.PARAMETER RepoRoot
  Path to the repo checkout (worktree) this script builds and tests against.
  Defaults to two directories above this script (tests/harness/../..).

.PARAMETER Branch
  Branch name the sender asks for a review of. Purely a string in the request
  body; nothing here checks it out.

.PARAMETER SkipBuild
  Skip `go build`; use the binaries already in <RepoRoot>/bin.

.PARAMETER OnlyRound
  Run only round 1 or round 2 (1 or 2). Default: both.

.EXAMPLE
  pwsh tests/harness/phase1-agents.ps1
#>
[CmdletBinding()]
param(
    [string]$RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path,
    [string]$Branch = "p1/t1-H",
    [switch]$SkipBuild,
    [ValidateSet(1, 2)]
    [int]$OnlyRound = 0,
    [int]$RelayPortBase = 18787,
    [int]$AgentTimeoutSeconds = 180,
    [int]$TotalTimeoutSeconds = 600
)

$ErrorActionPreference = "Stop"
$scriptStart = Get-Date

function Write-Step($msg) { Write-Host "[1.H] $msg" -ForegroundColor Cyan }
function Write-Fail($msg) { Write-Host "[1.H] FAIL: $msg" -ForegroundColor Red }
function Write-Ok($msg) { Write-Host "[1.H] OK: $msg" -ForegroundColor Green }

function Stop-ProcessTree {
    # Windows PowerShell 5.1 runs on .NET Framework, where Process.Kill() has
    # no (bool entireProcessTree) overload -- that was added in .NET 5+/Core.
    # Calling Kill($true) there throws a MethodException that a bare
    # `catch {}` swallows silently, so the process is never actually killed.
    # taskkill /T /F is the reliable way to end a process tree here.
    param([System.Diagnostics.Process]$Process)
    if ($null -eq $Process) { return }
    try {
        if (-not $Process.HasExited) {
            & taskkill.exe /PID $Process.Id /T /F 2>&1 | Out-Null
        }
    } catch {}
}

function Format-ArgList {
    # Windows PowerShell 5.1 targets .NET Framework, whose ProcessStartInfo has
    # no ArgumentList property (that is a .NET Core addition) -- build a single
    # quoted Arguments string instead.
    param([string[]]$ArgList)
    $parts = foreach ($a in $ArgList) {
        if ($a -match '[\s"]') { '"' + ($a -replace '"', '\"') + '"' } else { $a }
    }
    return ($parts -join ' ')
}

# ---------------------------------------------------------------------------
# Small process helpers
# ---------------------------------------------------------------------------

function Start-BackgroundProc {
    param([string]$FilePath, [string[]]$ArgList, [hashtable]$Env, [string]$WorkDir, [string]$StdoutPath, [string]$StderrPath)
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $FilePath
    $psi.Arguments = Format-ArgList $ArgList
    $psi.WorkingDirectory = $WorkDir
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    $psi.UseShellExecute = $false
    foreach ($k in $Env.Keys) { $psi.Environment[$k] = $Env[$k] }
    $p = New-Object System.Diagnostics.Process
    $p.StartInfo = $psi
    $null = $p.Start()
    # Drain async so the child never blocks on a full pipe.
    $outSb = New-Object System.Text.StringBuilder
    $errSb = New-Object System.Text.StringBuilder
    Register-ObjectEvent -InputObject $p -EventName OutputDataReceived -Action {
        if ($EventArgs.Data) { $Event.MessageData.AppendLine($EventArgs.Data) | Out-Null }
    } -MessageData $outSb | Out-Null
    Register-ObjectEvent -InputObject $p -EventName ErrorDataReceived -Action {
        if ($EventArgs.Data) { $Event.MessageData.AppendLine($EventArgs.Data) | Out-Null }
    } -MessageData $errSb | Out-Null
    $p.BeginOutputReadLine()
    $p.BeginErrorReadLine()
    return [pscustomobject]@{ Process = $p; Out = $outSb; Err = $errSb; StdoutPath = $StdoutPath; StderrPath = $StderrPath }
}

function Stop-BackgroundProc {
    param($Handle)
    if ($null -eq $Handle) { return }
    try {
        if (-not $Handle.Process.HasExited) {
            $Handle.Process.CloseMainWindow() | Out-Null
            Start-Sleep -Milliseconds 300
            if (-not $Handle.Process.HasExited) { Stop-ProcessTree -Process $Handle.Process }
        }
    } catch {}
    if ($Handle.StdoutPath) { Set-Content -Path $Handle.StdoutPath -Value $Handle.Out.ToString() -Encoding utf8 }
    if ($Handle.StderrPath) { Set-Content -Path $Handle.StderrPath -Value $Handle.Err.ToString() -Encoding utf8 }
}

function Invoke-CliJson {
    <# Runs `agentnet.exe <args...> --json` with DORYLINAE_HOME set, returns parsed JSON. #>
    param([string]$AgentnetExe, [string]$HomeDir, [string[]]$CliArgs, [int]$TimeoutMs = 5000)
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
    <# Reads audit_events.action from a dorylinae.db via python3's stdlib sqlite3
       (no sqlite3 CLI or CGo driver is available in this environment; see README). #>
    param([string]$Python, [string]$DbPath)
    if (-not (Test-Path $DbPath)) { return @() }
    $code = "import sqlite3,sys; con=sqlite3.connect(sys.argv[1]); print(chr(10).join(r[0] for r in con.execute('select action from audit_events order by id')))"
    $out = & $Python -c $code $DbPath 2>$null
    if ($LASTEXITCODE -ne 0) { return @() }
    return @($out -split "`n" | Where-Object { $_.Trim().Length -gt 0 })
}

# ---------------------------------------------------------------------------
# Snippet
# ---------------------------------------------------------------------------

function Get-SnippetBody {
    $snippetFile = Join-Path $RepoRoot "Docs\agents\snippet.md"
    $raw = Get-Content -Raw -Path $snippetFile
    # The doc wraps the pasted block in a ```markdown fence; extract just that block.
    if ($raw -match '(?s)```markdown\r?\n(.*?)\r?\n```') { return $Matches[1] }
    throw "could not extract the fenced snippet block from $snippetFile"
}

# ---------------------------------------------------------------------------
# One round: $SenderTool asks $RecipientTool for a review.
# ---------------------------------------------------------------------------

function Invoke-Round {
    param(
        [int]$RoundNum,
        [string]$SenderTool,      # "claude" or "codex"
        [string]$RecipientTool,
        [string]$AgentnetExe,
        [string]$RunDir,
        [int]$RelayPort,
        [string]$Python
    )

    $result = [ordered]@{
        Round = $RoundNum; Sender = $SenderTool; Recipient = $RecipientTool
        Pass = $false; Reason = ""; RequestId = $null
    }

    $relayHome = Join-Path $RunDir "relay"
    $aHome = Join-Path $RunDir "a-home"
    $bHome = Join-Path $RunDir "b-home"
    $aWork = Join-Path $RunDir "a-work"
    $bWork = Join-Path $RunDir "b-work"
    foreach ($d in @($relayHome, $aHome, $bHome, $aWork, $bWork)) { New-Item -ItemType Directory -Force -Path $d | Out-Null }

    $relayExe = Join-Path $RepoRoot "bin\relay.exe"
    $daemonExe = Join-Path $RepoRoot "bin\agentnetd.exe"

    $handles = @{}
    try {
        Write-Step "round $RoundNum ($SenderTool -> $RecipientTool): starting relay on 127.0.0.1:$RelayPort"
        $handles.relay = Start-BackgroundProc -FilePath $relayExe `
            -ArgList @("--listen", "127.0.0.1:$RelayPort", "--queue-db", (Join-Path $relayHome "relay-queue.db")) `
            -Env @{} -WorkDir $relayHome `
            -StdoutPath (Join-Path $RunDir "relay.out.log") -StderrPath (Join-Path $RunDir "relay.err.log")
        Start-Sleep -Milliseconds 500
        if ($handles.relay.Process.HasExited) { throw "relay exited immediately (see relay.err.log)" }

        $relayUrl = "ws://127.0.0.1:$RelayPort"
        Write-Step "starting daemon A (agent-a) and daemon B (agent-b)"
        $handles.daemonA = Start-BackgroundProc -FilePath $daemonExe -ArgList @("--relay", $relayUrl) `
            -Env @{ DORYLINAE_HOME = $aHome; DORYLINAE_AGENT_NAME = "agent-a" } -WorkDir $aHome `
            -StdoutPath (Join-Path $RunDir "daemonA.out.log") -StderrPath (Join-Path $RunDir "daemonA.err.log")
        $handles.daemonB = Start-BackgroundProc -FilePath $daemonExe -ArgList @("--relay", $relayUrl) `
            -Env @{ DORYLINAE_HOME = $bHome; DORYLINAE_AGENT_NAME = "agent-b" } -WorkDir $bHome `
            -StdoutPath (Join-Path $RunDir "daemonB.out.log") -StderrPath (Join-Path $RunDir "daemonB.err.log")

        Wait-Until -What "daemon A ready" -TimeoutSeconds 15 -Cond {
            $r = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $aHome -CliArgs @("status")
            $r.ExitCode -eq 0
        } | Out-Null
        Wait-Until -What "daemon B ready" -TimeoutSeconds 15 -Cond {
            $r = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $bHome -CliArgs @("status")
            $r.ExitCode -eq 0
        } | Out-Null
        Write-Ok "both daemons answer status"

        # --- pair A <-> B --------------------------------------------------
        Write-Step "pairing A and B"
        $newCode = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $aHome -CliArgs @("pair", "--new")
        if (-not $newCode.Json -or -not $newCode.Json.ok) { throw "pair --new failed: $($newCode.Stdout) $($newCode.Stderr)" }
        $code = $newCode.Json.code
        if (-not $code) { throw "pair --new returned no code: $($newCode.Stdout)" }
        $redeem = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $bHome -CliArgs @("pair", $code)
        if (-not $redeem.Json -or $redeem.Json.state -ne "complete") {
            throw "pair redeem did not complete: $($redeem.Stdout) $($redeem.Stderr)"
        }
        Write-Ok "paired (fingerprint check skipped: same-host automated run)"

        # --- team ------------------------------------------------------------
        Write-Step "creating team t1h on A and inviting B"
        $team = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $aHome -CliArgs @("team", "create", "t1h")
        if (-not $team.Json -or -not $team.Json.ok) { throw "team create failed: $($team.Stdout) $($team.Stderr)" }
        $invite = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $aHome -CliArgs @("team", "invite", "t1h")
        if (-not $invite.Json -or -not $invite.Json.code) { throw "team invite returned no code: $($invite.Stdout)" }
        $join = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $bHome -CliArgs @("team", "join", $invite.Json.code)
        if (-not $join.Json -or -not $join.Json.ok) { throw "team join failed: $($join.Stdout) $($join.Stderr)" }

        Wait-Until -What "roster to reach 2 members on A" -TimeoutSeconds 20 -Cond {
            $show = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $aHome -CliArgs @("team", "show", "t1h")
            $show.Json -and $show.Json.team -and @($show.Json.team.members).Count -ge 2
        } | Out-Null
        Wait-Until -What "roster visible on B" -TimeoutSeconds 20 -Cond {
            $lst = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $bHome -CliArgs @("team", "list")
            $lst.Json -and @($lst.Json.teams | Where-Object { $_.name -eq "t1h" }).Count -ge 1
        } | Out-Null
        Write-Ok "team t1h has both members"

        # --- agent working directories -------------------------------------
        $snippet = Get-SnippetBody
        $aSnippetFile = if ($SenderTool -eq "claude") { "CLAUDE.md" } else { "AGENTS.md" }
        $bSnippetFile = if ($RecipientTool -eq "claude") { "CLAUDE.md" } else { "AGENTS.md" }
        Set-Content -Path (Join-Path $aWork $aSnippetFile) -Value $snippet -Encoding utf8
        Set-Content -Path (Join-Path $bWork $bSnippetFile) -Value $snippet -Encoding utf8

        $idemKey = "t1h-r$RoundNum-$([guid]::NewGuid().ToString('N').Substring(0,12))"
        $senderPrompt = @"
You are working with a teammate whose AgentNet peer name is agent-b, on the shared team t1h.
Ask agent-b's agent, over AgentNet, for a code review of the branch $Branch.
Use exactly this idempotency key so a retry never sends the request twice: $idemKey
Send the request and then stop. Do not do anything else, and do not wait for the answer.
"@
        $recipientPrompt = @"
Check your AgentNet inbox for anything waiting for you.
Accept whatever is there. Do not do the review, and do not do anything else with it.
Then mark it complete with the short note "Acknowledged, no work performed" and a result
with status n/a and the one-line summary "Acknowledged, no work performed."
Then stop.
"@

        $binDir = Join-Path $RepoRoot "bin"

        # --- sender agent ----------------------------------------------------
        Write-Step "invoking sender agent ($SenderTool) in $aWork"
        $senderRun = Invoke-Agent -Tool $SenderTool -Prompt $senderPrompt -WorkDir $aWork -BinDir $binDir `
            -HomeDir $aHome -TimeoutSeconds $AgentTimeoutSeconds -LogPrefix (Join-Path $RunDir "sender-$SenderTool")
        if (-not $senderRun.Ran) {
            $result.Reason = "sender ($SenderTool) could not run: $($senderRun.Reason)"
            return [pscustomobject]$result
        }

        Wait-Until -What "a request to appear in A's outbox" -TimeoutSeconds 30 -Cond {
            $lst = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $aHome -CliArgs @("request", "list")
            $lst.Json -and @($lst.Json.requests).Count -ge 1
        } | Out-Null

        $listA = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $aHome -CliArgs @("request", "list")
        $reqs = @($listA.Json.requests)
        if ($reqs.Count -ne 1) {
            $result.Reason = "expected exactly 1 request on A after the sender agent ran, found $($reqs.Count)"
            return [pscustomobject]$result
        }
        $reqId = $reqs[0].id
        $result.RequestId = $reqId
        Write-Ok "sender agent queued exactly one request: $reqId"

        Wait-Until -What "request delivered to B's inbox" -TimeoutSeconds 30 -Cond {
            $inb = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $bHome -CliArgs @("inbox")
            $inb.Json -and @($inb.Json.requests | Where-Object { $_.id -eq $reqId }).Count -ge 1
        } | Out-Null

        # --- recipient agent ---------------------------------------------------
        Write-Step "invoking recipient agent ($RecipientTool) in $bWork"
        $recipRun = Invoke-Agent -Tool $RecipientTool -Prompt $recipientPrompt -WorkDir $bWork -BinDir $binDir `
            -HomeDir $bHome -TimeoutSeconds $AgentTimeoutSeconds -LogPrefix (Join-Path $RunDir "recipient-$RecipientTool")
        if (-not $recipRun.Ran) {
            $result.Reason = "recipient ($RecipientTool) could not run: $($recipRun.Reason)"
            return [pscustomobject]$result
        }

        Wait-Until -What "B's inbox to show it completed" -TimeoutSeconds 30 -Cond {
            $inbAll = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $bHome -CliArgs @("inbox", "--all")
            $inbAll.Json -and @($inbAll.Json.requests | Where-Object { $_.id -eq $reqId -and $_.state -eq "completed" }).Count -ge 1
        } | Out-Null
        Wait-Until -What "A's mirror to show it completed" -TimeoutSeconds 30 -Cond {
            $show = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $aHome -CliArgs @("request", "show", $reqId)
            $show.Json -and $show.Json.request -and $show.Json.request.state -eq "completed"
        } | Out-Null

        $showA = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $aHome -CliArgs @("request", "show", $reqId)
        $req = $showA.Json.request
        if ($req.state -ne "completed") { $result.Reason = "A's mirror state = $($req.state), want completed"; return [pscustomobject]$result }
        if (-not $req.note -or $req.note.Trim().Length -eq 0) { $result.Reason = "A's mirror has no completion note"; return [pscustomobject]$result }
        if (-not $req.result -or -not $req.result.status) { $result.Reason = "A's mirror has no D14 result"; return [pscustomobject]$result }
        if ($req.result.status -notin @("n/a", "pass")) { $result.Reason = "result.status = $($req.result.status), want n/a or pass"; return [pscustomobject]$result }
        if (-not $req.result.summary -or $req.result.summary.Trim().Length -eq 0) { $result.Reason = "result has no summary"; return [pscustomobject]$result }

        $listA2 = Invoke-CliJson -AgentnetExe $AgentnetExe -HomeDir $aHome -CliArgs @("request", "list")
        if (@($listA2.Json.requests).Count -ne 1) { $result.Reason = "duplicate request detected: $(@($listA2.Json.requests).Count) requests on A"; return [pscustomobject]$result }

        # --- audit ---------------------------------------------------------
        if (-not $Python) {
            $result.Reason = "no python3 with sqlite3 module available to read audit_events (see README prerequisites)"
            return [pscustomobject]$result
        }
        $auditA = Get-AuditActions -Python $Python -DbPath (Join-Path $aHome "dorylinae.db")
        $auditB = Get-AuditActions -Python $Python -DbPath (Join-Path $bHome "dorylinae.db")
        $allActions = @($auditA) + @($auditB)
        $needed = @("request.submit", "request.in", "request.accept", "request.complete", "request.state")
        $missing = $needed | Where-Object { $allActions -notcontains $_ }
        if ($missing.Count -gt 0) {
            $result.Reason = "audit log missing: $($missing -join ', ') (A has: $($auditA -join ','); B has: $($auditB -join ','))"
            return [pscustomobject]$result
        }

        $result.Pass = $true
        $result.Reason = "ok"
        return [pscustomobject]$result
    } finally {
        Stop-BackgroundProc $handles.daemonA
        Stop-BackgroundProc $handles.daemonB
        Stop-BackgroundProc $handles.relay
    }
}

function Invoke-Agent {
    <# Runs one headless agent turn. Returns { Ran: bool; Reason: string }.
       Ran=$false means the harness itself could not be exercised (missing
       binary, auth/usage-limit failure, non-zero exit with no request
       created) -- distinct from an assertion failure after a real run. #>
    param([string]$Tool, [string]$Prompt, [string]$WorkDir, [string]$BinDir, [string]$HomeDir, [int]$TimeoutSeconds, [string]$LogPrefix)

    $env = @{ DORYLINAE_HOME = $HomeDir; PATH = "$BinDir;$env:PATH" }
    switch ($Tool) {
        "claude" {
            $exe = Join-Path $env:USERPROFILE ".local\bin\claude.exe"
            if (-not (Test-Path $exe)) { $cmd = Get-Command claude -ErrorAction SilentlyContinue; if ($cmd) { $exe = $cmd.Source } }
            if (-not (Test-Path $exe)) { return @{ Ran = $false; Reason = "claude executable not found" } }
            $argList = @($Prompt, "-p", "--restricted", "--tools", "Bash", "--allowedTools", "Bash(agentnet *)",
                "--permission-prompts", "none", "--output-format", "json")
        }
        "codex" {
            $cmd = Get-Command codex -ErrorAction SilentlyContinue
            if (-not $cmd) { return @{ Ran = $false; Reason = "codex executable not found" } }
            $exe = $cmd.Source
            $argList = @("exec", "--skip-git-repo-check", "--sandbox", "workspace-write", "-C", $WorkDir, "--json", $Prompt)
        }
        default { return @{ Ran = $false; Reason = "unknown harness $Tool" } }
    }

    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $exe
    $psi.Arguments = Format-ArgList $argList
    $psi.WorkingDirectory = $WorkDir
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    $psi.UseShellExecute = $false
    foreach ($k in $env.Keys) { $psi.Environment[$k] = $env[$k] }
    $p = [System.Diagnostics.Process]::Start($psi)
    $stdout = $p.StandardOutput.ReadToEndAsync()
    $stderr = $p.StandardError.ReadToEndAsync()
    $finished = $p.WaitForExit($TimeoutSeconds * 1000)
    if (-not $finished) {
        Stop-ProcessTree -Process $p
        Set-Content -Path "$LogPrefix.timeout.log" -Value "timed out after $TimeoutSeconds s"
        return @{ Ran = $false; Reason = "$Tool timed out after $TimeoutSeconds s" }
    }
    $out = $stdout.Result
    $err = $stderr.Result
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
    Write-Step "building agentnet, agentnetd, relay"
    $env:PATH = "$env:USERPROFILE\tools\go\bin;$env:PATH"
    Push-Location $RepoRoot
    try {
        & go build -o bin/ ./cmd/... 2>&1 | Tee-Object -Variable buildOut | Out-Null
        if ($LASTEXITCODE -ne 0) { Write-Fail "go build failed:`n$buildOut"; exit 1 }
    } finally { Pop-Location }
}

$agentnetExe = Join-Path $RepoRoot "bin\agentnet.exe"
if (-not (Test-Path $agentnetExe)) { Write-Fail "agentnet.exe not found at $agentnetExe"; exit 1 }

$python = Get-Python3
if (-not $python) { Write-Host "[1.H] WARNING: no python3 with sqlite3 found; audit assertions will fail (see README)" -ForegroundColor Yellow }

$rootRun = Join-Path ([IO.Path]::GetTempPath()) "phase1-agents-$([guid]::NewGuid().ToString('N').Substring(0,8))"
New-Item -ItemType Directory -Force -Path $rootRun | Out-Null
Write-Step "run directory: $rootRun"

$rounds = @(
    @{ Num = 1; Sender = "claude"; Recipient = "codex" },
    @{ Num = 2; Sender = "codex"; Recipient = "claude" }
)
if ($OnlyRound -ne 0) { $rounds = $rounds | Where-Object { $_.Num -eq $OnlyRound } }

$results = @()
foreach ($r in $rounds) {
    $port = $RelayPortBase + $r.Num
    $runDir = Join-Path $rootRun "round$($r.Num)"
    New-Item -ItemType Directory -Force -Path $runDir | Out-Null
    $res = Invoke-Round -RoundNum $r.Num -SenderTool $r.Sender -RecipientTool $r.Recipient `
        -AgentnetExe $agentnetExe -RunDir $runDir -RelayPort $port -Python $python
    $results += $res
    if ($res.Pass) { Write-Ok "round $($r.Num) ($($r.Sender) -> $($r.Recipient)): PASS ($($res.RequestId))" }
    else { Write-Fail "round $($r.Num) ($($r.Sender) -> $($r.Recipient)): $($res.Reason)" }

    if ((Get-Date) - $scriptStart -gt [TimeSpan]::FromSeconds($TotalTimeoutSeconds)) {
        Write-Fail "total run time exceeded $TotalTimeoutSeconds s; stopping"
        break
    }
}

Write-Host ""
Write-Host "=== 1.H summary ===" -ForegroundColor Cyan
foreach ($res in $results) {
    $status = if ($res.Pass) { "PASS" } else { "FAIL" }
    Write-Host ("  round {0}: {1} -> {2}: {3} ({4})" -f $res.Round, $res.Sender, $res.Recipient, $status, $res.Reason)
}
$elapsed = (Get-Date) - $scriptStart
Write-Host ("  elapsed: {0:N1}s (limit {1}s)" -f $elapsed.TotalSeconds, $TotalTimeoutSeconds)
Write-Host "  logs: $rootRun"

$allPass = ($results.Count -gt 0) -and (($results | Where-Object { -not $_.Pass }).Count -eq 0)
$withinTime = $elapsed.TotalSeconds -le $TotalTimeoutSeconds
if ($allPass -and $withinTime) { exit 0 } else { exit 1 }
