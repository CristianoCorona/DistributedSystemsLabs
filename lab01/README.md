# Lab 01: Concurrent HTTP server and pub-sub broker

## Overview

`http_server` is an HTTP file server and pub-sub broker written in Go for TDA596 Distributed
Systems. It is built
directly on TCP sockets from the `net` package. From `net/http` it uses only
`http.ReadRequest` to parse requests and the status code constants and `http.StatusText`.
Responses are written by hand.

- `GET` returns a file from the document root `./data`. The file is streamed from disk, not
  loaded into memory.
- `POST` stores the request body as a file in `./data` and answers `201 Created`. The update
  is atomic, and the data is flushed to disk before it becomes visible.
- Each connection is handled in its own goroutine. At most 10 connections are handled at the
  same time.
- Each connection carries one request and is then closed.
- A client has 5 seconds to send the request line and the headers, then 60 more seconds to
  send the body and read the response. This also covers a long poll, which waits at most
  15 seconds.
- At startup the server creates `./data` if it is missing and removes temporary upload files
  left behind by a previous run.

The same binary, on the same port, is also a pub-sub broker (the Bonus part):

- `POST /subscribe?topic=X` registers a subscriber and answers `201 Created` with `{"id":N}`.
- `POST /unsubscribe?topic=X&id=N` removes it.
- `POST /publish?topic=X` sends the request body to every current subscriber of `X`.
- `GET /events?topic=X&id=N` delivers the subscriber's messages with long polling: it answers
  as soon as there is a message, or with `204 No Content` after 15 seconds.
- Each message gets a sequence number in its topic, and every subscriber receives the
  messages in that order. Each subscriber has a queue of up to 64 undelivered messages, and
  a publish never blocks.
- A waiting `GET /events` does not count towards the 10 connections.

## Requirements coverage

