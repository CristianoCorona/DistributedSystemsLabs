# Concurrency test for atomic POST (Basic part).
#
# Purpose:  shows that concurrent POSTs of the same file are atomic: every GET receives one
#           complete version of the file, never a mix of two versions or a truncated file.
# Requires: a running server on port 8080 (the port is fixed in this script), curl, and
#           about 10 MB of free space in the current directory and in data/.
# Usage:    from lab01, with the server running:  (cd scripts && bash atomic.sh)
#           It writes A.txt, B.txt and got_1.txt ... got_20.txt to the current directory
#           and overwrites data/big.txt on the server.
# Expected output: one line per distinct result, with its count. A correct server prints
#           only single-letter lines with the full size, and the counts add up to 20, e.g.
#                13 a 5000000
#                 7 b 5000000
#           Both letters (e.g. "ab") would mean a mixed file; a size below 5000000 a
#           truncated one.

# Two versions of the same file, 5 MB each: one made only of 'a', one only of 'b'.
head -c 5000000 /dev/zero | tr '\0' 'a' > A.txt
head -c 5000000 /dev/zero | tr '\0' 'b' > B.txt
curl -s --data-binary @A.txt localhost:8080/big.txt > /dev/null   # initial version

# 20 rounds started in parallel (&): in each, two POSTs of different versions race with
# a GET of the same file. wait blocks until all 60 requests have finished.
for i in $(seq 20); do
  curl -s --data-binary @A.txt localhost:8080/big.txt > /dev/null &
  curl -s --data-binary @B.txt localhost:8080/big.txt > /dev/null &
  curl -s localhost:8080/big.txt -o got_$i.txt &
done
wait
# For each downloaded file: tr -s squeezes runs of the same letter, so a pure file becomes
# "a" or "b" and a mixed one shows both letters (e.g. "ab"); wc -c gives its size. uniq -c
# counts equal lines.
for f in got_*.txt; do echo "$(tr -s 'ab' < $f) $(wc -c < $f)"; done | sort | uniq -c
