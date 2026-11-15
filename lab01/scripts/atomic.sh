head -c 5000000 /dev/zero | tr '\0' 'a' > A.txt
head -c 5000000 /dev/zero | tr '\0' 'b' > B.txt
curl -s --data-binary @A.txt localhost:8080/big.txt > /dev/null   # versione iniziale

for i in $(seq 20); do
  curl -s --data-binary @A.txt localhost:8080/big.txt > /dev/null &
  curl -s --data-binary @B.txt localhost:8080/big.txt > /dev/null &
  curl -s localhost:8080/big.txt -o got_$i.txt &
done
wait
for f in got_*.txt; do echo "$(tr -s 'ab' < $f) $(wc -c < $f)"; done | sort | uniq -c
