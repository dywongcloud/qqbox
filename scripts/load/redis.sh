# redis-benchmark: 100000 each of SET, GET, INCR, LPUSH and LPOP from 50
# clients. None may be lost: the INCR counter must end at 100000 and the list
# empty.
redis-server --unixsocket /tmp/r.sock --port 0 --daemonize no --save "" --ignore-warnings ARM64-COW-BUG >/tmp/redis.log 2>&1 &
for i in $(seq 1 200); do redis-cli -s /tmp/r.sock ping 2>/dev/null | grep -q PONG && break; done
redis-benchmark -s /tmp/r.sock -c 50 -n 100000 -t set,get,incr,lpush,lpop --csv >/tmp/rb.csv 2>/tmp/rb.err || { tail -3 /tmp/rb.err; exit 1; }
n=$(redis-cli -s /tmp/r.sock get counter:__rand_int__)
l=$(redis-cli -s /tmp/r.sock llen mylist)
redis-cli -s /tmp/r.sock shutdown nosave >/dev/null 2>&1
[ "$n" = 100000 ] || { echo "INCR counter is $n, want 100000"; exit 1; }
[ "$l" = 0 ] || { echo "list has $l entries after as many pops as pushes"; exit 1; }
echo "LOAD redis $(awk -F'"' '$2 != "test" { printf "%s=%.0f/s ", tolower($2), $4 }' /tmp/rb.csv)"
