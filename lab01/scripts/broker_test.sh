#!/usr/bin/env bash
# End-to-end test of the pub-sub broker (Bonus part) against a running server.
#
# Purpose:  runs 46 checks in 8 groups over real HTTP connections: subscribe response, bad
#           requests (400/404/501), delivery and order, long polling and the 204 timeout,
#           subscription rules, bounded queues, concurrent publishers, and waiting polls
#           that do not use up the 10 connection slots.
# Requires: a running server, bash, curl, awk, and a broker queue size of 64 (QUEUE below
#           must match broker.New in main.go).
# Usage:
#   Terminal 1 (lab01):  go build -o http_server ./cmd/http_server && ./http_server 8080
#   Terminal 2 (lab01):  bash scripts/broker_test.sh 8080
# Expected output: one "PASS <check>" line per check, grouped under headings 1 to 8, and
#   finally "Result: 46 passed, 0 failed". The exit status is 0 only if no check failed.
#
# Takes about 20 seconds: one check waits for the 15-second long-poll timeout.
# Every run uses its own topic names, so it can be repeated without restarting the server.
# It stores one file on the server, data/run<PID>.txt (group 8).

PORT=${1:-8080}
URL="http://localhost:$PORT"
QUEUE=64               # queue size passed to broker.New in main.go
RUN="run$$"            # unique prefix: sequence numbers start at 1 on fresh topics
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
pass=0
fail=0

# Helpers. ok/bad count and print a result; expect compares an expected and an actual value.
ok()  { echo "  PASS  $1"; pass=$((pass + 1)); }
bad() { echo "  FAIL  $1"; fail=$((fail + 1)); }
expect() { # name, expected, actual
    if [ "$2" = "$3" ]; then ok "$1"; else bad "$1 (expected '$2', got '$3')"; fi
}
# status prints only the status code; body() prints only the body.
status() { curl -s -o /dev/null -w "%{http_code}" "$@"; }
body()   { curl -s "$@"; }
now()    { date +%s.%N; }
secs()   { awk -v a="$1" -v b="$2" 'BEGIN { printf "%.2f", b - a }'; }
atleast() { awk -v x="$1" -v y="$2" 'BEGIN { exit !(x >= y) }'; }   # true if $1 >= $2
# subscribe TOPIC prints the new id.
subscribe() { body -X POST "$URL/subscribe?topic=$1" | grep -o '[0-9]\+'; }
publish()   { curl -s -o /dev/null --data-binary "$2" "$URL/publish?topic=$1"; }
# seqs prints the sequence numbers of a JSON array of messages, space separated.
seqs() { grep -o '"seq":[0-9]*' | cut -d: -f2 | tr '\n' ' ' | sed 's/ $//'; }

# Stop early if nothing answers on the port (curl fails only if it cannot connect).
if ! curl -s -o /dev/null "$URL/"; then
    echo "Cannot reach $URL. Start the server first: ./http_server $PORT"
    exit 1
fi

# Group 1: the subscribe response has the format the lab asks for (201, JSON, {"id":N}),
# and ids are unique across topics (App.subscribe, Broker.Subscribe).
echo "1. Subscribe"
resp=$(curl -s -i -X POST "$URL/subscribe?topic=$RUN-a")
expect "status 201 Created" "HTTP/1.0 201 Created" "$(echo "$resp" | head -1 | tr -d '\r')"
expect "Content-Type application/json" "application/json" "$(echo "$resp" | grep -i '^Content-Type' | cut -d' ' -f2 | tr -d '\r')"
if echo "$resp" | tail -1 | grep -q '^{"id":[0-9]\+}$'; then ok "body is {\"id\":N}"; else bad "body is not {\"id\":N}: $(echo "$resp" | tail -1)"; fi
a=$(subscribe "$RUN-a"); b=$(subscribe "$RUN-b"); c=$(subscribe "$RUN-a")
if [ "$a" != "$b" ] && [ "$a" != "$c" ] && [ "$b" != "$c" ]; then ok "ids unique across topics ($a, $b, $c)"; else bad "ids not unique: $a, $b, $c"; fi

