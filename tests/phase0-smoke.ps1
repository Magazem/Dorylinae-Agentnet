<#
Phase 0 single-machine smoke test (Windows PowerShell 5.1+ / pwsh).
Runs a local relay and two daemons (A, B) with separate config dirs, then: status, identity,
pair v2, peers verify, ping, stop/start B, offline mail (debug `note` kind; needs DORYLINAE_DEBUG=1,
which the script sets for both daemons), relay restart, peers remove. Prints PASS/FAIL per step.
Does NOT cover: service install, reboot, real network (see tests/phase0-manual.md).

Usage: powershell -NoProfile -File tests\phase0-smoke.ps1 [-BinDir bin] [-Build]
Exit code: 0 if all steps passed, 1 otherwise.
#>
param(
    [string]$BinDir = (Join-Path $PSScriptRoot '..\bin'),
    [switch]$Build
)
$ErrorActionPreference = 'Stop'
$repo = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$BinDir = [System.IO.Path]::GetFullPath($BinDir)

if ($Build -or -not (Test-Path (Join-Path $BinDir 'agentnet.exe'))) {
    Write-Host "building into $BinDir"
    Push-Location $repo
    try { & go build -trimpath -o "$BinDir\" ./cmd/agentnet ./cmd/agentnetd ./cmd/relay; if ($LASTEXITCODE -ne 0) { throw 'go build failed' } }
    finally { Pop-Location }
}
$agentnet = Join-Path $BinDir 'agentnet.exe'
$agentnetd = Join-Path $BinDir 'agentnetd.exe'
$relayExe = Join-Path $BinDir 'relay.exe'

$work = Join-Path ([System.IO.Path]::GetTempPath()) ("dorylinae-smoke-" + [guid]::NewGuid().ToString('N').Substring(0, 8))
$homeA = Join-Path $work 'A'; $homeB = Join-Path $work 'B'
New-Item -ItemType Directory -Force $homeA, $homeB | Out-Null
$queueDb = Join-Path $work 'relay-queue.db'

$listener = New-Object System.Net.Sockets.TcpListener([System.Net.IPAddress]::Loopback, 0)
$listener.Start(); $port = $listener.LocalEndpoint.Port; $listener.Stop()
$relayUrl = "ws://127.0.0.1:$port"

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

function Ag([string]$who, [string[]]$argv) {
    $h = if ($who -eq 'A') { $homeA } else { $homeB }
    Invoke-Prog $agentnet $argv @{ DORYLINAE_HOME = $h; DORYLINAE_DEBUG = '1' }
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
function Wait-Ping([string]$from, [string]$peer, [int]$seconds = 30) {
    $script:lastPing = $null
    Wait-Until {
        $r = Ag $from @('ping', "@$peer", '--json'); $script:lastPing = $r
        ($r.Code -eq 0) -and ($r.Out -match '"state":\s*"complete"')
    } $seconds
}

try {
    Write-Host "work dir: $work  relay: $relayUrl"

    # 1. relay (loopback; v2 pairing is always on)
    Start-Bg 'relay' $relayExe @('--listen', "127.0.0.1:$port", '--queue-db', $queueDb, '--verbose')
    $up = Wait-Until { (Test-Path (Join-Path $work 'relay.out.log')) -and ((Get-Content (Join-Path $work 'relay.out.log') -Raw) -match 'listening') } 10
    Step 'relay starts and listens' $up

    # 2. daemons + status
    # DORYLINAE_DEBUG=1: the daemons accept the debug mail kind "note" (offline mail step)
    $envA = @{ DORYLINAE_HOME = $homeA; DORYLINAE_RELAY_URL = $relayUrl; DORYLINAE_DEBUG = '1' }
    $envB = @{ DORYLINAE_HOME = $homeB; DORYLINAE_RELAY_URL = $relayUrl; DORYLINAE_DEBUG = '1' }
    Start-Bg 'A' $agentnetd @('run') $envA
    Start-Bg 'B' $agentnetd @('run') $envB
    $sa = Wait-Status 'A'; $sb = Wait-Status 'B'
    Step 'status: daemon A running' $sa; Step 'status: daemon B running' $sb
    if (-not ($sa -and $sb)) { throw 'daemons did not start; see logs' }
    $pidB1 = (Ag 'B' @('status', '--json')).Out | ConvertFrom-Json | Select-Object -ExpandProperty pid

    # 3. identity
    $ia = (Ag 'A' @('identity', '--json')).Out | ConvertFrom-Json
    $ib = (Ag 'B' @('identity', '--json')).Out | ConvertFrom-Json
    Step 'identity: both have 20-char fingerprints' (($ia.fingerprint.Length -eq 20) -and ($ib.fingerprint.Length -eq 20))
    Step 'identity: fingerprints differ' ($ia.fingerprint -ne $ib.fingerprint)
    $nameA = $ia.card.name; $nameB = $ib.card.name
    if ($nameA -eq $nameB) { Write-Host "note: both agents are named '$nameA'; using public keys" }
    $refA = if ($nameA -eq $nameB) { $ia.card.public_key } else { $nameA }
    $refB = if ($nameA -eq $nameB) { $ib.card.public_key } else { $nameB }

    # 4. pair v2: A issues, B redeems
    $n = Ag 'A' @('pair', '--new', '--json'); $nj = $n.Out | ConvertFrom-Json
    $code = $null
    if ($nj.code) { $code = $nj.code }
    else { Wait-Until { $s = (Ag 'A' @('pair', '--status', $nj.pairing_id, '--json')).Out | ConvertFrom-Json; if ($s.code) { $script:code = $s.code; $true } else { $false } } 5 | Out-Null; $code = $script:code }
    Step 'pair --new returns a 15-char v2 code' ($code -and (($code -replace '[-\s]', '').Length -eq 15)) $n.Out
    $r = Ag 'B' @('pair', $code, '--json'); $rj = $r.Out | ConvertFrom-Json
    if ($rj.state -eq 'pending') {
        Wait-Until { $s = (Ag 'B' @('pair', '--status', $rj.pairing_id, '--json')).Out | ConvertFrom-Json; if ($s.state -ne 'pending') { $script:rj = $s; $true } else { $false } } 30 | Out-Null
        $rj = $script:rj
    }
    Step 'pair <code> completes with trust=code' (($rj.state -eq 'complete') -and ($rj.peer.trust -eq 'code')) $r.Out
    $peersA = $null
    $okA = Wait-Until { $script:peersA = ((Ag 'A' @('peers', '--json')).Out | ConvertFrom-Json).peers; $script:peersA.Count -eq 1 } 30
    Step 'peers: A lists B' $okA
    $peersB = ((Ag 'B' @('peers', '--json')).Out | ConvertFrom-Json).peers
    Step 'peers: fingerprints match identity' (($script:peersA[0].fingerprint -eq $ib.fingerprint) -and ($peersB[0].fingerprint -eq $ia.fingerprint))
    $r = Ag 'B' @('pair', $code, '--json')
    Step 'pair: reusing the code fails (code_used)' (($r.Code -ne 0) -and ($r.Out -match 'code_used'))

    # 5. verify
    $r = Ag 'A' @('peers', 'verify', $refB, ($ib.fingerprint -replace '(.{4})(?!$)', '$1 '))
    Step 'peers verify with correct fingerprint' ($r.Code -eq 0) $r.Err
    $bad = $ib.fingerprint.ToCharArray(); $bad[0] = if ($bad[0] -eq 'A') { 'B' } else { 'A' }
    $r = Ag 'A' @('peers', 'verify', $refB, (-join $bad), '--json')
    Step 'peers verify with wrong fingerprint is rejected' (($r.Code -eq 1) -and ($r.Out -match 'fingerprint_mismatch')) $r.Out
    $tr = ((Ag 'A' @('peers', '--json')).Out | ConvertFrom-Json).peers[0].trust
    Step 'peers: trust is fingerprint after verify' ($tr -eq 'fingerprint') "trust=$tr"

    # 6. ping both ways
    Step 'ping A -> B' (Wait-Ping 'A' $refB) $script:lastPing.Out
    Step 'ping B -> A' (Wait-Ping 'B' $refA) $script:lastPing.Out

    # 7. stop B, ping, restart B
    Stop-Bg 'B'
    Step 'status: B not running after stop (exit 3)' ((Ag 'B' @('status')).Code -eq 3)
    $r = Ag 'A' @('ping', "@$refB", '--json')
    if ($r.Out -match '"pending"') {
        $id = ($r.Out | ConvertFrom-Json).ping_id
        Wait-Until { $s = Ag 'A' @('ping', '--status', $id, '--json'); $script:r = $s; $s.Out -notmatch '"pending"' } 15 | Out-Null
        $r = $script:r
    }
    Step 'ping to stopped B fails cleanly' (($r.Code -ne 0) -and ($r.Out -match '"failed"|peer_offline|timeout')) $r.Out
    Step 'daemon A still running' ((Ag 'A' @('status')).Code -eq 0)
    Start-Bg 'B' $agentnetd @('run') $envB
    Step 'status: B running again' (Wait-Status 'B')
    $pidB2 = (Ag 'B' @('status', '--json')).Out | ConvertFrom-Json | Select-Object -ExpandProperty pid
    Step 'status: B has a new PID' ($pidB1 -ne $pidB2) "$pidB1 -> $pidB2"
    Step 'peers survive B restart' ((((Ag 'B' @('peers', '--json')).Out | ConvertFrom-Json).peers.Count) -eq 1)
    Step 'ping A -> B after B restart' (Wait-Ping 'A' $refB 40) $script:lastPing.Out

    # 7b. offline mail: A sends while B is stopped; B receives it after restart and the ack reaches A.
    # Both daemons run with DORYLINAE_DEBUG=1, so B understands the debug kind "note".
    $r = Invoke-Prog $agentnet @('mail', 'send', "@$refB", '--kind', 'note', '--text', 'x') @{ DORYLINAE_HOME = $homeA; DORYLINAE_DEBUG = '0' }
    Step 'mail send does not exist without DORYLINAE_DEBUG=1' (($r.Code -eq 2) -and ($r.Err -match 'unknown command')) $r.Err
    Stop-Bg 'B'
    Step 'status: B not running before offline mail (exit 3)' ((Ag 'B' @('status')).Code -eq 3)
    $r = Ag 'A' @('mail', 'send', "@$refB", '--kind', 'note', '--text', 'sent while B was offline', '--json')
    $mj = $null; try { $mj = $r.Out | ConvertFrom-Json } catch {}
    Step 'mail send to stopped B is accepted as queued' (($r.Code -eq 0) -and $mj -and ($mj.ok -eq $true) -and ($mj.state -eq 'queued') -and ($mj.id -like 'm-*')) "$($r.Out) $($r.Err)"
    $ob = ((Ag 'A' @('status', '--json')).Out | ConvertFrom-Json).outbox
    Step 'status: outbox has the mail waiting' (($ob.queued + $ob.relayed) -ge 1) ($ob | ConvertTo-Json -Compress)
    Step 'status: human output has an outbox line' ((Ag 'A' @('status')).Out -match 'outbox:\s+\d+ queued, \d+ relayed, \d+ expired')
    Start-Bg 'B' $agentnetd @('run') $envB
    Step 'status: B running again for mail' (Wait-Status 'B')
    $drained = Wait-Until { $o = ((Ag 'A' @('status', '--json')).Out | ConvertFrom-Json).outbox; ($o.queued + $o.relayed) -eq 0 } 60
    Step 'offline mail delivered: ack received, outbox drained' $drained

    # 8. relay restart
    Stop-Bg 'relay'
    Start-Bg 'relay' $relayExe @('--listen', "127.0.0.1:$port", '--queue-db', $queueDb, '--verbose')
    Step 'ping A -> B after relay restart' (Wait-Ping 'A' $refB 60) $script:lastPing.Out

    # 9. remove
    $r = Ag 'B' @('peers', 'remove', $refA)
    Step 'peers remove' ($r.Code -eq 0) $r.Err
    Step 'peers empty after remove' ((((Ag 'B' @('peers', '--json')).Out | ConvertFrom-Json).peers.Count) -eq 0)
    $r = Ag 'B' @('ping', "@$refA", '--json')
    Step 'ping to removed peer fails (unknown_peer)' (($r.Code -eq 1) -and ($r.Out -match 'unknown_peer')) $r.Out
}
catch {
    Write-Host "ABORT  $($_.Exception.Message)" -ForegroundColor Red
    $script:fails++
}
finally {
    foreach ($k in @('A', 'B', 'relay')) { Stop-Bg $k }
    if ($script:fails -gt 0) { Write-Host "logs kept in $work" }
    else { Start-Sleep -Milliseconds 500; Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue }
}
if ($script:fails -eq 0) { Write-Host 'ALL PASS' -ForegroundColor Green; exit 0 }
Write-Host "$($script:fails) FAILED" -ForegroundColor Red; exit 1
