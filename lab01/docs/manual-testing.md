# Manual testing with curl and nc

The examples below assume the server is running on port 8080 and are run from `lab01` in a
second terminal. The [last block](#clean-up) removes the files they create.

- [curl](#curl)
- [nc](#nc)
- [Timeouts](#timeouts)
- [Connection limit](#connection-limit)
- [Pub-sub broker](#pub-sub-broker)
- [Clean up](#clean-up)

Together, these commands produce every status code the server can return in practice:
`200`, `201`, `204`, `400`, `404`, `408` and `501`. (`500` is only possible if a JSON
response cannot be encoded, which does not happen with the current message types.)

## curl

[curl](https://curl.se/) sends one well-formed request and prints the response.

| Option | Effect |
|---|---|
| `-i` | print the response status line and headers before the body |
| `-v` | also print the request (`>` lines) and the response headers (`<` lines) |
| `-X METHOD` | use the given method |
| `--data-binary 'text'` | send `text` as the body; makes the request a `POST` |
| `--data-binary @file` | send the content of `file` byte for byte |
| `-o file` | write the response body to `file` |
| `--path-as-is` | send `/../` segments unchanged (curl normally removes them) |

Use `--data-binary`, not `--data`: `--data` removes newlines from files.

Store a text file, then read it:

```bash
curl -i --data-binary 'hello' localhost:8080/hello.txt
curl -i localhost:8080/hello.txt
```

```
HTTP/1.0 201 Created
Content-Type: text/plain
Content-Length: 25
Connection: close

File stored successfully.
```

```
HTTP/1.0 200 OK
Content-Type: text/plain
Content-Length: 5
Connection: close

hello
```

The response bodies have no trailing newline, so the shell prompt appears right after them.

Store an HTML page and check its `Content-Type`:

```bash
curl -s --data-binary '<h1>Hello</h1>' localhost:8080/page.html
curl -i localhost:8080/page.html
```

```
HTTP/1.0 200 OK
Content-Type: text/html
Content-Length: 14
Connection: close

<h1>Hello</h1>
```

Upload binary data and check that it comes back unchanged. The server checks only the
extension, so random bytes in a `.jpg` file are enough:

```bash
head -c 1000000 /dev/urandom > sample.jpg
curl -s --data-binary @sample.jpg localhost:8080/sample.jpg
curl -s localhost:8080/sample.jpg -o out.jpg && cmp out.jpg sample.jpg && echo identical
```

Error cases. Only the status line is shown here. Every error body is the status text:

```bash
curl -i localhost:8080/missing.html                      # HTTP/1.0 404 Not Found
curl -i localhost:8080/document.pdf                      # HTTP/1.0 400 Bad Request (extension is checked before existence)
curl -i localhost:8080/noextension                       # HTTP/1.0 400 Bad Request
curl -i --data-binary 'x' localhost:8080/document.pdf    # HTTP/1.0 400 Bad Request, nothing is stored
curl -i --data-binary 'x' localhost:8080/nodir/a.txt     # HTTP/1.0 400 Bad Request (directory does not exist)
curl -i -X DELETE localhost:8080/hello.txt               # HTTP/1.0 501 Not Implemented
curl -i -X PUT --data-binary 'x' localhost:8080/hello.txt  # HTTP/1.0 501 Not Implemented
curl -I localhost:8080/hello.txt                         # HTTP/1.0 501 Not Implemented (HEAD)
curl -i -X FOO localhost:8080/hello.txt                  # HTTP/1.0 400 Bad Request (unknown method)
curl -i -X get localhost:8080/hello.txt                  # HTTP/1.0 400 Bad Request (methods are case-sensitive)
```

Path traversal. `..` segments cannot leave `data/`, because the path is cleaned as if it
were absolute before being joined to the root:

```bash
curl -i --path-as-is localhost:8080/../../etc/passwd.txt   # HTTP/1.0 404 Not Found (looks for data/etc/passwd.txt)
curl -i --path-as-is localhost:8080/../hello.txt           # HTTP/1.0 200 OK, body "hello" (data/hello.txt)
```

## nc

curl always sends well-formed requests. `nc` (netcat) sends exactly the bytes you give it,
so it can send malformed or incomplete requests:

```bash
printf 'GARBAGE\r\n\r\n' | nc localhost 8080                                # 400 Bad Request (malformed request line)
printf 'FOO /hello.txt HTTP/1.1\r\n\r\n' | nc localhost 8080                # 400 Bad Request (unknown method)
printf 'GET /hello.txt HTTP/1.1\r\nHost: x\r\nno colon\r\n\r\n' | nc localhost 8080   # 400 Bad Request (malformed header)
printf 'GET /hello.txt HTTP/1.0\r\n\r\n' | nc localhost 8080                # 200 OK, body "hello"
```

The server may answer and close the connection before `nc` has sent everything; `nc` then
prints `write: Broken pipe` after the response. The response is still complete.

## Timeouts

A client has 5 seconds to send the request line and the headers. Once they are parsed, a new
60-second deadline covers the body and the response (see
[Two-phase deadline](design.md#two-phase-deadline)):

```bash
sleep 7 | nc localhost 8080                                         # nothing sent: 408 Request Timeout after 5 s
(printf 'GET /hello.txt HTTP/1.1\r\n'; sleep 7) | nc localhost 8080   # headers never finished: 408 after 5 s
(printf 'POST /slow.txt HTTP/1.1\r\nContent-Length: 10\r\n\r\nabc'; sleep 7; printf 'defghij'; sleep 1) | nc localhost 8080   # slow body: 201 Created after 7 s
(printf 'POST /cut.txt HTTP/1.1\r\nContent-Length: 10\r\n\r\nabc'; sleep 7) | nc localhost 8080   # body cut short: 400 Bad Request after 7 s
```

In the first two commands the response arrives after 5 seconds; `nc` exits when `sleep` ends,
about 7 seconds after it starts. The third body pauses for longer than the header timeout but
finishes well within 60 seconds, so `data/slow.txt` is stored with the content `abcdefghij`.
In the fourth, `nc` closes its side of the connection when `sleep` ends: the server reaches
the end of the stream after 3 of the 10 announced bytes, logs `Error storing file: unexpected
EOF` and answers `400`. Nothing is stored and no temporary file remains. A client that stays
connected without finishing its body gets no response: after 60 seconds the server logs an
`i/o timeout` and closes the connection. If the client connects and closes without sending
anything (`nc localhost 8080 < /dev/null`), the server logs it and closes the connection
without a response.

## Connection limit

To check the limit of 10 connections, open 10 connections that send nothing, then make an
eleventh request:

```bash
for i in $(seq 10); do (sleep 8 | nc localhost 8080 > /dev/null) & done
sleep 0.5
curl -s -o /dev/null -w '%{http_code} after %{time_total}s\n' localhost:8080/missing.html
wait
```

The eleventh connection is established at once (the kernel accepts it into the listen
backlog). The server reads it only when a slot frees up, which happens when the 10 idle
connections reach the 5-second header timeout. Expected output: a line like
`404 after 4.498701s`. If you repeat the test with 9 idle connections, the answer is
immediate. See [Connection limit and release](design.md#connection-limit-and-release).

## Pub-sub broker

The broker paths take their parameters from the query string. Quote the URL, or the shell
treats `&` as a background operator. Start from a freshly started server, so that the first
id is `1`.

Subscribe, then wait for a message that nobody publishes. The poll ends after 15 seconds
with an empty `204`:

```bash
curl -i -X POST 'localhost:8080/subscribe?topic=news'
curl -i 'localhost:8080/events?topic=news&id=1'
```

```
HTTP/1.0 201 Created
Content-Type: application/json
Content-Length: 8
Connection: close

{"id":1}
```

```
HTTP/1.0 204 No Content
Connection: close

```

Wait in one terminal and publish from a second one. The waiting request answers as soon as
the message is published:

```bash
curl 'localhost:8080/events?topic=news&id=1'                      # terminal 1, waits
curl -X POST --data-binary 'hello' 'localhost:8080/publish?topic=news'   # terminal 2: Published.
```

```
[{"seq":1,"body":"hello"}]
```

Messages published while nobody is polling are queued, and the next poll returns all of them
at once, in order:

```bash
curl -s -X POST --data-binary 'one' 'localhost:8080/publish?topic=news'
curl -s -X POST --data-binary 'two' 'localhost:8080/publish?topic=news'
curl 'localhost:8080/events?topic=news&id=1'      # [{"seq":2,"body":"one"},{"seq":3,"body":"two"}]
```

Error cases. Only the status line is shown:

```bash
curl -i -X POST 'localhost:8080/subscribe'                         # HTTP/1.0 400 Bad Request (missing topic)
curl -i -X POST 'localhost:8080/unsubscribe?topic=news&id=x'       # HTTP/1.0 400 Bad Request (invalid id)
curl -i -X POST 'localhost:8080/unsubscribe?topic=news&id=99'      # HTTP/1.0 404 Not Found
curl -i -X POST 'localhost:8080/unsubscribe?topic=sports&id=1'     # HTTP/1.0 404 Not Found (id 1 is on another topic)
curl -i 'localhost:8080/events?topic=news&id=99'                   # HTTP/1.0 404 Not Found
curl -i 'localhost:8080/subscribe?topic=news'                      # HTTP/1.0 501 Not Implemented (GET)
curl -i -X POST 'localhost:8080/events?topic=news&id=1'            # HTTP/1.0 501 Not Implemented
curl -i -X POST --data-binary 'x' 'localhost:8080/publish?topic=nobody'   # HTTP/1.0 200 OK (no subscribers)
```

Finally, unsubscribe. A waiting poll of the same subscriber is answered `404` at once:

```bash
curl -i -X POST 'localhost:8080/unsubscribe?topic=news&id=1'      # HTTP/1.0 200 OK, body "Unsubscribed."
```

## Clean up

```bash
rm -f data/hello.txt data/page.html data/sample.jpg data/slow.txt sample.jpg out.jpg
```
