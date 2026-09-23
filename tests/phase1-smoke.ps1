<#
Phase 1 single-machine smoke test (Windows PowerShell 5.1+ / pwsh), no admin rights.
Builds the binaries, runs a local relay (--queue-db) and four daemons (A, B, C, D)
with separate config dirs as plain child processes (no service install), and drives:
pairing v2 + fingerprints; team create/invite/join (incl. a third member); presence
levels and --only-team visibility; request submit with brief/artifacts/urgency,
queued while offline then delivered; inbox ordering by urgency; accept/decline/
defer/complete with a D14 result; cancel before and after accept; the 6th-high
urgency downgrade; a local-HTTP-listener webhook with HMAC verification; the
desktop notification setting; and an audit-log content check (python3's stdlib
sqlite3, no sqlite3 CLI/CGo driver in this environment).

Does NOT cover (two machines only): reboot survival, cross-OS, a real network
relay. See tests/phase1-manual.md "Two-machine only" section.

Usage: powershell -NoProfile -File tests\phase1-smoke.ps1 [-BinDir bin] [-Build]
Exit code: 0 if all steps passed, 1 otherwise.
#>
param(
    [string]$BinDir = (Join-Path $PSScriptRoot '..\bin'),
    [switch]$Build
)
$ErrorActionPreference = 'Stop'
$repo = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$BinDir = [System.IO.Path]::GetFullPath($BinDir)
$scriptStart = Get-Date

