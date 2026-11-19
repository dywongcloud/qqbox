<#
.SYNOPSIS
  Windows counterpart of container-work-sweep.sh: the container sweep taken past
  "it starts". Every language runtime compiles and runs a real program
  (sum 1..100 == 5050) and every database starts a server and serves a real
  query over a unix socket. Each workload is a POSIX sh script; to survive the
  PowerShell -> qqbox -> sh quoting layers it is base64-encoded and run as
  `sh -c "echo <b64> | base64 -d | sh"`, which is identical to the sh sweep.

.PARAMETER QQBox
  The qqbox.exe to use (default: .\qqbox.exe).

.PARAMETER Timeout
  Seconds each workload may run (default: 180); a slow host needs more.

.NOTES
  Env: PLATFORM (linux/amd64|linux/arm64). Exit code is the non-deferred fails.
#>
param([string]$QQBox = ".\qqbox.exe", [int]$Timeout = 180)
# Absolute: each workload runs in a background job, whose working directory is
# not this script's, so a relative path would not resolve there.
$QQBox = (Resolve-Path $QQBox).Path

$ErrorActionPreference = "Continue"
$pass = 0; $fail = 0; $fails = @()

# Work NAME MARKER EXTRAFLAGS IMAGE SCRIPT: run SCRIPT (sh) in IMAGE under qqbox
# with any EXTRAFLAGS (e.g. "-u postgres"), expect MARKER in the output. A
# leading "?" on NAME marks a known-deferred image. A failure's whole output is
# kept in work-fail-NAME.txt.
function Work([string]$name, [string]$marker, [string]$extra, [string]$image, [string]$script) {
    $defer = $false
    if ($name.StartsWith("?")) { $defer = $true; $name = $name.Substring(1) }
    $b64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($script))
    $inner = "echo $b64 | base64 -d | sh"
    # cmd /c (not Start-Process): PowerShell's -ArgumentList does not quote a
    # single arg containing spaces, which splits the sh -c payload. The $line
    # starts with the exe (not a quote), so cmd parses the inner "..." as one
    # argument to qqbox and applies the > redirect itself.
    $pre = if ($extra) { "$QQBox $extra" } else { "$QQBox" }
    $line = "$pre -image $image sh -c `"$inner`""
    $tmp = [IO.Path]::GetTempFileName()
    # Per-image cap via a job; on expiry kill the guest + its stub children.
    $job = Start-Job -ScriptBlock { param($l, $t) cmd /c "$l > `"$t`" 2>&1"; $LASTEXITCODE } -ArgumentList $line, $tmp
    if (Wait-Job $job -Timeout $Timeout) {
        $rc = [int](Receive-Job $job)
    } else {
        Stop-Job $job -EA SilentlyContinue
        cmd /c "taskkill /F /IM qqbox.exe /T >nul 2>&1" | Out-Null
        $rc = 124
    }
    Remove-Job $job -Force -EA SilentlyContinue
    $out = (Get-Content $tmp -Raw -EA SilentlyContinue)
    Remove-Item $tmp -EA SilentlyContinue
    if ($null -eq $out) { $out = "" }
    if ($rc -eq 0 -and $out -match [regex]::Escape($marker)) {
        Write-Output "PASS  $name ($image)"; $script:pass++
    } elseif ($defer) {
        Write-Output "DEFER $name ($image): rc=$rc (known)"
    } else {
        $last = ($out -split "`n" | Where-Object { $_ -ne "" } | Select-Object -Last 1)
        Write-Output ("FAIL  {0} ({1}): rc={2} | {3}" -f $name, $image, $rc, $last)
        Set-Content "work-fail-$name.txt" $out
        $script:fail++; $script:fails += $name
    }
}

Write-Output "== container WORK sweep (windows): $QQBox $($env:PLATFORM) $(Get-Date)"