# Group 2: every client mistake gets the right code: missing/invalid parameters -> 400
# (topicParam, idParam), unknown or other-topic id -> 404, a known but wrong method -> 501
# (requireMethod), an unknown method -> 400 (knownMethods).
echo "2. Bad requests"
id=$(subscribe "$RUN-v")
expect "subscribe without topic -> 400"      400 "$(status -X POST "$URL/subscribe")"
expect "subscribe with empty topic -> 400"   400 "$(status -X POST "$URL/subscribe?topic=")"
expect "publish without topic -> 400"        400 "$(status --data-binary x "$URL/publish")"
expect "events without topic -> 400"         400 "$(status "$URL/events?id=$id")"
expect "events without id -> 400"            400 "$(status "$URL/events?topic=$RUN-v")"
expect "events with id=abc -> 400"           400 "$(status "$URL/events?topic=$RUN-v&id=abc")"
expect "events with id=0 -> 400"             400 "$(status "$URL/events?topic=$RUN-v&id=0")"
expect "unsubscribe with id=abc -> 400"      400 "$(status -X POST "$URL/unsubscribe?topic=$RUN-v&id=abc")"
expect "events for unknown id -> 404"        404 "$(status "$URL/events?topic=$RUN-v&id=999999")"
expect "events with id of other topic -> 404" 404 "$(status "$URL/events?topic=$RUN-other&id=$id")"
expect "unsubscribe unknown id -> 404"       404 "$(status -X POST "$URL/unsubscribe?topic=$RUN-v&id=999999")"
expect "GET /subscribe -> 501"               501 "$(status "$URL/subscribe?topic=$RUN-v")"
expect "GET /publish -> 501"                 501 "$(status "$URL/publish?topic=$RUN-v")"
expect "GET /unsubscribe -> 501"             501 "$(status "$URL/unsubscribe?topic=$RUN-v&id=$id")"
expect "POST /events -> 501"                 501 "$(status -X POST "$URL/events?topic=$RUN-v&id=$id")"
expect "DELETE /events -> 501"               501 "$(status -X DELETE "$URL/events?topic=$RUN-v&id=$id")"
expect "unknown method FOO /events -> 400"   400 "$(status -X FOO "$URL/events?topic=$RUN-v&id=$id")"

# Group 3: publish answers 200 (also with no subscribers); queued messages come back in
# one JSON array, in order; quotes and newlines are JSON-escaped.
echo "3. Publish and receive"
id=$(subscribe "$RUN-p")
expect "publish -> 200" 200 "$(status --data-binary hello "$URL/publish?topic=$RUN-p")"
expect "publish to topic without subscribers -> 200" 200 "$(status --data-binary x "$URL/publish?topic=$RUN-nobody")"
expect "queued message delivered" '[{"seq":1,"body":"hello"}]' "$(body "$URL/events?topic=$RUN-p&id=$id")"
publish "$RUN-p" one; publish "$RUN-p" two; publish "$RUN-p" three
expect "all queued messages in one response, in order" "2 3 4" "$(body "$URL/events?topic=$RUN-p&id=$id" | seqs)"
publish "$RUN-p" 'say "hi"
bye'
if body "$URL/events?topic=$RUN-p&id=$id" | grep -qF '"body":"say \"hi\"\nbye"'; then ok "quotes and newlines are JSON-escaped"; else bad "special characters not escaped correctly"; fi

# Group 4: a poll waits and is answered right after a publish (between 0.9 and 3 s);
# a poll with nothing published gets 204 without Content-Length after the timeout.
echo "4. Long polling (takes ~17 s)"
id=$(subscribe "$RUN-lp")
t0=$(now)
body "$URL/events?topic=$RUN-lp&id=$id" > "$TMP/lp" &
pid=$!
sleep 1
publish "$RUN-lp" live
wait $pid
t1=$(now)
expect "waiting poll receives the message" '[{"seq":1,"body":"live"}]' "$(cat "$TMP/lp")"
el=$(secs "$t0" "$t1")
if atleast "$el" 0.9 && ! atleast "$el" 3; then ok "waited, then answered right after the publish (${el}s)"; else bad "poll took ${el}s; expected about 1s (the moment of the publish)"; fi
t0=$(now)
resp=$(curl -s -i "$URL/events?topic=$RUN-lp&id=$id")
t1=$(now)
expect "no message -> 204 No Content" "HTTP/1.0 204 No Content" "$(echo "$resp" | head -1 | tr -d '\r')"
expect "204 has no Content-Length" "" "$(echo "$resp" | grep -i '^Content-Length' | cut -d' ' -f2 | tr -d '\r')"
if atleast "$(secs "$t0" "$t1")" 10; then ok "204 came after the timeout ($(secs "$t0" "$t1")s)"; else bad "204 came too early ($(secs "$t0" "$t1")s)"; fi

