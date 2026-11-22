# Lab 01: Concurrent HTTP server and pub-sub broker

`http_server` is an HTTP file server and pub-sub broker written in Go for TDA596 Distributed
Systems ([lab text](docs/assignment.md)). It is built directly on TCP sockets from the `net`
package. From `net/http` it uses only `http.ReadRequest` to parse requests, the status code
constants and `http.StatusText`. Responses are written by hand.

## Overview

- `GET` streams a file from the document root `./data`; `POST` stores the request body there
  atomically and durably, and answers `201 Created`.
- Each connection runs in its own goroutine, carries one request and is then closed. At most
  10 connections are handled at the same time.
- A client has 5 seconds to send the request line and headers, then 60 more seconds for the
  body and the response.
- At startup the server creates `./data` if needed and removes temporary upload files left
  by a previous run.

The same binary, on the same port, is also a pub-sub broker (the Bonus part):

- `POST /subscribe?topic=X` answers `201 Created` with `{"id":N}`;
  `POST /unsubscribe?topic=X&id=N` removes the subscriber.
- `POST /publish?topic=X` queues the body for every current subscriber of `X`. Each message
  gets a sequence number in its topic; each subscriber has a queue of up to 64 messages, and
  a publish never blocks.
- `GET /events?topic=X&id=N` long-polls: it answers as soon as there is a message, or with
  `204 No Content` after 15 seconds. A waiting poll does not count towards the 10 connections.

## Build and run

Requires Go 1.27.3 or later. From the `lab01` folder:

```bash
go build -o http_server ./cmd/http_server
./http_server 8080
```

The only argument is the port (1 to 65535). Start the server from `lab01`: it serves `./data`
relative to the working directory. Stop it with Ctrl+C.

With Docker (or Podman, with the same arguments):

```bash
docker build -t http_server .
docker run --rm -p 8080:8080 -v ./data:/app/data:Z http_server
```

The volume shares `data/` with the host; `:Z` is needed on SELinux systems such as Fedora.
Details, startup output and other ports: [docs/running.md](docs/running.md).

On AWS, the same container runs on an EC2 instance of the AWS Academy Learner Lab
(`us-east-1`, Amazon Linux 2023, `t3.micro`); nothing in the code is specific to AWS. On the
instance, from the cloned repository:

```bash
cd ~/DistributedSystemsLabs/lab01
docker build -t http_server .
docker run --rm --name http_server -p 8080:8080 -v "$PWD/data":/app/data http_server
```

The security group opens port 8080 only to the users' IPv4 addresses, and the public IP
changes at every lab session. Setup, tests from the laptop and troubleshooting:
[docs/aws.md](docs/aws.md).

## Requirements coverage