This table covers the Basic part of `docs/assignment.md`. The Bonus part is covered in
[the next table](#bonus-part-pub-sub-broker).

| Requirement | Where | How |
|---|---|---|
| A new goroutine for each client | `Server.Serve` (`internal/server`) | After each `Accept`, `Serve` starts a goroutine that runs `Server.handleConn`. |
| Binary called `http_server`, port as first argument, no hard-coded port | `main` (`cmd/http_server`) | Build with `go build -o http_server ./cmd/http_server`. With anything other than exactly one argument, or with a port that is not an integer from 1 to 65535, the program logs `Usage: ./http_server <port>` and exits with status 1. |
| Compiles without errors or warnings | the whole module | `go build ./...`, `go vet ./...` and `gofmt -l .` produce no output, and `go test -race ./...` passes. |
| No fixed IP address for the server or its clients | `main`, `Server.Serve` | Listens on `:<port>`, which means all interfaces (IPv4 and IPv6). Client addresses are not checked. |
| Networking with `net` only | `Server.Serve` | `net.Listen("tcp", addr)` and `listener.Accept()`. |
| `net/http` only for parsing | `Server.handleConn`, `response.Stream` (`internal/response`) | `http.ReadRequest` parses the request. The status line and headers are written with `fmt.Fprintf`. `http.Response`, `http.Serve` and `http.ListenAndServe` are not used. |
| At most 10 concurrent connections; wait when the limit is reached | `server.New`, `Server.Serve` | The `Server.sem` field is a buffered channel with capacity `Config.MaxConns`, 10 (set in `main`), that works as a counting semaphore. A slot is taken **before** `Accept` and released when the connection goroutine returns. |
| Check that the request is properly formatted | `Server.handleConn` | If `http.ReadRequest` fails (malformed request line or headers), the server answers `400 Bad Request`. |
| Serve `html`, `txt`, `gif`, `jpeg`, `jpg`, `css` with the right `Content-Type` | `mimeTypes`, `Store.resolve` (`internal/filestore`) | `mimeTypes` maps `.html`→`text/html`, `.txt`→`text/plain`, `.gif`→`image/gif`, `.jpeg`/`.jpg`→`image/jpeg` and `.css`→`text/css`. |
| Any other extension → 400 | `Store.resolve`, `App.serveFile`, `App.storeFile` (`internal/app`) | `resolve` returns `ErrBadExtension`, which the handlers map to `400` for both `GET` and `POST`. Files with no extension are also rejected. |
| File does not exist → 404 | `Store.Get`, `App.serveFile` | `os.ErrNotExist` becomes `ErrNotFound`, which the handler maps to `404`. A directory whose name has an allowed extension is also reported as `ErrNotFound`. |
| `GET` and `POST` implemented; other valid methods → 501 | `knownMethods`, `App.Route` (`internal/app`) | The nine methods of RFC 9110 and RFC 5789 are known. `GET` and `POST` are dispatched to handlers. The other seven get `501 Not Implemented`. |
| Invalid request → 400 | `App.Route`, `Server.handleConn` | An unknown method (for example `FOO`) or an unparsable request gets `400`. |
| Any other error condition → 400 | `App.serveFile`, `App.storeFile` | Unexpected filesystem errors are logged and answered with `400`. |
| `POST` stores the file and a later `GET` returns it | `App.storeFile`, `Store.Put` | `Put` writes the body to `data/<path>`. A successful `POST` returns `201 Created`. |
| Concurrent `POST`s are atomic; `GET` sees one complete version | `Store.Put` | Write to a temporary file, `fsync` it, then `os.Rename`. See [Atomic POST](#atomic-post). |
| Well-formed error responses | `response.Error` (`internal/response`) | Every error is a full HTTP/1.0 response with `Content-Type: text/plain`, `Content-Length` and a body that holds the status text (for example `Not Found`). |
| No web frameworks | `go.mod` | No dependencies outside the standard library. |
| Containers (recommended) | `Dockerfile` | Multi-stage build; the runtime image is pinned to `alpine:3.24.4`. See [Run with Docker or Podman](#run-with-docker-or-podman). |

### Bonus part: pub-sub broker

| Requirement | Where | How |
|---|---|---|
| Same binary and port as the file server, same networking rules | `App.Route` (`internal/app`) | The four broker paths are matched in `Route`, before the file handlers. They go through the same `Server`, connection limit and deadlines. |
| `POST /subscribe?topic=X` → `201` with `{"id": N}`, a new id for every subscription | `App.subscribe`, `Broker.Subscribe` (`internal/broker`) | Ids come from a single counter, so they are unique across topics. The body is `{"id":N}` with `Content-Type: application/json`. |
| `POST /unsubscribe?topic=X&id=N` | `App.unsubscribe`, `Broker.Unsubscribe` | Removes the subscriber and its queue, answers `200 OK`. A waiting `GET /events` for it is woken up and answered `404`. |
| `POST /publish?topic=X` to every current subscriber | `App.publish`, `Broker.Publish` | The body is queued for every subscriber of `X`. Answers `200 OK`, also when the topic has no subscribers. |
| `GET /events?topic=X&id=N` with long polling, `204` on timeout | `App.events`, `Broker.Wait` | Answers `200 OK` with all queued messages as a JSON array as soon as there is one, or `204 No Content` after 15 seconds. See [Long polling](#long-polling). |
| Missing topic or invalid id → 400, unknown subscriber → 404, other methods → 501 | `topicParam`, `idParam`, `requireMethod` (`internal/app`) | An id that is not an integer from 1 up is invalid. An id used with the wrong topic counts as unknown. A known method other than the operation's gets `501`, an unknown method `400` as for files. |
| Shared state protected | `Broker` | A single `sync.Mutex` guards all the state. No operation blocks while holding it. |
| A sequence number for each message, subscribers receive them in order | `Broker.Publish` | The number is assigned and the message queued in the same critical section, so concurrent publishers are totally ordered. |
| Only messages published while subscribed | `Broker.Subscribe`, `Broker.Unsubscribe` | A new subscriber starts with an empty queue; an unsubscribed one has no queue left. |
| Bounded queue per subscriber, publish never blocks | `Broker.Publish` | Each queue is a buffered channel of 64 messages (set in `main`). When it is full, that subscriber misses the message. |
| Waiting long polls may be excluded from the limit of 10 | `server.Handler`, `App.events` | The handler gets a `release` function that gives the connection's slot back. `events` calls it once the parameters are valid. |

## Project structure and layering

```
lab01/
├── cmd/
│   └── http_server/
│       └── main.go       # argument and port check, configuration, wiring
├── internal/
│   ├── server/           # TCP listener, connection limit, two-phase deadline, request parsing
│   ├── app/              # method validation and dispatch, GET and POST handlers (handlers.go),
│   │                     # broker handlers and long polling (pubsub.go),
│   │                     # error -> status code mapping
│   ├── response/         # writes HTTP/1.0 responses, streaming the body
│   ├── filestore/        # root setup and temp file cleanup, extension check, path cleaning,
│   │                     # streaming read, atomic and durable write
│   └── broker/           # topics, subscribers, sequence numbers, bounded queues, waiting
│                         # (app, response, filestore and broker each have a *_test.go file)
├── docs/
│   └── assignment.md     # lab text
├── scripts/
│   ├── atomic.sh         # concurrency test for atomic POST
│   └── broker_test.sh    # end-to-end test of the pub-sub broker
├── data/                 # document root, created at startup, ignored by git
├── Dockerfile
├── .dockerignore
├── go.mod
└── README.md
```

The code has three layers. Each layer is a package under `internal/`, so Go itself prevents
code outside `lab01` from importing it, and the package boundaries decide what is visible:
only the names the other layers need are exported. Dependencies go downwards only.

1. **Transport** (package `server`): accepts TCP connections, enforces the connection limit
   and the two-phase deadline set in a `server.Config`, and parses the request with
   `http.ReadRequest`. It does not know about files or topics. It passes each parsed request
   to a `server.Handler`, a plain function type `func(net.Conn, *http.Request, func())`. The
   third argument, `release`, gives the connection's slot back early (see
   [Long polling](#long-polling)). It uses `response.Error` for the errors it answers itself
   (`400` and `408`).
2. **Application** (package `app`, plus package `response`): `App.Route` decides what to do with
   a request. The unexported handlers `serveFile` and `storeFile` (`handlers.go`) and
   `subscribe`, `unsubscribe`, `publish` and `events` (`pubsub.go`) map errors from the
   service layer to status codes. `response.Stream`, `response.Write`, `response.Empty` and
   `response.Error` serialize the answer. `app` does not import `server`: `main` passes the
   method value `App.Route` to the server, and it matches `server.Handler` because the
   signatures are the same.
3. **Service** (packages `filestore` and `broker`): these components do not know about HTTP.
   - `filestore.Open` prepares the root directory. `Store.Get` and `Store.Put` resolve a URL
     path to a file under the root directory; `Get` opens it for streaming and `Put` replaces
     it atomically. They return the errors `ErrNotFound` and `ErrBadExtension`.
   - `broker.New` creates the broker. `Broker.Subscribe`, `Unsubscribe`, `Publish` and `Wait`
     keep the topics, the subscribers and their queues. They return the error `ErrNotFound`.

```
cmd/http_server ──► internal/server ──► internal/response
       │                                       ▲
       ├──────────► internal/app ──────────────┘
       │                  │
       ├──────────► internal/filestore ◄───────┤
       │                                       │
       └──────────► internal/broker ◄──────────┘
```

`main` (in `cmd/http_server`) wires the layers together. The document root is the constant
`docRoot` (`./data`). Before the server starts, `main` calls `filestore.Open(docRoot)`, which
creates the directory with `os.MkdirAll(root, 0o755)` and removes the temporary files of
interrupted uploads. The `tmp-*` naming of those files is an internal detail of `filestore`,
so `main` does not need to know about it. Git ignores `data/`, so its contents are never
committed. `main` also creates the broker with `broker.New(64)`, the size of each
subscriber's queue, and passes both to `app.New`. The broker keeps its state in memory only.

## Build and run locally

Requires Go 1.27.3 or later (see `go.mod`). From the `lab01` folder:

```bash
go vet ./...
go build -o http_server ./cmd/http_server
./http_server 8080
```

The server prints two lines at startup:

```
2026/11/16 00:51:08 Removed 0 leftover temporary file(s) from ./data
2026/11/16 00:51:08 Listening on :8080
```

- The only argument is the TCP port, an integer from 1 to 65535.
- Files are read from and written to `./data`, relative to the **current working directory**.
  Start the server from `lab01`, or it will create and use a different `data` directory.
- If `./data` does not exist, the server creates it with mode `0755`. If it cannot (for
  example, if the current directory is not writable), it logs
  `Cannot create document root: ...` and exits with status 1.
- The server logs one line for each request (`Received request: GET /index.html`) and one
  for each error to standard error.
- The connection limit (10) and the timeouts (5 seconds for the headers, 60 seconds for the
  rest of the request) are fixed in `main`. They cannot be set on the command line. The broker's
  queue size (64) is also fixed in `main`, and the long-poll timeout (15 seconds) in
  `internal/app/pubsub.go`.

To stop the server, press Ctrl+C. The process exits at once and the operating system frees
the port. There is no graceful shutdown: requests in progress are cut off. Ctrl+Z only
suspends the process and keeps the port bound. If you pressed it, resume with `fg`, then
press Ctrl+C.

## Run with Docker or Podman

The `Dockerfile` compiles a static binary from `./cmd/http_server` in a `golang:1.27.3` stage
and copies it into an `alpine:3.24.4` image. Both base images are pinned to exact versions,
so a rebuild later produces the same environment. The working directory in the image is
`/app`, so the document root is `/app/data`. That directory is empty in the image.

```bash
docker build -t http_server .
docker run --rm -p 8080:8080 -v ./data:/app/data:Z http_server
```

- `-p 8080:8080` publishes the container port on the host.
- `-v ./data:/app/data` mounts the local `data` folder as the document root, so files are
  shared with the host and uploads persist after the container stops. Without this option,
  the container starts with an empty document root and loses uploads when it stops.
- `:Z` relabels the folder for SELinux. Fedora needs it. On systems without SELinux it has
  no effect.
- Under rootful Docker, files uploaded through the container are owned by `root` on the
  host. Under rootless Podman, they are owned by your user.
- The default port is `8080` (`CMD ["8080"]`). To use another port, pass it after the image
  name and publish it:

  ```bash
  docker run --rm -p 9090:9090 -v ./data:/app/data:Z http_server 9090
  ```

`podman` accepts the same commands: replace `docker` with `podman`. Ctrl+C stops the
container.

The build context is the `lab01` folder. `lab01/.dockerignore` keeps `http_server`, `data/`,
`scripts/` and `docs/` out of it, so local binaries, served files and test output are not sent
to the builder. Only the compiled binary ends up in the final image.

## Simulating a client with curl and nc

The examples below assume the server is running on port 8080 and are run from `lab01` in a
second terminal. They only create files whose names do not appear elsewhere in this README,
and the last block removes them.

### curl

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

### nc

curl always sends well-formed requests. `nc` (netcat) sends exactly the bytes you give it,
so it can send malformed or incomplete requests:

```bash
printf 'GARBAGE\r\n\r\n' | nc localhost 8080                                # 400 Bad Request (malformed request line)
printf 'FOO /hello.txt HTTP/1.1\r\n\r\n' | nc localhost 8080                # 400 Bad Request (unknown method)
printf 'GET /hello.txt HTTP/1.1\r\nHost: x\r\nno colon\r\n\r\n' | nc localhost 8080   # 400 Bad Request (malformed header)
printf 'GET /hello.txt HTTP/1.0\r\n\r\n' | nc localhost 8080                # 200 OK, body "hello"
```

Timeouts. A client has 5 seconds to send the request line and the headers. Once they are
parsed, a new 60-second deadline covers the body and the response:

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

### Pub-sub broker

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

### Clean up

```bash
rm -f data/hello.txt data/page.html data/sample.jpg data/slow.txt sample.jpg out.jpg
```

## Testing

### Running the tests

The unit tests use only the standard library package `testing`. From the `lab01` folder:

```bash
go test -race ./...
```

```
?   	github.com/CristianoCorona/DistributedSystemsLabs/lab01/cmd/http_server	[no test files]
ok  	github.com/CristianoCorona/DistributedSystemsLabs/lab01/internal/app	1.027s
ok  	github.com/CristianoCorona/DistributedSystemsLabs/lab01/internal/broker	1.179s
ok  	github.com/CristianoCorona/DistributedSystemsLabs/lab01/internal/filestore	1.031s
ok  	github.com/CristianoCorona/DistributedSystemsLabs/lab01/internal/response	1.024s
?   	github.com/CristianoCorona/DistributedSystemsLabs/lab01/internal/server	[no test files]
```

Add `-v` to see each test by name. `-race` enables the race detector; the tests also pass
without it. They never touch `./data` and do not need a running server.

| Package | File | What it covers |
|---|---|---|
| `filestore` | `filestore_test.go` | Each test gets its own root from `t.TempDir()`, which `testing` deletes afterwards. `Put` then `Get` returns the same body, size and `Content-Type`; a second `Put` replaces the whole file; the six extensions map to the right `Content-Type`, case-insensitively; other extensions give `ErrBadExtension` and store nothing; a missing file and a directory give `ErrNotFound`; `..` segments, with or without a leading `/`, stay inside the root; stored files have mode `0644`; no `tmp-*` file remains after a successful or a failed `Put`; `Open` creates a missing root and removes only the `tmp-<digits>` files. |
| `response` | `response_test.go` | `Write`, `Error` and `Stream` write into a `bytes.Buffer`, and the test compares the exact bytes, including `\r\n` and `Content-Length` (also for a binary body). `Empty` writes a `204` with only the status line and `Connection: close`. |
| `app` | `app_test.go` | Requests go to `App.Route` through `net.Pipe()`, an in-memory pair of connected `net.Conn`. An unknown or lowercase method gives `400`, `DELETE` and `PUT` give `501`, a missing file `404`, a bad extension `400` for `GET` and `POST`, a `POST` to a missing directory `400`. A `POST` gives `201 Created`, and a following `GET` returns `200` with the same body and `text/html`. |
| `broker` | `broker_test.go` | The broker is tested without the network, with a 50 ms timeout instead of 15 s. Unique ids across topics; one `Wait` returns every queued message in order; timeout; wake-up on publish and on unsubscribe; only messages of the own topic, published after subscribing; unknown id or wrong topic; a full queue drops messages without blocking; 10 concurrent publishers give every subscriber the sequence numbers 1 to 500 with no gaps. |

`net.Pipe` is synchronous: a `Write` on one end blocks until the other end reads it. The
`app` tests therefore read the response in a separate goroutine while `Route` writes it;
reading only after `Route` returned would deadlock on the first `Write`.

The transport layer (`server`) has no unit tests: its behaviour depends on real sockets and
timing, and the functional checks below cover it. The broker paths have no `app` tests
either: `scripts/broker_test.sh` checks them end to end (see
[Pub-sub end-to-end test](#pub-sub-end-to-end-test)).

### Functional checks

The curl and nc commands above cover each status code the server can return: `200`, `201`,
`204`, `400`, `404`, `408` and `501`. To check the connection limit, open 10 connections that send
nothing, then make an eleventh request:

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
immediate.

### Pub-sub end-to-end test

`scripts/broker_test.sh` runs 46 checks against a running server with curl. It covers the
subscribe response, every `400`, `404` and `501` case, delivery and order of queued messages,
JSON escaping, a long poll woken by a publish and one that times out with `204` (without
`Content-Length`), the subscription rules, unsubscribe waking a waiting poll, the bounded
queue (100 publishes to a queue of 64), 5 concurrent publishers, and 12 waiting polls that do
not block a file request. Each run uses its own topic names, so it can be repeated without
restarting the server. It takes about 20 seconds, because one check waits for the timeout:

```bash
./http_server 8080                  # terminal 1, from lab01
bash scripts/broker_test.sh 8080    # terminal 2, from lab01
```

The last line is `Result: 46 passed, 0 failed`, and the exit status is 0 only if every check
passes. The script stores one file (`<run>.txt`) in `data/`.

### Atomicity

`scripts/atomic.sh` creates two 5 MB files, one all `a` and one all `b`. It uploads the first
to `big.txt`, then starts 20 rounds in parallel. Each round has one `POST` of the `a` version,
one `POST` of the `b` version and one `GET`. Finally, it reduces each downloaded file to its
distinct letters and its size. The script expects the server on port 8080 and writes
`A.txt`, `B.txt` and `got_*.txt` to the current directory (git ignores them in `scripts/`).
Run it from `scripts/` so these files stay there:

```bash
(cd scripts && bash atomic.sh)
```

Example output. The split between `a` and `b` varies from run to run:

```
     13 a 5000000
      7 b 5000000
```

A correct server prints only lines with a single letter (`a` or `b`) and the full size
`5000000`. A mixed file would print `ab` or `ba`. A truncated file would show a smaller
size. The script overwrites `data/big.txt`. To remove the files it creates:

```bash
rm -f scripts/A.txt scripts/B.txt scripts/got_*.txt data/big.txt
```

### Race detector

Build with the race detector, start the server and run the atomicity script against it:

```bash
go build -race -o http_server ./cmd/http_server
./http_server 8080
```

In a second terminal, run `(cd scripts && bash atomic.sh)`. If the race detector finds a data
race, the server prints `WARNING: DATA RACE` to standard error. With the current code it
prints nothing beyond the normal request log. Rebuild without `-race` afterwards
(`go build -o http_server ./cmd/http_server`).

## Design decisions

- **Valid vs implemented methods.** The valid methods are the nine defined by RFC 9110 and
  RFC 5789: `GET`, `HEAD`, `POST`, `PUT`, `PATCH`, `DELETE`, `CONNECT`, `OPTIONS` and `TRACE`.
  They are listed in `knownMethods`. `GET` and `POST` are implemented, and the other seven get
  `501 Not Implemented`. Any other method, including lowercase forms like `get`, gets
  `400 Bad Request`, as the lab asks for invalid requests. `HEAD` also gets `501`.
- **One request per connection.** Every response starts with `HTTP/1.0` and has
  `Connection: close`. `Server.handleConn` closes the connection after the response. There is
  no keep-alive.
- **Connection limit before `Accept`.** In `Server.Serve`, the semaphore slot is acquired
  before `listener.Accept()`, not inside the goroutine. While 10 connections are active, the
  server does not call `Accept`. New clients wait in the kernel backlog and their deadline has
  not started yet. If `Accept` fails, the slot is returned at once.
- **Two-phase deadline.** `server.Config` holds two durations, and `Server.handleConn` sets
  them one after the other with `conn.SetDeadline`, which covers reads and writes.
  - `HeaderTimeout` (5 s) starts right after `Accept` and covers the request line and the
    headers. A well-behaved client sends them at once, so an idle client loses its slot
    quickly. If it expires, the server sets a fresh 1-second write deadline and sends
    `408 Request Timeout`.
  - `RequestTimeout` (60 s) starts when the headers have been parsed and covers reading the
    body and writing the response. It is longer because a large file over a slow link
    legitimately takes time. If it expires, for example while a `POST` body is being received,
    no response can be sent anymore: the error is logged and the connection is closed.
  - A read deadline alone would not be enough: a client that sends a request and never reads
    the response would block the server's write once the socket buffers are full, and hold
    the slot forever. `SetDeadline` bounds the write too.
  - A single deadline for the whole connection would force a choice between a short value,
    which cuts off large transfers, and a long one, which lets idle clients hold slots.
  - A long poll runs under `RequestTimeout` too: it waits at most 15 seconds, then writes its
    answer well within the 60 seconds.
- **Port validation.** The argument must be an integer from 1 to 65535. Port 0 is rejected:
  `net.Listen` would pick a random free port, and clients would not know where to connect.
  The parsed number is used to build the address, so `+80` or `080` both listen on `:80`.
- **Document root setup.** The root path is the single constant `docRoot` in
  `cmd/http_server/main.go`. `main` passes it to `filestore.Open`, which creates it with
  `os.MkdirAll` before the server starts, so a fresh clone works without manual setup. If it
  cannot be created, the server exits.
- **Startup cleanup of temporary files.** If the process stops during an upload, its
  `tmp-*` file stays on disk, because deferred cleanup does not run. Before the server starts
  accepting connections, `filestore.Open` deletes these files from the root and logs how
  many it removed. It only deletes regular files named `tmp-` followed by digits, the form
  `os.CreateTemp` produces. A client file such as `tmp-notes.txt` is kept. Subdirectories are
  not scanned.
- **File permissions.** Every stored file gets mode `0644` (owner read/write, everyone
  read), whether it is new or replaces an existing file. `os.CreateTemp` would otherwise
  leave it at `0600`.
- **`POST` status.** A successful `POST` returns `201 Created` with the body
  `File stored successfully.`: the body is now a resource that a later `GET` returns. The
  same status is used for new and replaced files. To tell the two cases apart (and answer
  `200 OK` for a replacement), the server would have to check whether the file exists before
  the rename, and with concurrent uploads that check could be outdated by the time of the
  rename.
- **Streaming `GET`.** `Store.Get` opens the file and returns the open descriptor, its size
  (from `Stat` on the descriptor) and its `Content-Type`. `response.Stream` writes the headers
  with `Content-Length` equal to that size and copies the file to the connection with
  `io.Copy`, so memory use does not grow with the file size. On Linux, `io.Copy` from a file to
  a TCP connection uses the `sendfile` system call: the data goes from the page cache to the
  socket without passing through user space. This was checked with `strace`.
  `serveFile` closes the file when the copy ends. `response.Write`, used for the short
  messages, is `Stream` with a body held in memory.
- **Directories are not files.** On Linux a directory can be opened like a file. A `GET` for
  a directory whose name has an allowed extension (for example `data/photos.jpg/`) answers
  `404 Not Found`.
- **Path handling.** `Store.resolve` cleans the URL path with `filepath.Clean("/" + path)`
  and joins it to the root of the store (`docRoot`). Cleaning it as an absolute path makes
  `..` segments stop at the root, even for a name passed without the leading `/`.
  Subdirectories of `data/` work if they exist. A `POST` to a missing subdirectory fails with
  `400`. The server never creates subdirectories.
- **Extension matching.** Extensions are matched case-insensitively, so `IMAGE.JPG` is
  accepted as `image/jpeg`. File names stay case-sensitive, as on the Linux filesystem:
  `INDEX.HTML` and `index.html` are different files.
- **Order of checks.** The extension is checked before the filesystem is accessed. A
  request for a missing `.pdf` file returns `400`, not `404`.
- **Other errors.** Unexpected errors from the filesystem, such as permission denied, a
  missing directory or a failed body read, are logged and answered with `400 Bad Request`,
  as the lab text says for "any other error condition".

### Atomic POST

Several clients may `POST` the same file while others `GET` it. A `GET` must always return one
complete version, never a partial or mixed one.

**Why an in-place write fails.** `os.WriteFile` opens the target with `O_TRUNC` and rewrites
it. A `GET` that runs during the write reads an empty or truncated file. Two concurrent
`POST`s write into the same file, each at its own offsets, so the result can mix both
versions.

**Mutex alternatives.** A lock around file access is correct, but every variant has a cost:

| Approach | Concurrent GETs | Effect of a slow upload | Other processes |
|---|---|---|---|
| Global `sync.Mutex` | No, all file access is serialized | Blocks every request | Not coordinated |
| Global `sync.RWMutex` | Yes | Blocks every `GET` | Not coordinated |
| One `sync.RWMutex` per file | Yes | Blocks `GET`s of that file | Not coordinated |

The main problem is a slow upload. A `POST` that holds the write lock while reading the body
from the network keeps readers waiting, possibly until the timeout. Buffering the body in a
temporary file first avoids this, but by then most of the rename approach is in place and the
lock is still needed. Per-file locks need a map of locks guarded by a second mutex, and the
map grows unless it is cleaned up. A mutex also coordinates only this process, not, for
example, a second container that shares the volume.

**Chosen approach: write a temporary file, then rename it.** `Store.Put` never modifies the
visible file. It creates a temporary file in the same directory as the target with
`os.CreateTemp(dir, "tmp-*")`, copies the body into it with `io.Copy`, sets mode `0644`,
flushes it to disk with `Sync` and closes it, then replaces the target with `os.Rename`. It
checks the error from closing the file, because some write errors only appear then. If any
step fails, a deferred `os.Remove` deletes the temporary file and the target is left
unchanged.

**Why rename is atomic.** A file name is a directory entry that points to an inode.
`rename(2)` switches the entry from the old inode to the new one in a single step. A `GET`
that opens the file gets either the old inode or the new one. A `GET` that already opened the
old inode keeps reading it to the end, because the kernel frees an inode only after its last
descriptor is closed. With concurrent `POST`s the last rename wins, and every version that
becomes visible is complete. No lock is needed, and the guarantee also holds for other
processes. `rename(2)` is atomic only within one filesystem; across filesystems it fails with
`EXDEV`. This is why the temporary file is created next to the target and not in `/tmp`, which
matters when `data/` is a mounted container volume.

**Temporary files are never served.** `os.CreateTemp` replaces `*` with a random decimal
number, so a temporary file has a name like `tmp-1850080396`, with no extension. The
extension check rejects any request for such a name with `400` before the filesystem is
accessed, so a `GET` can never return a half-written upload.

**Streaming reads stay atomic.** `Store.Get` does not read the whole file before answering:
it keeps the descriptor open while the data is sent. This is still safe, because `Put` never
writes into a visible file. If a `POST` renames a new version over the name during the copy,
the descriptor keeps pointing to the old inode, which stays intact until it is closed. The
size taken from `Stat` on that descriptor therefore matches the bytes that are sent.

**Atomicity and durability.** The rename makes the new version visible atomically. Atomicity
alone does not survive a crash: written data first goes to the kernel's page cache, so after
an operating system crash or a power loss a renamed file could be empty or incomplete. `Put`
therefore calls `Sync()` on the temporary file before closing and renaming it, and checks its
error. Uploads are slower, because each one waits for the disk. The directory itself is not
synced, so after a crash the rename may be lost: the old version is then still in place,
complete.

The [atomicity test](#atomicity) checks this behaviour.

### Pub-sub broker design

**State and locking.** `broker.Broker` holds all the state: the subscribers by id, the set of
ids of each topic, the last sequence number of each topic and, for each subscriber, its topic
and its queue. A single `sync.Mutex` guards all of it, as the lab suggests. No operation
blocks while holding the mutex: `Wait` holds it only to find the queue, and releases it before
waiting. A finer-grained scheme (one lock per topic) would let publishes on different topics
run in parallel, but every operation here is a few map operations, so the single mutex is not
a bottleneck and is much easier to get right.

**Ids.** Ids come from a single counter that starts at 1 and is never reused, so the same
client gets different ids on different topics, and an old id never points to a new
subscriber. Each subscriber remembers its topic: a request with a valid id and another topic
gets `404`, as for an unknown id.

**Sequence numbers and order.** `Publish` assigns the next sequence number of the topic and
appends the message to every subscriber's queue in the same critical section. Two concurrent
publishes are therefore totally ordered: every subscriber sees increasing sequence numbers,
and two subscribers never see two messages in different orders. Numbers are per topic, so a
gap in what a subscriber receives means that its queue was full and it missed messages.

**Bounded queues.** Each subscriber's queue is a buffered channel of 64 messages (the argument
of `broker.New` in `main`). `Publish` never waits: if a queue is full, that subscriber misses
the message, as the lab allows. `Publish` is the only sender and holds the mutex, so the queue
cannot fill up between the length check and the send. `Unsubscribe` closes the channel, which
wakes up a waiting `Wait`.

**Message format.** `GET /events` answers with a JSON array of every queued message, oldest
first, for example `[{"seq":2,"body":"one"},{"seq":3,"body":"two"}]`. Returning all of them
at once saves a round trip per message when a subscriber has fallen behind. The body is sent
as a JSON string.

### Long polling

The lab allows long polling, streaming or any other approach. Long polling fits the server
best: every exchange is still one request and one response on its own connection, with
`Content-Length` and `Connection: close`, exactly like the file requests. Streaming would
need a response without a known length that stays open, which HTTP/1.0 can only end by
closing the connection.

`App.events` validates the parameters, then:

1. **Releases the connection's slot.** A request that waits is not doing any work, and the lab
   allows it to be excluded from the limit of 10. `server.Handler` receives a `release`
   function that gives the slot back. `Server.Serve` builds it with `sync.Once`, so calling
   it early and again when the goroutine ends returns the slot exactly once. Twelve waiting
   polls do not slow down a file request (checked by
   [the end-to-end test](#pub-sub-end-to-end-test)).
2. **Calls `Broker.Wait`**, which returns as soon as one of these happens:

   | Event | Result |
   |---|---|
   | The queue has messages (already, or after a publish) | `200 OK` with all of them |
   | 15 seconds pass with no message | `204 No Content`, with no `Content-Type` or `Content-Length` (RFC 9110 forbids `Content-Length` in a `204`) |
   | The subscriber is unsubscribed | `404 Not Found` |

## Known limitations

- **No graceful shutdown.** Ctrl+C or `SIGTERM` ends the process at once, and requests in
  progress are cut off. Temporary files left in `data/` are removed at the next startup, but
  only from the top level of `data/`, not from its subdirectories.
- **No size limit on uploads.** The server does not limit the size of a `POST` body, so a
  client can fill the disk.
- **The timeouts are fixed.** A transfer that needs more than 60 seconds after the headers is
  cut off; an upload cut off this way gets no response. Ten clients that connect and send
  nothing block all other clients for up to 5 seconds. The long-poll timeout (15 seconds) is
  a constant too, and must stay below `RequestTimeout`, or the deadline would expire before
  the `204` is written.
- **The rename is not synced.** File contents are flushed to disk, but the directory is not,
  so a crash right after a `POST` may bring back the previous version. See
  [Atomic POST](#atomic-post).
- **Missing `Host` header is accepted.** HTTP/1.1 requires a `Host` header, but
  `http.ReadRequest` does not enforce it, and the server answers such requests normally.
- **A client that disconnects while waiting can lose messages.** The server does not notice
  that the client of a waiting `GET /events` has gone. When a message arrives, `Wait` takes it
  off the queue, and the write to the closed connection fails or goes nowhere. Delivery is at
  most once: a message is never delivered twice, but it can be lost this way, or when the
  subscriber's queue is full (the gap in sequence numbers shows it).
- **Concurrent polls for the same subscriber share its messages.** Two `GET /events` with the
  same id read from the same queue, so each gets only part of the messages.
- **No limit on subscriptions or waiting polls.** A client can subscribe as many times as it
  likes and keep a poll waiting on each id, each holding a goroutine and a socket. The lab
  allows waiting polls outside the limit of 10.
- **Sequence counters are never removed.** The broker keeps the last sequence number of every
  topic that was ever published to, also after its last subscriber has left, so the memory
  grows with the number of distinct topic names.
- **Message bodies are sent as JSON strings.** Bytes that are not valid UTF-8 are replaced
  with `U+FFFD` by `encoding/json`, so binary payloads do not arrive unchanged.
- **No authentication and no persistence.** Anyone who knows or guesses an id can poll or
  unsubscribe it, since ids are consecutive. The broker's state lives in memory and is lost
  when the server stops.