# Group 5: only messages of the own topic and published after subscribing; after
# unsubscribe the id is gone (404); unsubscribe wakes a waiting poll with 404 at once.
echo "5. Subscription rules"
a=$(subscribe "$RUN-x"); b=$(subscribe "$RUN-y")
publish "$RUN-x" for-x
publish "$RUN-y" for-y
expect "a subscriber only gets its own topic" '[{"seq":1,"body":"for-y"}]' "$(body "$URL/events?topic=$RUN-y&id=$b")"
publish "$RUN-old" before
id=$(subscribe "$RUN-old")
publish "$RUN-old" after
expect "only messages published after subscribing" '[{"seq":2,"body":"after"}]' "$(body "$URL/events?topic=$RUN-old&id=$id")"
id=$(subscribe "$RUN-u")
expect "unsubscribe -> 200" 200 "$(status -X POST "$URL/unsubscribe?topic=$RUN-u&id=$id")"
expect "publish after unsubscribe still works" 200 "$(status --data-binary x "$URL/publish?topic=$RUN-u")"
expect "events after unsubscribe -> 404" 404 "$(status "$URL/events?topic=$RUN-u&id=$id")"
expect "second unsubscribe -> 404" 404 "$(status -X POST "$URL/unsubscribe?topic=$RUN-u&id=$id")"
id=$(subscribe "$RUN-w")
t0=$(now)
status "$URL/events?topic=$RUN-w&id=$id" > "$TMP/w" &
pid=$!
sleep 0.5
curl -s -o /dev/null -X POST "$URL/unsubscribe?topic=$RUN-w&id=$id"
wait $pid
t1=$(now)
expect "unsubscribe wakes a waiting poll with 404" 404 "$(cat "$TMP/w")"
el=$(secs "$t0" "$t1")
if atleast "$el" 3; then bad "waiting poll was not woken (${el}s)"; else ok "woken at once (${el}s)"; fi

# Group 6: 100 publishes to a subscriber that does not poll: publish never blocks, the
# queue keeps the first 64 messages, and the gap in sequence numbers shows the dropped ones.
echo "6. Bounded queue, publish never blocks"
id=$(subscribe "$RUN-q")
N=$((QUEUE + 36))
t0=$(now)
for i in $(seq $N); do publish "$RUN-q" "m$i"; done
t1=$(now)
ok "$N publishes to a queue of $QUEUE finished in $(secs "$t0" "$t1")s"
got=$(body "$URL/events?topic=$RUN-q&id=$id" | seqs)
expect "subscriber kept the first $QUEUE messages" "$(seq -s ' ' 1 $QUEUE)" "$got"
publish "$RUN-q" next
expect "after draining, new messages arrive (gap shows the skipped ones)" "$((N + 1))" "$(body "$URL/events?topic=$RUN-q&id=$id" | seqs)"

# Group 7: 5 concurrent publishers, 10 messages each: both subscribers get 1..50 in the
# same order (one critical section in Broker.Publish).
echo "7. Concurrent publishers keep the order"
s1=$(subscribe "$RUN-c"); s2=$(subscribe "$RUN-c")
pids=""
for p in $(seq 5); do
    (for i in $(seq 10); do publish "$RUN-c" "p$p-$i"; done) &
    pids="$pids $!"
done
wait $pids
expect "subscriber $s1 got 1..50 in order" "$(seq -s ' ' 1 50)" "$(body "$URL/events?topic=$RUN-c&id=$s1" | seqs)"
expect "subscriber $s2 got 1..50 in order" "$(seq -s ' ' 1 50)" "$(body "$URL/events?topic=$RUN-c&id=$s2" | seqs)"

# Group 8: 12 waiting polls (more than the 10 slots) must not delay a file GET, because
# App.events releases its slot before waiting; then one publish wakes all 12.
echo "8. Waiting polls do not use up the 10 connection slots"
ids=""
for i in $(seq 12); do ids="$ids $(subscribe "$RUN-s")"; done
pids=""
for id in $ids; do
    status "$URL/events?topic=$RUN-s&id=$id" > "$TMP/s$id" &
    pids="$pids $!"
done
sleep 1
curl -s -o /dev/null --data-binary hi "$URL/$RUN.txt"
t0=$(now)
c=$(status "$URL/$RUN.txt")
t1=$(now)
if [ "$c" = 200 ] && ! atleast "$(secs "$t0" "$t1")" 1; then
    ok "file GET answered in $(secs "$t0" "$t1")s with 12 polls waiting"
else
    bad "file GET with 12 polls waiting: status $c after $(secs "$t0" "$t1")s"
fi
publish "$RUN-s" wake
wait $pids
woken=$(cat "$TMP"/s* | grep -o 200 | wc -l)
expect "one publish woke all 12 polls" 12 "$woken"

# Summary; the exit status is 0 only if every check passed.
echo
echo "Result: $pass passed, $fail failed"
[ "$fail" -eq 0 ]