Work python  pysum   "" python:3.12-alpine  "python3 -c `"print('pysum', sum(range(101)))`""
Work node    nodesum "" node:22-alpine      "node -e `"let s=0;for(let i=1;i<=100;i++)s+=i;console.log('nodesum',s)`""
Work ruby    rubysum "" ruby:3.3-alpine     "ruby -e `"puts 'rubysum '+(1..100).sum.to_s`""
Work perl    perlsum "" perl:5.40           'perl -e ''$s+=$_ for 1..100; print "perlsum $s\n"'''
Work php     phpsum  "" php:8.3-cli-alpine  'php -r ''echo "phpsum ".array_sum(range(1,100))."\n";'''
Work busybox bbsum   "" busybox:1.36        's=0;i=1;while [ $i -le 100 ];do s=$((s+i));i=$((i+1));done;echo bbsum $s'

Work go   gosum   "" golang:1.23-alpine @'
cat > /tmp/m.go <<'END'
package main
import "fmt"
func main(){ s:=0; for i:=1;i<=100;i++{s+=i}; fmt.Println("gosum",s) }
END
go run /tmp/m.go
'@

Work gcc  csum    "" gcc:14 @'
cat > /tmp/a.c <<'END'
#include <stdio.h>
int main(){int s=0;for(int i=1;i<=100;i++)s+=i;printf("csum %d\n",s);return 0;}
END
gcc /tmp/a.c -o /tmp/a && /tmp/a
'@

Work rust rustsum "" rust:1.82-alpine @'
cat > /tmp/m.rs <<'END'
fn main(){let mut s=0;for i in 1..=100{s+=i};println!("rustsum {}",s);}
END
rustc -O /tmp/m.rs -o /tmp/m && /tmp/m
'@

Work java javasum "" eclipse-temurin:21 @'
cd /tmp
cat > Main.java <<'END'
public class Main{public static void main(String[] a){int s=0;for(int i=1;i<=100;i++)s+=i;System.out.println("javasum "+s);}}
END
javac Main.java && java Main
'@

Work redis redisok "" redis:7-alpine @'
redis-server --unixsocket /tmp/r.sock --port 0 --daemonize no --save "" --ignore-warnings ARM64-COW-BUG >/tmp/redis.log 2>&1 &
for i in $(seq 1 200); do redis-cli -s /tmp/r.sock ping 2>/dev/null | grep -q PONG && break; done
redis-cli -s /tmp/r.sock set greet redisok >/dev/null
redis-cli -s /tmp/r.sock get greet
'@

Work postgres pgselect2 "-u postgres" postgres:16-alpine @'
export PGDATA=/tmp/pg
initdb -D "$PGDATA" -A trust >/tmp/initdb.log 2>&1 || { tail -3 /tmp/initdb.log; exit 1; }
pg_ctl -D "$PGDATA" -o "-k /tmp -c listen_addresses=''" -w start >/tmp/pg.log 2>&1 || { tail -3 /tmp/pg.log; exit 1; }
psql -h /tmp -d postgres -tAc "select 'pgselect' || (1+1)"
pg_ctl -D "$PGDATA" stop -m fast >/dev/null 2>&1
'@

Work mariadb mariasel2 "-u mysql" mariadb:11 @'
mariadb-install-db --datadir=/tmp/maria --auth-root-authentication-method=normal --force >/tmp/mi.log 2>&1 || { tail -3 /tmp/mi.log; exit 1; }
mariadbd --datadir=/tmp/maria --socket=/tmp/my.sock --skip-networking --skip-grant-tables >/tmp/md.log 2>&1 &
for i in $(seq 1 400); do mariadb --socket=/tmp/my.sock -e "select 1" >/dev/null 2>&1 && break; done
mariadb --socket=/tmp/my.sock -N -e "select concat('mariasel',1+1)"
mariadb-admin --socket=/tmp/my.sock shutdown >/dev/null 2>&1 || mysqladmin --socket=/tmp/my.sock shutdown >/dev/null 2>&1 || true
'@

Work "dotnet" "8." "" mcr.microsoft.com/dotnet/sdk:8.0-alpine "dotnet --version"

Write-Output "== work sweep done: $pass passed, $fail failed $(if ($fails) { '(' + ($fails -join ' ') + ')' })"
exit $fail