This table covers the Basic part of [the lab text](docs/assignment.md). The Bonus part is
covered in [the next table](#bonus-part-pub-sub-broker).

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
| Concurrent `POST`s are atomic; `GET` sees one complete version | `Store.Put` | Write to a temporary file, `fsync` it, then `os.Rename`. See [Atomic POST](docs/design.md#atomic-post). |
| Well-formed error responses | `response.Error` (`internal/response`) | Every error is a full HTTP/1.0 response with `Content-Type: text/plain`, `Content-Length` and a body that holds the status text (for example `Not Found`). |
| No web frameworks | `go.mod` | No dependencies outside the standard library. |
| Containers (recommended) | `Dockerfile` | Multi-stage build; the runtime image is pinned to `alpine:3.24.4`. See [docs/running.md](docs/running.md#with-docker-or-podman). |

### Bonus part: pub-sub broker

| Requirement | Where | How |
|---|---|---|
| Same binary and port as the file server, same networking rules | `App.Route` (`internal/app`) | The four broker paths are matched in `Route`, before the file handlers. They go through the same `Server`, connection limit and deadlines. |
| `POST /subscribe?topic=X` → `201` with `{"id": N}`, a new id for every subscription | `App.subscribe`, `Broker.Subscribe` (`internal/broker`) | Ids come from a single counter, so they are unique across topics. The body is `{"id":N}` with `Content-Type: application/json`. |
| `POST /unsubscribe?topic=X&id=N` | `App.unsubscribe`, `Broker.Unsubscribe` | Removes the subscriber and its queue, answers `200 OK`. A waiting `GET /events` for it is woken up and answered `404`. |
| `POST /publish?topic=X` to every current subscriber | `App.publish`, `Broker.Publish` | The body is queued for every subscriber of `X`. Answers `200 OK`, also when the topic has no subscribers. |
| `GET /events?topic=X&id=N` with long polling, `204` on timeout | `App.events`, `Broker.Wait` | Answers `200 OK` with all queued messages as a JSON array as soon as there is one, or `204 No Content` after 15 seconds. See [Long polling](docs/design.md#long-polling). |
| Missing topic or invalid id → 400, unknown subscriber → 404, other methods → 501 | `topicParam`, `idParam`, `requireMethod` (`internal/app`) | An id that is not an integer from 1 up is invalid. An id used with the wrong topic counts as unknown. A known method other than the operation's gets `501`, an unknown method `400` as for files. |
| Shared state protected | `Broker` | A single `sync.Mutex` guards all the state. No operation blocks while holding it. |
| A sequence number for each message, subscribers receive them in order | `Broker.Publish` | The number is assigned and the message queued in the same critical section, so concurrent publishers are totally ordered. |
| Only messages published while subscribed | `Broker.Subscribe`, `Broker.Unsubscribe` | A new subscriber starts with an empty queue; an unsubscribed one has no queue left. |
| Bounded queue per subscriber, publish never blocks | `Broker.Publish` | Each queue is a buffered channel of 64 messages (set in `main`). When it is full, that subscriber misses the message. |
| Waiting long polls may be excluded from the limit of 10 | `server.Handler`, `App.events` | The handler gets a `release` function that gives the connection's slot back. `events` calls it once the parameters are valid. |

## Design decisions

The code has three layers, each a package under `internal/`: transport (`server`),
application (`app`, `response`) and service (`filestore`, `broker`). See
[Project structure and layering](docs/design.md#project-structure-and-layering).

- **Atomic POST.** `Store.Put` writes a temporary file next to the target, syncs it and
  renames it over the target, so a `GET` sees the old or the new version, never a mix, with
  no lock. [Why](docs/design.md#atomic-post)
- **Connection limit before `Accept`.** A semaphore slot is taken before `Accept`, so extra
  clients wait in the kernel backlog; `sync.Once` returns each slot exactly once.
  [Details](docs/design.md#connection-limit-and-release)
- **Two-phase deadline.** 5 s for the headers, so idle clients lose their slot quickly, then
  60 s for body and response, so large transfers are not cut off.
  [Why](docs/design.md#two-phase-deadline)
- **One mutex for the broker.** A single `sync.Mutex` guards all broker state and is never
  held while waiting. [Details](docs/design.md#pub-sub-broker-design)
- **Long polling.** Each poll is still one request and one response with `Content-Length`,
  like a file request; its slot is released while it waits. [Why](docs/design.md#long-polling)
- **Valid vs implemented methods.** The nine RFC methods are valid; `GET` and `POST` are
  implemented, the other seven get `501`, anything else `400`.
  [Details](docs/design.md#valid-vs-implemented-methods)
- **Extension checked first.** A missing `.pdf` gets `400`, not `404`, and temporary files
  (no extension) can never be served. [Details](docs/design.md#order-of-checks)
- **Path cleaning.** `filepath.Clean("/" + path)` keeps `..` inside `data/`.
  [Details](docs/design.md#path-handling)
- **Streaming GET.** Files are copied from disk with `io.Copy`, so memory does not grow with
  file size. [Details](docs/design.md#streaming-get)
- **`201 Created` for every successful `POST`**, new or replaced file.
  [Why](docs/design.md#post-status)
- Further decisions (port validation, file mode `0644`, startup cleanup, other errors → `400`):
  [Other decisions](docs/design.md#other-decisions).

## Testing

From the `lab01` folder:

```bash
go test -race ./...                       # unit tests, no server needed
./http_server 8080                        # in another terminal, for the two scripts
(cd scripts && bash atomic.sh)            # needs the server on port 8080
bash scripts/broker_test.sh 8080          # about 20 s
```

- `go test` runs 29 unit tests of `filestore`, `response`, `app` and `broker`: file storage
  and lookup, exact response bytes, status codes through `App.Route`, and broker ordering,
  queues and wake-ups.
- `atomic.sh` sends 20 rounds of concurrent `POST`s of two 5 MB versions plus `GET`s of the
  same file; every line it prints must show a single letter and the size `5000000`.
- `broker_test.sh` runs 46 end-to-end checks of the broker over HTTP; the last line must be
  `Result: 46 passed, 0 failed`.

Every test and check is explained in [docs/testing.md](docs/testing.md); curl and nc examples
for every status code are in [docs/manual-testing.md](docs/manual-testing.md). To remove the
files the two scripts leave behind:

```bash
rm -f scripts/A.txt scripts/B.txt scripts/got_*.txt data/big.txt data/run[0-9]*.txt
```

## Known limitations

- No graceful shutdown: Ctrl+C or `SIGTERM` cuts off requests in progress.
- No size limit on uploads: a client can fill the disk.
- Fixed timeouts: a transfer that needs more than 60 s after the headers is cut off, and 10
  silent clients block all others for up to 5 s.
- The directory is not synced after the rename: a crash right after a `POST` may bring back
  the previous (complete) version.
- A missing `Host` header is accepted, because `http.ReadRequest` does not enforce it.
- Delivery is at most once: a message is lost if the queue is full or if the client of a
  waiting poll disconnects before the answer.
- Two polls with the same id share its queue, so each gets only part of the messages.
- No limit on subscriptions or waiting polls; each waiting poll holds a goroutine and a socket.
- Sequence counters of topics are never removed, so memory grows with distinct topic names.
- Message bodies are JSON strings: bytes that are not valid UTF-8 become `U+FFFD`.
- No authentication (ids are consecutive and guessable) and no persistence (broker state is
  in memory only).
