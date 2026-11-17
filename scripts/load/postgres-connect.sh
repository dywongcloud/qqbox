# pgbench with a new connection for every transaction, from 8 clients over
# 15 s: postgres starts a backend process per connection, so this is fork
# under load. No transaction may fail.
export PGDATA=/tmp/pg
initdb -D "$PGDATA" -A trust >/tmp/initdb.log 2>&1 || { tail -3 /tmp/initdb.log; exit 1; }
pg_ctl -D "$PGDATA" -o "-k /tmp -c listen_addresses=''" -w start >/tmp/pg.log 2>&1 || { tail -3 /tmp/pg.log; exit 1; }
pgbench -h /tmp -i -s 1 -q postgres >/tmp/pbi.log 2>&1 || { tail -3 /tmp/pbi.log; exit 1; }
pgbench -h /tmp -n -C -c 8 -j 4 -T 15 postgres >/tmp/pb.log 2>&1
rc=$?
pg_ctl -D "$PGDATA" stop -m fast >/dev/null 2>&1
failed=$(sed -n 's/^number of failed transactions: \([0-9]*\).*/\1/p' /tmp/pb.log)
[ "$rc" = 0 ] && [ "$failed" = 0 ] || { tail -5 /tmp/pb.log; exit 1; }
echo "LOAD postgres-connect tps=$(sed -n 's/^tps = \([0-9]*\).*/\1/p' /tmp/pb.log) connect=$(sed -n 's/^average connection time = \([0-9.]*\) ms/\1/p' /tmp/pb.log)ms"
