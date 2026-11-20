#!/usr/bin/env bash
# Part 1 test script for the Lab 1 HTTP server.
#
# Usage:
#   Terminal 1 (project folder):  go build -o http_server && ./http_server 8080
#   Terminal 2 (same folder):     bash test.sh 8080
#
# Needs: bash, curl, and the server running from this same folder,
# because the script puts test files directly into ./files.

PORT=${1:-8080}
URL="http://localhost:$PORT"
ROOT="files"
pass=0
fail=0

ok()   { echo "  PASS  $1"; pass=$((pass + 1)); }
bad()  { echo "  FAIL  $1"; fail=$((fail + 1)); }
expect() { # name, expected, actual
    if [ "$2" = "$3" ]; then ok "$1"; else bad "$1 (expected '$2', got '$3')"; fi
}
status() { curl -s -o /dev/null -w "%{http_code}" "$@"; }
ctype() { curl -s -o /dev/null -w "%{content_type}" "$@"; }
# Send raw bytes over TCP (bash's built-in /dev/tcp) and print the status code
# from the first response line.
raw() {
    local fd line
    exec {fd}<> "/dev/tcp/localhost/$PORT"
    printf "$1" >&$fd
    read -t 15 -r line <&$fd
    exec {fd}>&-
    echo "$line" | awk '{print $2}'
}
now()   { date +%s.%N; }
secs()  { awk -v a="$1" -v b="$2" 'BEGIN { printf "%.1f", b - a }'; }

# --- Preconditions -----------------------------------------------------------
if ! curl -s -o /dev/null "$URL/"; then
    echo "Cannot reach $URL. Start the server first: ./http_server $PORT"
    exit 1
fi
if [ ! -d "$ROOT" ]; then
    echo "No ./$ROOT folder here. Run this script from the folder the server runs in."
    exit 1
fi

# --- Test files --------------------------------------------------------------
echo "hello" > "$ROOT/test.txt"
echo "<h1>Hi</h1>" > "$ROOT/index.html"
echo "body{color:red}" > "$ROOT/style.css"
head -c 50000 /dev/urandom > "$ROOT/pic.jpg"   # random bytes: tests binary safety
cp "$ROOT/pic.jpg" "$ROOT/pic.jpeg"
cp "$ROOT/pic.jpg" "$ROOT/anim.gif"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

echo "1. GET and Content-Type"
expect "GET .txt status"   200          "$(status "$URL/test.txt")"
expect "GET .txt type"     "text/plain" "$(ctype "$URL/test.txt")"
expect "GET .html type"    "text/html"  "$(ctype "$URL/index.html")"
expect "GET .css type"     "text/css"   "$(ctype "$URL/style.css")"
expect "GET .jpg type"     "image/jpeg" "$(ctype "$URL/pic.jpg")"
expect "GET .jpeg type"    "image/jpeg" "$(ctype "$URL/pic.jpeg")"
expect "GET .gif type"     "image/gif"  "$(ctype "$URL/anim.gif")"
expect "GET body correct"  "hello"      "$(curl -s "$URL/test.txt")"
curl -s "$URL/pic.jpg" -o "$TMP/got.jpg"
if cmp -s "$ROOT/pic.jpg" "$TMP/got.jpg"; then ok "binary file arrives byte-for-byte"; else bad "binary file was changed in transit"; fi

echo "2. Error codes"
expect "missing file -> 404"        404 "$(status "$URL/missing.txt")"
expect "unsupported .exe -> 400"    400 "$(status "$URL/program.exe")"
expect "no extension -> 400"        400 "$(status "$URL/noextension")"
expect "PUT -> 501"                 501 "$(status -X PUT "$URL/test.txt")"
expect "DELETE -> 501"              501 "$(status -X DELETE "$URL/test.txt")"
expect "HEAD -> 501"                501 "$(raw 'HEAD /test.txt HTTP/1.0\r\n\r\n')"
expect "unknown method FOO -> 400"  400 "$(status -X FOO "$URL/test.txt")"
expect "garbage request -> 400"     400 "$(raw 'garbage\r\n\r\n')"
expect "missing HTTP version -> 400" 400 "$(raw 'GET /test.txt\r\n\r\n')"
expect "malformed header -> 400"    400 "$(raw 'GET /test.txt HTTP/1.0\r\nbadheader\r\n\r\n')"

