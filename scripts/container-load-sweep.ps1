<#
.SYNOPSIS
  Windows counterpart of container-load-sweep.sh: the container images under
  load. Each test starts a server and drives it with a benchmark client in the
  same container, fails if any request failed or was lost, and reports
  throughput and latency. The tests are the POSIX sh scripts in load\, run
  base64-encoded as `sh -c "echo <b64> | base64 -d | sh"`, as the sh sweep
  runs them.

.PARAMETER Laboon
  The laboon.exe to use (default: .\laboon.exe).

.PARAMETER Timeout
  Seconds each test may run (default: 900).

.PARAMETER Only
  Space-separated test names to run (default: all).

.NOTES
  Exit code is the number of tests that failed. A failure's whole output is
  kept in load-fail-NAME.txt.
#>
param([string]$Laboon = ".\laboon.exe", [int]$Timeout = 900, [string]$Only = "")
# Absolute: each test runs in a background job, whose working directory is not
# this script's, so a relative path would not resolve there.
$Laboon = (Resolve-Path $Laboon).Path

$ErrorActionPreference = "Continue"
$pass = 0; $fail = 0; $fails = @()

# Load NAME USER IMAGE [net]: run load\NAME.sh in IMAGE as USER (empty: the
# image's) and expect its "LOAD NAME ..." result line. net: the test needs a
# network stack (loopback TCP), which laboon gives only with -net.
function Load([string]$name, [string]$user, [string]$image, [string]$net = "") {
    if ($Only -and -not (" $Only " -like "* $name *")) { return }
    $text = [IO.File]::ReadAllText((Join-Path $PSScriptRoot "load\$name.sh")) -replace "`r", ""
    $b64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($text))
    $flags = ""
    if ($user) { $flags += " -u $user" }
    if ($net) { $flags += " -net" }
    $line = "$Laboon$flags -image $image sh -c `"echo $b64 | base64 -d | sh`""
    $tmp = [IO.Path]::GetTempFileName()
    $sw = [Diagnostics.Stopwatch]::StartNew()
    $job = Start-Job -ScriptBlock { param($l, $t) cmd /c "$l > `"$t`" 2>&1"; $LASTEXITCODE } -ArgumentList $line, $tmp
    if (Wait-Job $job -Timeout $Timeout) {
        $rc = [int](Receive-Job $job)
    } else {
        Stop-Job $job -EA SilentlyContinue
        cmd /c "taskkill /F /IM laboon.exe /T >nul 2>&1" | Out-Null
        $rc = 124
    }
    Remove-Job $job -Force -EA SilentlyContinue
    $secs = [int]$sw.Elapsed.TotalSeconds
    $out = (Get-Content $tmp -Raw -EA SilentlyContinue)
    Remove-Item $tmp -EA SilentlyContinue
    if ($null -eq $out) { $out = "" }
    $res = ($out -split "`n" | Where-Object { $_ -like "LOAD $name *" } | Select-Object -Last 1)
    if ($rc -eq 0 -and $res) {
        Write-Output ("PASS  {0}: {1} ({2}s)" -f $name, $res.Substring("LOAD $name ".Length).Trim(), $secs)
        $script:pass++
    } else {
        $last = ($out -split "`n" | Where-Object { $_ -ne "" } | Select-Object -Last 1)
        Write-Output ("FAIL  {0} ({1}): rc={2} | {3}" -f $name, $image, $rc, $last)
        Set-Content "load-fail-$name.txt" $out
        $script:fail++; $script:fails += $name
    }
}

Write-Output "== container LOAD sweep (windows): $Laboon $(Get-Date)"
Load postgres postgres postgres:16-alpine
Load postgres-connect postgres postgres:16-alpine
Load redis "" redis:7-alpine
Load mariadb mysql mariadb:11
Load node "" node:22-alpine net
Write-Output "== load sweep done: $pass passed, $fail failed $(if ($fails) { '(' + ($fails -join ' ') + ')' })"
exit $fail
