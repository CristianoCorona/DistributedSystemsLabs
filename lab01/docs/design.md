# Design

This document explains how the server is built and why. The [README](../README.md) gives a
one-line summary of each decision and links here.

- [Project structure and layering](#project-structure-and-layering)
- [Connection limit and release](#connection-limit-and-release)
- [Two-phase deadline](#two-phase-deadline)
- [Atomic POST](#atomic-post)
- [Pub-sub broker design](#pub-sub-broker-design)
- [Long polling](#long-polling)
- [Other decisions](#other-decisions)
- [Known limitations in detail](#known-limitations-in-detail)

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
├── docs/                 # lab text and the detailed documentation
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
2. **Application** (package `app`, plus package `response`): `App.Route` decides what to do
   with a request. The unexported handlers `serveFile` and `storeFile` (`handlers.go`) and
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

## Connection limit and release

**Counting semaphore.** `server.New` creates `Server.sem`, a buffered channel with capacity
`Config.MaxConns` (10, set in `main`). Sending into it takes a slot; receiving from it gives
one back. When the channel is full, the send blocks.

**The slot is taken before `Accept`.** In `Server.Serve`, the slot is acquired before
`listener.Accept()`, not inside the goroutine. While 10 connections are active, the server
does not call `Accept`. New clients wait in the kernel backlog and their deadline has not
started yet. If `Accept` fails, the slot is returned at once. Because the slot is taken
before the goroutine is started, there are never more than 10 connection goroutines.

**The slot is released exactly once.** The goroutine builds a `release` function around
`sync.Once` and defers it, so the slot comes back when `handleConn` returns. The same
`release` is passed down to the handler (see `server.Handler`), so a handler that only waits
can give the slot back early. `sync.Once` makes the early call and the deferred call return
the slot exactly once together; without it, the second receive would steal a slot that
belongs to another connection. Only `App.events` calls `release` early (see
[Long polling](#long-polling)).

The connection limit can be checked by hand with `nc` (see
[Connection limit](manual-testing.md#connection-limit)).

## Two-phase deadline

`server.Config` holds two durations, and `Server.handleConn` sets them one after the other
with `conn.SetDeadline`, which covers reads and writes.

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

The timeouts can be observed with `nc` (see [Timeouts](manual-testing.md#timeouts)).

## Atomic POST

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

`scripts/atomic.sh` checks this behaviour (see [testing.md](testing.md#scriptsatomicsh)).

## Pub-sub broker design

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
wakes up a waiting `Wait`. Closing is safe because, once `Unsubscribe` releases the mutex, the
subscriber can no longer be found, so nothing will ever send on the closed channel (a send on
a closed channel would panic).

**Message format.** `GET /events` answers with a JSON array of every queued message, oldest
first, for example `[{"seq":2,"body":"one"},{"seq":3,"body":"two"}]`. Returning all of them
at once saves a round trip per message when a subscriber has fallen behind. The body is sent
as a JSON string. The field names come from the JSON tags of `broker.Message`.

## Long polling

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
   polls do not slow down a file request (checked by group 8 of
   [`scripts/broker_test.sh`](testing.md#scriptsbroker_testsh)).
2. **Calls `Broker.Wait`** with `pollTimeout` (15 seconds, a constant in
   `internal/app/pubsub.go`). `Wait` returns as soon as one of these happens:

   | Event | Result |
   |---|---|
   | The queue has messages (already, or after a publish) | `200 OK` with all of them |
   | 15 seconds pass with no message | `204 No Content`, with no `Content-Type` or `Content-Length` (RFC 9110 forbids `Content-Length` in a `204`) |
   | The subscriber is unsubscribed | `404 Not Found` |

`pollTimeout` must stay below `RequestTimeout` (60 s), or the connection deadline would
expire before the `204` is written.

## Other decisions

### Valid vs implemented methods

The valid methods are the nine defined by RFC 9110 and RFC 5789: `GET`, `HEAD`, `POST`,
`PUT`, `PATCH`, `DELETE`, `CONNECT`, `OPTIONS` and `TRACE`. They are listed in
`knownMethods`. `GET` and `POST` are implemented, and the other seven get
`501 Not Implemented`. Any other method, including lowercase forms like `get`, gets
`400 Bad Request`, as the lab asks for invalid requests. `HEAD` also gets `501`. On the
broker paths, a known method other than the operation's gets `501` (`requireMethod`).

### One request per connection

Every response starts with `HTTP/1.0` and has `Connection: close`. `Server.handleConn`
closes the connection after the response. There is no keep-alive.

### Port validation

The argument must be an integer from 1 to 65535. Port 0 is rejected: `net.Listen` would pick
a random free port, and clients would not know where to connect. The parsed number is used to
build the address, so `+80` or `080` both listen on `:80`.

### Document root setup

The root path is the single constant `docRoot` in `cmd/http_server/main.go`. `main` passes it
to `filestore.Open`, which creates it with `os.MkdirAll` before the server starts, so a fresh
clone works without manual setup. If it cannot be created, the server exits.

### Startup cleanup of temporary files

If the process stops during an upload, its `tmp-*` file stays on disk, because deferred
cleanup does not run. Before the server starts accepting connections, `filestore.Open`
deletes these files from the root and logs how many it removed (`removeTempFiles`). It only
deletes regular files named `tmp-` followed by digits, the form `os.CreateTemp` produces. A
client file such as `tmp-notes.txt` is kept. Subdirectories are not scanned.

### File permissions

Every stored file gets mode `0644` (owner read/write, everyone read), whether it is new or
replaces an existing file. `os.CreateTemp` would otherwise leave it at `0600`.

### POST status

A successful `POST` returns `201 Created` with the body `File stored successfully.`: the body
is now a resource that a later `GET` returns. The same status is used for new and replaced
files. To tell the two cases apart (and answer `200 OK` for a replacement), the server would
have to check whether the file exists before the rename, and with concurrent uploads that
check could be outdated by the time of the rename.

### Streaming GET

`Store.Get` opens the file and returns the open descriptor, its size (from `Stat` on the
descriptor) and its `Content-Type`. `response.Stream` writes the headers with
`Content-Length` equal to that size and copies the file to the connection with `io.Copy`, so
memory use does not grow with the file size. On Linux, `io.Copy` from a file to a TCP
connection uses the `sendfile` system call: the data goes from the page cache to the socket
without passing through user space. This was checked with `strace`. `serveFile` closes the
file when the copy ends. `response.Write`, used for the short messages, is `Stream` with a
body held in memory.

### Directories are not files

On Linux a directory can be opened like a file. A `GET` for a directory whose name has an
allowed extension (for example `data/photos.jpg/`) answers `404 Not Found`.

### Path handling

`Store.resolve` cleans the URL path with `filepath.Clean("/" + path)` and joins it to the root
of the store (`docRoot`). Cleaning it as an absolute path makes `..` segments stop at the
root, even for a name passed without the leading `/`. Subdirectories of `data/` work if they
exist. A `POST` to a missing subdirectory fails with `400`. The server never creates
subdirectories.

### Extension matching

Extensions are matched case-insensitively, so `IMAGE.JPG` is accepted as `image/jpeg`. File
names stay case-sensitive, as on the Linux filesystem: `INDEX.HTML` and `index.html` are
different files.

### Order of checks

The extension is checked before the filesystem is accessed. A request for a missing `.pdf`
file returns `400`, not `404`.

### Other errors

Unexpected errors from the filesystem, such as permission denied, a missing directory or a
failed body read, are logged and answered with `400 Bad Request`, as the lab text says for
"any other error condition". The one exception is a JSON response that cannot be encoded
(`writeJSON`): that is the server's fault, so it is answered `500 Internal Server Error`.

## Known limitations in detail

The [README](../README.md#known-limitations) lists these in one line each.

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