echo "3. POST"
expect "POST new file -> 201"       201 "$(status --data-binary 'new content' "$URL/new.txt")"
expect "GET after POST"             "new content" "$(curl -s "$URL/new.txt")"
expect "POST into new subfolder"    201 "$(status --data-binary @"$ROOT/pic.jpg" "$URL/up/copy.jpg")"
curl -s "$URL/up/copy.jpg" -o "$TMP/copy.jpg"
if cmp -s "$ROOT/pic.jpg" "$TMP/copy.jpg"; then ok "binary upload round-trips"; else bad "binary upload was changed"; fi
expect "POST .exe -> 400"           400 "$(status --data-binary 'x' "$URL/bad.exe")"

echo "4. Path traversal"
c=$(status --path-as-is "$URL/../../etc/passwd.txt")
if [ "$c" = 404 ] || [ "$c" = 400 ]; then ok "../ cannot escape ./$ROOT (got $c)"; else bad "../ traversal: expected 404 or 400, got $c"; fi

echo "5. Read timeout (takes a few seconds)"
t0=$(now)
exec {fd}<> "/dev/tcp/localhost/$PORT"   # connect, then send nothing
read -t 30 -r line <&$fd
exec {fd}>&-
c=$(echo "$line" | awk '{print $2}')
t1=$(now)
if [ "$c" = 400 ]; then ok "silent client gets 400 after $(secs "$t0" "$t1")s"; else bad "silent client: expected 400, got '$c' after $(secs "$t0" "$t1")s"; fi

echo "6. 10-connection limit (takes a few seconds)"
fds=""
for i in $(seq 9); do    # 9 idle connections: one slot is still free
    exec {fd}<> "/dev/tcp/localhost/$PORT"
    fds="$fds $fd"
done
sleep 0.5
t0=$(now)
c=$(status "$URL/test.txt")
t1=$(now)
waited=$(secs "$t0" "$t1")
if [ "$c" = 200 ] && awk -v w="$waited" 'BEGIN { exit !(w < 1) }'; then
    ok "10th connection served at once (${waited}s): limit is not below 10"
else
    bad "10th connection: expected an immediate 200, got $c after ${waited}s"
fi
for fd in $fds; do exec {fd}>&-; done
sleep 0.5
fds=""
for i in $(seq 12); do   # 12 idle connections hold all 10 slots
    exec {fd}<> "/dev/tcp/localhost/$PORT"
    fds="$fds $fd"
done
sleep 0.5
t0=$(now)
c=$(status "$URL/test.txt")
t1=$(now)
waited=$(secs "$t0" "$t1")
if awk -v w="$waited" 'BEGIN { exit !(w >= 2) }'; then
    ok "13th request waited ${waited}s for a free slot (status $c)"
else
    bad "13th request answered after ${waited}s; limit not enforced"
fi
for fd in $fds; do exec {fd}>&-; done

echo "7. Atomic POST under concurrent GETs (takes a few seconds)"
SIZE=300000
head -c $SIZE /dev/zero | tr '\0' 'A' > "$TMP/A.txt"
head -c $SIZE /dev/zero | tr '\0' 'B' > "$TMP/B.txt"
pids=""
for i in $(seq 6); do
    curl -s --data-binary @"$TMP/A.txt" "$URL/race.txt" > /dev/null & pids="$pids $!"
    curl -s --data-binary @"$TMP/B.txt" "$URL/race.txt" > /dev/null & pids="$pids $!"
done
mixed=0
complete=0
for i in $(seq 15); do
    curl -s "$URL/race.txt" -o "$TMP/race.got"
    a=$(tr -cd 'A' < "$TMP/race.got" | wc -c)
    b=$(tr -cd 'B' < "$TMP/race.got" | wc -c)
    if { [ "$a" -gt 0 ] && [ "$b" -gt 0 ]; } || { [ "$a" -gt 0 ] && [ "$a" -ne $SIZE ]; } || { [ "$b" -gt 0 ] && [ "$b" -ne $SIZE ]; }; then
        mixed=$((mixed + 1))
    elif [ "$a" -eq $SIZE ] || [ "$b" -eq $SIZE ]; then
        complete=$((complete + 1))
    fi
done
wait $pids
expect "partial or mixed reads" 0 "$mixed"
curl -s "$URL/race.txt" -o "$TMP/race.got"
final=$(wc -c < "$TMP/race.got")
if [ "$complete" -gt 0 ] || [ "$final" -eq $SIZE ]; then
    ok "uploads actually landed ($complete complete reads during the race, final size $final)"
else
    bad "no complete version of race.txt was ever read; the POSTs may be failing"
fi
left=$(ls -A "$ROOT" | grep -c '^\.upload-')
expect "leftover temp files" 0 "$left"

echo
echo "Result: $pass passed, $fail failed"
[ "$fail" -eq 0 ]