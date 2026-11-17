# pgbench's TPC-B-like transactions from 20 clients over 20 s; no transaction
# may fail.
export PGDATA=/tmp/pg
initdb -D "$PGDATA" -A trust >/tmp/initdb.log 2>&1 || { tail -3 /tmp/initdb.log; exit 1; }
pg_ctl -D "$PGDATA" -o "-k /tmp -c listen_addresses=''" -w start >/tmp/pg.log 2>&1 || { tail -3 /tmp/pg.log; exit 1; }
pgbench -h /tmp -i -s 1 -q postgres >/tmp/pbi.log 2>&1 || { tail -3 /tmp/pbi.log; exit 1; }
pgbench -h /tmp -n -c 20 -j 4 -T 20 postgres >/tmp/pb.log 2>&1
rc=$?
pg_ctl -D "$PGDATA" stop -m fast >/dev/null 2>&1
failed=$(sed -n 's/^number of failed transactions: \([0-9]*\).*/\1/p' /tmp/pb.log)
[ "$rc" = 0 ] && [ "$failed" = 0 ] || { tail -5 /tmp/pb.log; exit 1; }
echo "LOAD postgres tps=$(sed -n 's/^tps = \([0-9]*\).*/\1/p' /tmp/pb.log) lat=$(sed -n 's/^latency average = \([0-9.]*\) ms/\1/p' /tmp/pb.log)ms"