if ($Build -or -not (Test-Path (Join-Path $BinDir 'agentnet.exe'))) {
    Write-Host "building into $BinDir"
    $env:PATH = "$env:USERPROFILE\tools\go\bin;$env:PATH"
    Push-Location $repo
    try { & go build -trimpath -o "$BinDir\" ./cmd/agentnet ./cmd/agentnetd ./cmd/relay; if ($LASTEXITCODE -ne 0) { throw 'go build failed' } }
    finally { Pop-Location }
}
$agentnet = Join-Path $BinDir 'agentnet.exe'
$agentnetd = Join-Path $BinDir 'agentnetd.exe'
$relayExe = Join-Path $BinDir 'relay.exe'

$work = Join-Path ([System.IO.Path]::GetTempPath()) ("dorylinae-p1-smoke-" + [guid]::NewGuid().ToString('N').Substring(0, 8))
$homeA = Join-Path $work 'A'; $homeB = Join-Path $work 'B'; $homeC = Join-Path $work 'C'; $homeD = Join-Path $work 'D'
New-Item -ItemType Directory -Force $homeA, $homeB, $homeC, $homeD | Out-Null
$queueDb = Join-Path $work 'relay-queue.db'

function New-LoopbackPort {
    $listener = New-Object System.Net.Sockets.TcpListener([System.Net.IPAddress]::Loopback, 0)
    $listener.Start(); $p = $listener.LocalEndpoint.Port; $listener.Stop()
    return $p
}
$port = New-LoopbackPort
$relayUrl = "ws://127.0.0.1:$port"
$hookPort = New-LoopbackPort

$script:fails = 0
$script:procs = @{}

function Step([string]$name, [bool]$ok, [string]$detail = '') {
    if ($ok) { Write-Host "PASS  $name" -ForegroundColor Green }
    else { Write-Host "FAIL  $name  $detail" -ForegroundColor Red; $script:fails++ }
}

# Run a program with extra env vars; returns @{Code; Out; Err}. Never throws on non-zero exit.
function Invoke-Prog([string]$exe, [string[]]$argv, [hashtable]$envs = @{}, [int]$timeoutMs = 20000) {
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $exe
    $psi.Arguments = ($argv | ForEach-Object { if ($_ -match '[\s"]') { '"' + ($_ -replace '"', '\"') + '"' } else { $_ } }) -join ' '
    $psi.UseShellExecute = $false; $psi.RedirectStandardOutput = $true; $psi.RedirectStandardError = $true
    $psi.CreateNoWindow = $true
    foreach ($k in $envs.Keys) { $psi.EnvironmentVariables[$k] = $envs[$k] }
    $p = [System.Diagnostics.Process]::Start($psi)
    $so = $p.StandardOutput.ReadToEndAsync(); $se = $p.StandardError.ReadToEndAsync()
    if (-not $p.WaitForExit($timeoutMs)) { try { $p.Kill() } catch {}; return @{ Code = -1; Out = ''; Err = 'timeout' } }
    $p.WaitForExit()
    return @{ Code = $p.ExitCode; Out = $so.Result; Err = $se.Result }
}

$homes = @{ A = $homeA; B = $homeB; C = $homeC; D = $homeD }
function Ag([string]$who, [string[]]$argv) {
    Invoke-Prog $agentnet $argv @{ DORYLINAE_HOME = $homes[$who] }
}
function AgJson([string]$who, [string[]]$argv) {
    $r = Ag $who ($argv + '--json')
    $j = $null
    if ($r.Out.Trim().Length -gt 0) { try { $j = $r.Out | ConvertFrom-Json } catch {} }
    return @{ Code = $r.Code; Out = $r.Out; Err = $r.Err; Json = $j }
}

# Start a long-running process with its own stdout/stderr files.
function Start-Bg([string]$key, [string]$exe, [string[]]$argv, [hashtable]$envs = @{}) {
    $old = @{}
    foreach ($k in $envs.Keys) { $old[$k] = [Environment]::GetEnvironmentVariable($k); [Environment]::SetEnvironmentVariable($k, $envs[$k]) }
    try {
        $script:procs[$key] = Start-Process -FilePath $exe -ArgumentList $argv -PassThru -WindowStyle Hidden `
            -RedirectStandardOutput (Join-Path $work "$key.out.log") -RedirectStandardError (Join-Path $work "$key.err.log")
    }
    finally { foreach ($k in $old.Keys) { [Environment]::SetEnvironmentVariable($k, $old[$k]) } }
}
function Stop-Bg([string]$key) {
    $p = $script:procs[$key]
    if ($p -and -not $p.HasExited) { try { $p.Kill() } catch {}; $p.WaitForExit(5000) | Out-Null }
}

function Wait-Until([scriptblock]$cond, [int]$seconds = 20) {
    $end = (Get-Date).AddSeconds($seconds)
    while ((Get-Date) -lt $end) { if (& $cond) { return $true }; Start-Sleep -Milliseconds 300 }
    return $false
}
function Wait-Status([string]$who, [int]$seconds = 20) { Wait-Until { (Ag $who @('status')).Code -eq 0 } $seconds }

function Get-Python3 {
    foreach ($cand in @('python3', 'python')) {
        $c = Get-Command $cand -ErrorAction SilentlyContinue
        if ($c) {
            & $c.Source -c "import sqlite3" 2>$null
            if ($LASTEXITCODE -eq 0) { return $c.Source }
        }
    }
    return $null
}
function Get-AuditRows([string]$python, [string]$dbPath) {
    if (-not (Test-Path $dbPath)) { return @() }
    $code = "import sqlite3,sys,json; con=sqlite3.connect(sys.argv[1]); [print(json.dumps({'action':a,'detail':d})) for a,d in con.execute('select action, detail from audit_events order by id')]"
    $out = & $python -c $code $dbPath 2>$null
    if ($LASTEXITCODE -ne 0 -or -not $out) { return @() }
    return @($out | Where-Object { $_.Trim().Length -gt 0 } | ForEach-Object { $_ | ConvertFrom-Json })
}

function ConvertFrom-Base64Url([string]$s) {
    $s = $s.Replace('-', '+').Replace('_', '/')
    switch ($s.Length % 4) { 2 { $s += '==' } 3 { $s += '=' } }
    return [Convert]::FromBase64String($s)
}
function Get-HmacSha256Base64Url([byte[]]$key, [string]$data) {
    $hmac = New-Object System.Security.Cryptography.HMACSHA256
    $hmac.Key = $key
    $hash = $hmac.ComputeHash([System.Text.Encoding]::UTF8.GetBytes($data))
    $b64 = [Convert]::ToBase64String($hash)
    return ($b64.TrimEnd('=').Replace('+', '-').Replace('/', '_'))
}

try {
    Write-Host "work dir: $work  relay: $relayUrl  hook port: $hookPort"

    # 1. relay + four daemons (A, B, C, D), no admin, all foreground child processes
    Start-Bg 'relay' $relayExe @('--listen', "127.0.0.1:$port", '--queue-db', $queueDb, '--verbose')
    $up = Wait-Until { (Test-Path (Join-Path $work 'relay.out.log')) -and ((Get-Content (Join-Path $work 'relay.out.log') -Raw) -match 'listening') } 10
    Step 'relay starts and listens' $up

    # DORYLINAE_KEYSTORE=file: the webhook secret's OS-keychain account is the literal
    # string "webhook" (internal/daemon/daemon.go webhookKeystore), not scoped per config
    # dir like the identity key's account is. With several daemons on one machine, they
    # collide in Windows Credential Manager: whichever daemon sets/rotates its webhook last
    # silently overwrites every other daemon's stored secret, and the ones it printed
    # earlier stop matching what actually signs deliveries. This looks like a real product
    # bug (see the run report); the file backend sidesteps it here since it IS scoped per
    # config dir.
    $envCommon = @{ DORYLINAE_RELAY_URL = $relayUrl; DORYLINAE_KEYSTORE = 'file' }
    foreach ($who in @('A', 'B', 'C', 'D')) {
        Start-Bg $who $agentnetd @('run') (@{ DORYLINAE_HOME = $homes[$who] } + $envCommon)
    }
    $okAll = $true
    foreach ($who in @('A', 'B', 'C', 'D')) { $okAll = (Wait-Status $who) -and $okAll }
    Step 'status: A, B, C, D all running' $okAll
    if (-not $okAll) { throw 'daemons did not start; see logs' }

    # 2. identity + pairing v2 + fingerprints (A <-> B)
    $ia = (AgJson 'A' @('identity')).Json
    $ib = (AgJson 'B' @('identity')).Json
    Step 'identity: A and B have 20-char fingerprints' (($ia.fingerprint.Length -eq 20) -and ($ib.fingerprint.Length -eq 20))
    Step 'identity: fingerprints differ' ($ia.fingerprint -ne $ib.fingerprint)
    $refA = $ia.card.public_key; $refB = $ib.card.public_key

    $n = AgJson 'A' @('pair', '--new')
    $code = $n.Json.code
    Step 'pair --new returns a 15-char v2 code' ($code -and (($code -replace '[-\s]', '').Length -eq 15)) $n.Out
    $r = AgJson 'B' @('pair', $code)
    Step 'pair <code> completes with trust=code' (($r.Json.state -eq 'complete') -and ($r.Json.peer.trust -eq 'code')) $r.Out

    $r = AgJson 'A' @('peers', 'verify', $refB, ($ib.fingerprint -replace '(.{4})(?!$)', '$1 '))
    Step 'peers verify with correct fingerprint sets trust=fingerprint' (($r.Code -eq 0) -and ($r.Json.peer.trust -eq 'fingerprint')) $r.Out
    $bad = $ib.fingerprint.ToCharArray(); $bad[0] = if ($bad[0] -eq 'A') { 'B' } else { 'A' }
    $r = AgJson 'A' @('peers', 'verify', $refB, (-join $bad))
    Step 'peers verify with wrong fingerprint is rejected' (($r.Code -eq 1) -and ($r.Out -match 'fingerprint_mismatch')) $r.Out

    # 3. team create, invite, join (incl. a third member)
    $t = AgJson 'A' @('team', 'create', 'backend')
    Step 'team create: A owns backend' (($t.Code -eq 0) -and ($t.Json.team.role -eq 'owner')) $t.Out
    $inv = AgJson 'A' @('team', 'invite', 'backend')
    Step 'team invite: returns a code' (($inv.Code -eq 0) -and $inv.Json.code) $inv.Out
    $j = AgJson 'B' @('team', 'join', $inv.Json.code)
    Step 'team join: B joins' (($j.Code -eq 0) -and ($j.Json.state -eq 'complete')) $j.Out
    $rosterOk = Wait-Until { $s = AgJson 'A' @('team', 'show', 'backend'); @($s.Json.team.members).Count -ge 2 } 20
    Step 'team show: A sees 2 members after B joins' $rosterOk

    $inv2 = AgJson 'A' @('team', 'invite', 'backend')
    Step 'team invite: second code for C' (($inv2.Code -eq 0) -and $inv2.Json.code) $inv2.Out
    $j2 = AgJson 'C' @('team', 'join', $inv2.Json.code)
    Step 'team join: C (a third member) joins' (($j2.Code -eq 0) -and ($j2.Json.state -eq 'complete')) $j2.Out
    $rosterOk3 = Wait-Until { $s = AgJson 'A' @('team', 'show', 'backend'); @($s.Json.team.members).Count -ge 3 } 20
    Step 'team show: A sees 3 members (A, B, C) after C joins' $rosterOk3
    $listOk = Wait-Until { $l = AgJson 'C' @('team', 'list'); @($l.Json.teams | Where-Object { $_.name -eq 'backend' }).Count -ge 1 } 20
    Step 'team list: C sees backend' $listOk

    # D shares a *different* team with B ("outsiders", not "backend"), so it can act as
    # an outside-the-team peer for the --only-team visibility check below (presence is
    # scoped per team, not per pairing: a peer paired directly but sharing no team would
    # not even reach the "does B share backend with me" check the same way a real outside
    # teammate does).
    $to = AgJson 'D' @('team', 'create', 'outsiders')
    Step 'team create: D owns outsiders (for the outside-peer presence check)' ($to.Code -eq 0) $to.Out
    $invD = AgJson 'D' @('team', 'invite', 'outsiders')
    $rd = AgJson 'B' @('team', 'join', $invD.Json.code)
    Step 'B joins D''s outsiders team (B is now on two teams)' ($rd.Json.state -eq 'complete') $rd.Out
    $rosterOkD = Wait-Until { $s = AgJson 'D' @('team', 'show', 'outsiders'); @($s.Json.team.members).Count -ge 2 } 20
    Step 'team show: D sees B on outsiders' $rosterOkD

    # 4. presence: status --team with all three levels, invisible/only-team/human-off, offline after stop
    $st = AgJson 'A' @('status', '--team', 'backend')
    $bMember = $st.Json.team.members | Where-Object { $_.public_key -eq $refB }
    Step 'status --team: B shows online with a last_seen' (($bMember.daemon_online -eq $true) -and $bMember.last_seen) ($bMember | ConvertTo-Json -Compress)

    $inv3 = AgJson 'B' @('presence', '--invisible')
    Step 'presence --invisible: B reports invisible' ($inv3.Json.mode -eq 'invisible') $inv3.Out
    $seenOffline = Wait-Until { $s = AgJson 'A' @('status', '--team', 'backend'); $m = $s.Json.team.members | Where-Object { $_.public_key -eq $refB }; -not $m.daemon_online } 15
    Step 'presence: A sees B go offline at once after --invisible' $seenOffline

    $vis = AgJson 'B' @('presence', '--visible')
    Step 'presence --visible: B reports visible' ($vis.Json.mode -eq 'visible') $vis.Out
    $seenOnline = Wait-Until { $s = AgJson 'A' @('status', '--team', 'backend'); $m = $s.Json.team.members | Where-Object { $_.public_key -eq $refB }; $m.daemon_online } 15
    Step 'presence: A sees B online again after --visible' $seenOnline

    $ot = AgJson 'B' @('presence', '--only-team', 'backend')
    Step 'presence --only-team: B reports only_team backend' (($ot.Json.mode -eq 'only_team') -and ($ot.Json.team.name -eq 'backend')) $ot.Out
    $selfCheck = AgJson 'B' @('presence')
    Step 'presence (no flags): B still reports only_team backend' (($selfCheck.Json.mode -eq 'only_team') -and ($selfCheck.Json.team.name -eq 'backend')) $selfCheck.Out
    $outsideOffline = Wait-Until {
        $s = AgJson 'D' @('status', '--team', 'outsiders')
        $m = $s.Json.team.members | Where-Object { $_.public_key -eq $refB }
        $m -and (-not $m.daemon_online)
    } 15
    Step 'presence --only-team: a peer on a different team sees B as never seen / offline' $outsideOffline
    $vis2 = AgJson 'B' @('presence', '--visible')
    Step 'presence: B back to visible for the rest of the run' ($vis2.Json.mode -eq 'visible') $vis2.Out
    $hOff = AgJson 'B' @('presence', '--human', 'off')
    Step 'presence --human off: human sharing turned off' ($hOff.Json.human_share -eq $false) $hOff.Out
    $hOn = AgJson 'B' @('presence', '--human', 'on')
    Step 'presence --human on: human sharing turned back on' ($hOn.Json.human_share -eq $true) $hOn.Out

    # 5. request with brief/artifacts/urgency, queued while B is stopped, then delivered
    Stop-Bg 'B'
    Step 'status: B not running before offline request (exit 3)' ((Ag 'B' @('status')).Code -eq 3)
    $offReq = AgJson 'A' @('request', $refB, 'task', '--title', 'Offline artifact request', `
            '--brief', "What: SMOKEMARKBRIEF check the artifact`nWhy: smoke test`nDone when: reviewed", `
            '--urgency', 'blocking', '--urgency-reason', 'smoke test needs a blocking sample', `
            '--artifact', 'url=https://example.test/x branch=main commit=abcdef1234567890 path=foo/bar', `
            '--idempotency-key', 'p1smoke-offline-1')
    # Presence considers a killed daemon "online" until its heartbeat times out (up to ~75s;
    # Docs/protocol/presence.md "Daemon killed on B (no goodbye)"), so peer.daemon_online
    # right after a hard kill is not asserted here; "queued" plus later delivery is.
    Step 'request: accepted as queued while B is offline, urgency blocking, has an artifact' `
        (($offReq.Code -eq 0) -and ($offReq.Json.status -eq 'queued') -and ($offReq.Json.urgency -eq 'blocking')) $offReq.Out
    $offReqId = $offReq.Json.id
    Start-Bg 'B' $agentnetd @('run') (@{ DORYLINAE_HOME = $homeB } + $envCommon)
    Step 'status: B running again' (Wait-Status 'B')
    $delivered = Wait-Until { $inb = AgJson 'B' @('inbox'); @($inb.Json.requests | Where-Object { $_.id -eq $offReqId }).Count -ge 1 } 30
    Step 'request: delivered to B after it restarts' $delivered
    $showOff = AgJson 'A' @('request', 'show', $offReqId)
    Step 'request show: artifact is present on A''s mirror' (@($showOff.Json.request.artifacts).Count -ge 1) $showOff.Out

    # 6. inbox order by urgency: high, normal, low -> high first, then normal, then low
    $rLow = AgJson 'A' @('request', $refB, 'question', '--title', 'Low prio', '--brief', 'What: low', '--urgency', 'low', '--idempotency-key', 'p1smoke-order-low')
    Start-Sleep -Milliseconds 200
    $rHigh = AgJson 'A' @('request', $refB, 'review', '--title', 'High prio', '--brief', 'What: high', '--urgency', 'high', '--urgency-reason', 'smoke order test', '--idempotency-key', 'p1smoke-order-high-1')
    Start-Sleep -Milliseconds 200
    $rNorm = AgJson 'A' @('request', $refB, 'task', '--title', 'Normal prio', '--brief', 'What: normal', '--idempotency-key', 'p1smoke-order-normal')
    $ids = @($rLow.Json.id, $rHigh.Json.id, $rNorm.Json.id)
    $allIn = Wait-Until { $inb = AgJson 'B' @('inbox'); @($ids | Where-Object { $id = $_; @($inb.Json.requests | Where-Object { $_.id -eq $id }).Count -ge 1 }).Count -eq 3 } 20
    Step 'inbox: all three ordering requests arrived' $allIn
    $inbOrder = (AgJson 'B' @('inbox')).Json.requests
    $orderedIds = @($inbOrder | Where-Object { $ids -contains $_.id } | Select-Object -ExpandProperty id)
    Step 'inbox: ordered high, normal, low' (($orderedIds.Count -eq 3) -and ($orderedIds[0] -eq $rHigh.Json.id) -and ($orderedIds[1] -eq $rNorm.Json.id) -and ($orderedIds[2] -eq $rLow.Json.id)) ($orderedIds -join ',')

    # 7. accept / decline / defer / complete with a D14 result
    $acc = AgJson 'B' @('accept', $rHigh.Json.id)
    Step 'accept: B accepts the high-priority request' ($acc.Code -eq 0) $acc.Out
    $accSeen = Wait-Until { $s = AgJson 'A' @('request', 'show', $rHigh.Json.id); $s.Json.request.state -eq 'accepted' } 15
    Step 'accept: A''s mirror shows accepted' $accSeen

    $dec = AgJson 'B' @('decline', $rNorm.Json.id, '--reason', 'SMOKEMARKDECLINE not needed for the smoke test')
    Step 'decline: B declines the normal request' ($dec.Code -eq 0) $dec.Out
    $decSeen = Wait-Until { $s = AgJson 'A' @('request', 'show', $rNorm.Json.id); $s.Json.request.state -eq 'declined' } 15
    Step 'decline: A''s mirror shows declined' $decSeen

    $def = AgJson 'B' @('defer', $rLow.Json.id, '--until', '2h')
    Step 'defer: B defers the low-priority request' ($def.Code -eq 0) $def.Out
    $defSeen = Wait-Until { $s = AgJson 'A' @('request', 'show', $rLow.Json.id); $s.Json.request.state -eq 'deferred' } 15
    Step 'defer: A''s mirror shows deferred' $defSeen

    $outFile = Join-Path $work 'complete-output.txt'
    Set-Content -Path $outFile -Value "SMOKEMARKOUTPUT line 1`nline 2" -Encoding utf8 -NoNewline
    $comp = AgJson 'B' @('complete', $rHigh.Json.id, '--note', 'SMOKEMARKNOTE all good', `
            '--status', 'pass', '--summary', 'SMOKEMARKSUMMARY all green', '--exit-code', '0', `
            '--output-from-file', $outFile, '--artifact', 'url=https://example.test/y branch=main commit=1234567890abcdef path=out/log')
    Step 'complete: B completes with a D14 result' ($comp.Code -eq 0) $comp.Out
    $compSeen = Wait-Until { $s = AgJson 'A' @('request', 'show', $rHigh.Json.id); $s.Json.request.state -eq 'completed' } 15
    Step 'complete: A''s mirror shows completed' $compSeen
    $showComp = (AgJson 'A' @('request', 'show', $rHigh.Json.id)).Json.request
    Step 'complete: A''s mirror has the D14 result (status, summary, output, artifact)' `
        (($showComp.result.status -eq 'pass') -and ($showComp.result.summary -match 'SMOKEMARKSUMMARY') -and ($showComp.result.output -match 'SMOKEMARKOUTPUT') -and (@($showComp.result.artifacts).Count -ge 1)) `
        ($showComp | ConvertTo-Json -Compress -Depth 6)

    # 8. cancel before accept -> cancelled; cancel after accept -> refused
    $rc1 = AgJson 'A' @('request', $refB, 'task', '--title', 'Cancel before accept', '--brief', 'What: c1', '--idempotency-key', 'p1smoke-cancel-1')
    $arrived1 = Wait-Until { $inb = AgJson 'B' @('inbox'); @($inb.Json.requests | Where-Object { $_.id -eq $rc1.Json.id }).Count -ge 1 } 15
    Step 'cancel test: request 1 reached B''s inbox' $arrived1
    $can1 = AgJson 'A' @('request', 'cancel', $rc1.Json.id, '--reason', 'SMOKEMARKCANCEL not needed')
    Step 'cancel before accept: cancel accepted locally' ($can1.Code -eq 0) $can1.Out
    $lastInb1 = $null
    $can1Seen = Wait-Until { $script:lastInb1 = AgJson 'B' @('inbox', '--all'); @($lastInb1.Json.requests | Where-Object { $_.id -eq $rc1.Json.id -and $_.state -eq 'cancelled' }).Count -ge 1 } 30
    Step 'cancel before accept: B''s inbox --all shows cancelled' $can1Seen "looking for $($rc1.Json.id): $($lastInb1.Out)"
    $can1SeenA = Wait-Until { $s = AgJson 'A' @('request', 'show', $rc1.Json.id); $s.Json.request.state -eq 'cancelled' } 30
    Step 'cancel before accept: A''s mirror shows cancelled' $can1SeenA

    # Per Docs/protocol/request.md "After accept the sender cannot cancel": once A's own
    # mirror has observed the accept, request_cancel refuses locally with bad_state. The
    # "refused" outcome only happens when A's cancel is created while A's own mirror still
    # reads "pending", and *then* reaches B after B has already accepted. On one machine,
    # racing a CLI process launch against an already-open loopback relay connection is not
    # reliable (the queued "accepted" confirmation typically wins). To make this
    # deterministic instead of racy: stop A, have B accept while A is offline (the
    # confirmation queues at the relay), then bring A back up with NO relay connection at
    # all (no DORYLINAE_RELAY_URL) so it is physically unable to receive that confirmation
    # -- A's mirror is certainly still "pending" -- fire the cancel there (queues locally in
    # A's own outbox), then restart A once more with the relay reconnected so both the
    # queued cancel (A -> B) and the queued accept confirmation (B -> A) actually flow.
    $rc2 = AgJson 'A' @('request', $refB, 'task', '--title', 'Cancel after accept', '--brief', 'What: c2', '--idempotency-key', 'p1smoke-cancel-2')
    $arrived2 = Wait-Until { $inb = AgJson 'B' @('inbox'); @($inb.Json.requests | Where-Object { $_.id -eq $rc2.Json.id }).Count -ge 1 } 30
    Step 'cancel test: request 2 reached B''s inbox' $arrived2

    Stop-Bg 'A'
    Step 'status: A not running before the accept-then-cancel test (exit 3)' ((Ag 'A' @('status')).Code -eq 3)
    $acc2 = AgJson 'B' @('accept', $rc2.Json.id)
    Step 'cancel after accept: B accepts request 2 while A is offline' ($acc2.Code -eq 0) $acc2.Out
    # A with no relay URL at all: it cannot possibly receive B's queued "accepted" mail, so
    # its own mirror is guaranteed to still read "pending" when we cancel below.
    Start-Bg 'A' $agentnetd @('run') @{ DORYLINAE_HOME = $homeA; DORYLINAE_KEYSTORE = 'file' }
    Step 'status: A running again, deliberately relay-less' (Wait-Status 'A')
    $can2 = AgJson 'A' @('request', 'cancel', $rc2.Json.id)
    Step 'cancel after accept: cancel accepted locally while A''s mirror still reads pending' `
        (($can2.Code -eq 0) -and ($can2.Json.request.state -eq 'pending')) $can2.Out
    Stop-Bg 'A'
    Start-Bg 'A' $agentnetd @('run') (@{ DORYLINAE_HOME = $homeA } + $envCommon)
    Step 'status: A running again with the relay reconnected' (Wait-Status 'A')
    $can2Refused = Wait-Until { $s = AgJson 'A' @('request', 'show', $rc2.Json.id); ($s.Json.request.state -eq 'accepted') -and ($s.Json.request.cancel -eq 'refused') } 30
    Step 'cancel after accept: A''s mirror shows cancel=refused, state stays accepted' $can2Refused

    # 9. the urgency limit: the 6th high in the run arrives as normal with a note
    # rHigh above was high #1; send five more to reach the 6th.
    $downgraded = $null
    for ($i = 2; $i -le 6; $i++) {
        $rh = AgJson 'A' @('request', $refB, 'task', '--title', "High #$i", '--brief', "What: high $i", `
                '--urgency', 'high', '--urgency-reason', 'smoke urgency-limit test', '--idempotency-key', "p1smoke-highlimit-$i")
        if ($i -eq 6) { $downgraded = $rh }
    }
    Step 'urgency limit: the 6th high request is downgraded to normal with urgency_declared=high and a note' `
        (($downgraded.Code -eq 0) -and ($downgraded.Json.urgency -eq 'normal') -and ($downgraded.Json.urgency_declared -eq 'high') -and $downgraded.Json.urgency_note) $downgraded.Out

    # 10. webhook to a local HTTP listener, HMAC signature verified
    $listener = New-Object System.Net.HttpListener
    $hookPrefix = "http://127.0.0.1:$hookPort/hook/"
    $listener.Prefixes.Add($hookPrefix)
    $listener.Start()
    try {
        $whB = AgJson 'B' @('notify', '--webhook', $hookPrefix, '--webhook-title', 'on')
        Step 'notify --webhook: B gets a whsec_ secret' (($whB.Code -eq 0) -and ($whB.Json.secret -like 'whsec_*')) $whB.Out
        $whA = AgJson 'A' @('notify', '--webhook', $hookPrefix, '--webhook-title', 'on')
        Step 'notify --webhook: A gets a whsec_ secret' (($whA.Code -eq 0) -and ($whA.Json.secret -like 'whsec_*')) $whA.Out
        $secretB = ConvertFrom-Base64Url ($whB.Json.secret -replace '^whsec_', '')
        $secretA = ConvertFrom-Base64Url ($whA.Json.secret -replace '^whsec_', '')

        $wreq = AgJson 'A' @('request', $refB, 'task', '--title', 'Webhook test', '--brief', 'What: webhook', '--idempotency-key', 'p1smoke-webhook-1')
        Wait-Until { $inb = AgJson 'B' @('inbox'); @($inb.Json.requests | Where-Object { $_.id -eq $wreq.Json.id }).Count -ge 1 } 15 | Out-Null
        AgJson 'B' @('accept', $wreq.Json.id) | Out-Null

        $posts = New-Object System.Collections.Generic.List[object]
        $deadline = (Get-Date).AddSeconds(20)
        while ((Get-Date) -lt $deadline -and $posts.Count -lt 2) {
            $ctxTask = $listener.GetContextAsync()
            if ($ctxTask.Wait(2000)) {
                $ctx = $ctxTask.Result
                $body = (New-Object IO.StreamReader($ctx.Request.InputStream)).ReadToEnd()
                $posts.Add(@{ Headers = $ctx.Request.Headers; Body = $body })
                $ctx.Response.StatusCode = 200
                $ctx.Response.OutputStream.Close()
            }
        }
        Step 'webhook: at least 2 signed deliveries arrived (request.received + request.accepted)' ($posts.Count -ge 2) "$($posts.Count) received"

        $verified = 0
        foreach ($post in $posts) {
            $payload = $post.Body | ConvertFrom-Json
            $sigHeader = $post.Headers['Dorylinae-Signature']
            $idHeader = $post.Headers['Dorylinae-Webhook-Id']
            $tsHeader = $post.Headers['Dorylinae-Webhook-Timestamp']
            if (-not $sigHeader -or $sigHeader -notmatch '^v1=(.+)$') { continue }
            $sig = $Matches[1]
            $secret = if ($payload.event -eq 'request.received') { $secretB } else { $secretA }
            $signedContent = 'v1:' + $tsHeader + ':' + $idHeader + ':' + $post.Body
            $expect = Get-HmacSha256Base64Url $secret $signedContent
            if (($expect -eq $sig) -and ($idHeader -eq $payload.id)) { $verified++ }
        }
        Step 'webhook: HMAC signature verifies for every delivery received' ($verified -eq $posts.Count -and $posts.Count -ge 2) "$verified/$($posts.Count) verified"

        $offA = AgJson 'A' @('notify', '--webhook', 'off')
        $offB = AgJson 'B' @('notify', '--webhook', 'off')
        Step 'notify --webhook off: removed on both sides' (($offA.Json.webhook -eq $null) -and ($offB.Json.webhook -eq $null)) "$($offA.Out) $($offB.Out)"
    }
    finally {
        $listener.Stop(); $listener.Close()
    }

    # 11. desktop notification setting: check the setting and that the call path runs
    $dOff = AgJson 'B' @('notify', '--desktop', 'off')
    Step 'notify --desktop off' ($dOff.Json.desktop -eq $false) $dOff.Out
    $tOff = AgJson 'B' @('notify', '--test')
    Step 'notify --test with desktop off reports disabled' (($tOff.Code -eq 0) -and ($tOff.Json.desktop -eq 'disabled')) $tOff.Out
    $dOn = AgJson 'B' @('notify', '--desktop', 'on')
    Step 'notify --desktop on' ($dOn.Json.desktop -eq $true) $dOn.Out
    $tOn = AgJson 'B' @('notify', '--test')
    Step 'notify --test with desktop on runs the call path (shown or failed, not disabled)' `
        (($tOn.Code -eq 0) -and ($tOn.Json.desktop -in @('shown', 'failed'))) $tOn.Out

    # 12. audit log has the events and no content
    $python = Get-Python3
    if (-not $python) {
        Step 'audit: python3 with sqlite3 available to read audit_events' $false 'no python3/sqlite3 found; see tests/harness/README.md'
    }
    else {
        $rowsA = Get-AuditRows $python (Join-Path $homeA 'dorylinae.db')
        $rowsB = Get-AuditRows $python (Join-Path $homeB 'dorylinae.db')
        $allRows = @($rowsA) + @($rowsB)
        $actions = $allRows | Select-Object -ExpandProperty action
        $needed = @('request.submit', 'request.in', 'request.accept', 'request.decline', 'request.defer', 'request.complete', 'team.create', 'team.join', 'notify.config')
        $missing = $needed | Where-Object { $actions -notcontains $_ }
        Step 'audit: all expected P1 actions are present across A and B' ($missing.Count -eq 0) "missing: $($missing -join ', ')"

        $markers = @('SMOKEMARKBRIEF', 'SMOKEMARKDECLINE', 'SMOKEMARKNOTE', 'SMOKEMARKSUMMARY', 'SMOKEMARKOUTPUT', 'SMOKEMARKCANCEL')
        $leak = $false; $leakDetail = ''
        foreach ($row in $allRows) {
            foreach ($m in $markers) {
                if ($row.detail -and ($row.detail -match $m)) { $leak = $true; $leakDetail = "$($row.action): $m" }
            }
        }
        Step 'audit: no request content (title/brief/reason/note/summary/output) leaks into audit_events' (-not $leak) $leakDetail
    }
}
catch {
    Write-Host "ABORT  $($_.Exception.Message)" -ForegroundColor Red
    Write-Host $_.ScriptStackTrace
    $script:fails++
}
finally {
    foreach ($k in @('A', 'B', 'C', 'D', 'relay')) { Stop-Bg $k }
    if ($script:fails -gt 0) { Write-Host "logs kept in $work" }
    else { Start-Sleep -Milliseconds 500; Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue }
}
$elapsed = (Get-Date) - $scriptStart
Write-Host ("elapsed: {0:N1}s" -f $elapsed.TotalSeconds)
if ($script:fails -eq 0) { Write-Host 'ALL PASS' -ForegroundColor Green; exit 0 }
Write-Host "$($script:fails) FAILED" -ForegroundColor Red; exit 1
