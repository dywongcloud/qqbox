#!/bin/sh
# container-load-sweep.sh: the container images under load. Each test starts
# a server and drives it with a benchmark client in the same container
# (pgbench, redis-benchmark, mariadb-slap, a node HTTP client), fails if any
# request failed or was lost, and reports throughput and latency. The tests
# are the scripts in load/.
#
#   scripts/container-load-sweep.sh [qqbox-binary | docker]   (default: ./qqbox)
#
# docker runs the same tests under Docker, as a baseline.
# PLATFORM   linux/amd64 or linux/arm64 (default: the host architecture)
# ONLY       space-separated test names to run (default: all)
# Exit status is the number of tests that failed. A failure's whole output is
# kept in load-fail-NAME.txt.
set -u

dir=$(cd "$(dirname "$0")" && pwd)/load
RT=${1:-./qqbox}
if [ "$RT" != docker ] && [ ! -x "$RT" ]; then RT="go run ./cmd/qqbox"; fi
PLAT=""
if [ -n "${PLATFORM:-}" ]; then
	if [ "$RT" = docker ]; then PLAT="--platform $PLATFORM"; else PLAT="-platform $PLATFORM"; fi
fi

TO=""
for t in timeout gtimeout; do
	if command -v "$t" >/dev/null 2>&1; then TO="$t 900"; break; fi
done

pass=0
fail=0
fails=""

# load NAME USER IMAGE: run load/NAME.sh in IMAGE as USER (none: the image's)
# and expect its "LOAD NAME ..." result line.
load() {
	name=$1 user=$2 image=$3
	case " ${ONLY:-$name} " in *" $name "*) ;; *) return ;; esac
	b64=$(base64 <"$dir/$name.sh" | tr -d '\n')
	u=""
	[ -n "$user" ] && u="-u $user"
	start=$(date +%s)
	if [ "$RT" = docker ]; then
		out=$($TO docker run --rm $PLAT $u "$image" sh -c "echo $b64 | base64 -d | sh" 2>&1)
	else
		out=$($TO $RT $PLAT $u -image "$image" sh -c "echo $b64 | base64 -d | sh" 2>&1)
	fi
	rc=$?
	secs=$(($(date +%s) - start))
	line=$(printf '%s\n' "$out" | grep "^LOAD $name " | tail -1)
	if [ "$rc" = 0 ] && [ -n "$line" ]; then
		echo "PASS  $name: ${line#LOAD $name } (${secs}s)"
		pass=$((pass + 1))
	else
		echo "FAIL  $name ($image): rc=$rc | $(printf '%s' "$out" | tail -2 | tr '\n' ' ' | cut -c1-160)"
		printf '%s\n' "$out" >"load-fail-$name.txt"
		fail=$((fail + 1))
		fails="$fails $name"
	fi
}

echo "== container LOAD sweep: $RT ${PLATFORM:-} $(date)"
load postgres postgres postgres:16-alpine
load postgres-connect postgres postgres:16-alpine
load redis "" redis:7-alpine
load mariadb mysql mariadb:11
load node "" node:22-alpine
echo "== load sweep done: $pass passed, $fail failed${fails:+ ($fails)}"
exit $fail
