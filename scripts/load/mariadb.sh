# mariadb-slap: 4000 generated mixed reads and writes from 20 clients, three
# times; it fails on any query error.
mariadb-install-db --datadir=/tmp/maria --auth-root-authentication-method=normal --force >/tmp/mi.log 2>&1 || { tail -3 /tmp/mi.log; exit 1; }
mariadbd --datadir=/tmp/maria --socket=/tmp/my.sock --skip-networking --skip-grant-tables >/tmp/md.log 2>&1 &
for i in $(seq 1 400); do mariadb --socket=/tmp/my.sock -e "select 1" >/dev/null 2>&1 && break; done
mariadb-slap --socket=/tmp/my.sock --concurrency=20 --iterations=3 --number-of-queries=4000 \
	--auto-generate-sql --auto-generate-sql-load-type=mixed --auto-generate-sql-add-autoincrement >/tmp/slap.log 2>&1
rc=$?
avg=$(sed -n 's/.*Average number of seconds to run all queries: \([0-9.]*\).*/\1/p' /tmp/slap.log)
[ "$rc" = 0 ] && [ -n "$avg" ] && qps=$(mariadb --socket=/tmp/my.sock -N -e "select round(4000 / $avg)")
mariadb-admin --socket=/tmp/my.sock shutdown >/dev/null 2>&1
[ -n "${qps:-}" ] || { tail -5 /tmp/slap.log; exit 1; }
echo "LOAD mariadb qps=$qps run=${avg}s"
