# Testing

There are three kinds of tests:

| What | Needs a running server | Checks |
|---|---|---|
| [Go unit tests](#go-unit-tests) (`go test`) | No | 28 tests in 4 packages: `filestore`, `response`, `app`, `broker` |
| [`scripts/atomic.sh`](#scriptsatomicsh) | Yes, on port 8080 | Concurrent `POST`s and `GET`s of one file never produce a mixed or truncated file |
| [`scripts/broker_test.sh`](#scriptsbroker_testsh) | Yes, on the given port | 46 end-to-end checks of the pub-sub broker over HTTP |

The [race detector](#race-detector) can be added to both the unit tests and the server.

For each test below: what it does, what it expects, which code it exercises, and what a
failure would mean.

## Go unit tests

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

**What is not unit-tested, and why.** The transport layer (`server`) has no unit tests: its
behaviour depends on real sockets and timing, and the manual checks in
[manual-testing.md](manual-testing.md) (timeouts, connection limit) and group 8 of
`broker_test.sh` cover it. The broker paths have no `app` tests either: `broker_test.sh`
checks them end to end.

### `filestore` (`filestore_test.go`)

Each test gets its own root from `t.TempDir()` (helper `openTemp`), which `testing` deletes
afterwards, so no test touches the real `./data`.

- **TestPutThenGet.** `Put("/hello.txt", "hello")`, then `Get("/hello.txt")`. Expects the
  body `hello`, size 5 and `Content-Type` `text/plain`. Exercises `Store.Put`, `Store.Get`,
  `Store.resolve`. A failure means a stored file does not come back as it was sent, or the
  size (which becomes `Content-Length`) is wrong.
- **TestPutReplacesExistingFile.** `Put` of `old version`, then `Put` of `new` on the same
  name. Expects `Get` to return exactly `new`. Exercises the temp-file-and-rename path of
  `Store.Put`. A failure (for example `new version`) means the second write overwrote the
  first bytes in place instead of replacing the whole file.
- **TestContentTypes.** `Put` and `Get` of one file per allowed extension, plus `/B.JPG`.
  Expects the six types of `mimeTypes`, and `image/jpeg` for the upper-case `.JPG`.
  Exercises `mimeTypes` and the case-insensitive lookup in `Store.resolve`. A failure means a
  browser would get the wrong `Content-Type`.
- **TestBadExtension.** `Get` and `Put` of `/document.pdf`, `/noextension`, `/` and
  `/tmp-123`. Expects `ErrBadExtension` for all of them, and an empty root afterwards.
  Exercises `Store.resolve` and the fact that the extension is checked before the
  filesystem is touched. A failure means a forbidden file could be stored or served; for
  `/tmp-123`, that a half-written temporary file could be served.
- **TestGetMissingFile.** `Get("/missing.html")` on an empty root. Expects `ErrNotFound`.
  Exercises the `os.ErrNotExist` → `ErrNotFound` mapping in `Store.Get`. A failure means a
  missing file would not produce `404`.
- **TestGetDirectoryIsNotFound.** Creates a directory `photos.jpg` in the root, then
  `Get("/photos.jpg")`. Expects `ErrNotFound`. Exercises the `IsDir` check in `Store.Get`. A
  failure means the server would try to send a directory as a file.
- **TestPathTraversalStaysInRoot.** `Put` of `/../escape.txt`, `../escape.txt` and
  `a/../../escape.txt` into a root that is a subdirectory, then `Get("/../../../escape.txt")`.
  Expects every file to land inside the root, nothing in the parent, and the `Get` to read it
  back. Exercises `filepath.Clean("/" + name)` in `Store.resolve`. A failure means a client
  could read or write files outside `data/`.
- **TestPutFileMode.** `Put` of one file, then `os.Stat`. Expects mode `0644`. Exercises the
  `Chmod` in `Store.Put`. A failure means stored files keep the `0600` mode of
  `os.CreateTemp`.
- **TestPutLeavesNoTempFile.** A successful `Put("/ok.txt")`, then a `Put("/failed.txt")`
  whose body (`failingReader`) returns `partial` and then an error, like a client that
  disconnects. Expects an error from the second `Put` and a root that contains only `ok.txt`.
  Exercises the deferred `os.Remove` in `Store.Put`. A failure means interrupted uploads
  leave `tmp-*` files behind, or create a partial target.
- **TestOpenCreatesRoot.** `Open` on a path `a/b/data` that does not exist. Expects a
  directory there. Exercises `os.MkdirAll` in `filestore.Open`. A failure means a fresh clone
  would not start without creating `data/` by hand.
- **TestOpenRemovesTempFiles.** Creates `tmp-123`, `tmp-notes.txt`, `tmp-`, `hello.txt` and a
  directory `tmp-456`, then calls `Open`. Expects only `tmp-123` to be removed. Exercises
  `removeTempFiles`. A failure means either leftovers survive a restart, or the cleanup
  deletes client files or directories.

### `response` (`response_test.go`)

Each test writes into a `bytes.Buffer` and compares the exact bytes, including `\r\n` and
`Content-Length`: a client parses these literally, so a missing `\r` is a real bug.

- **TestWrite.** `Write(200, "text/html", "<h1>Hello</h1>")`. Expects the full response with
  `Content-Length: 14`. Exercises `response.Write` and `response.Stream`. A failure means
  every short response the server sends is malformed.
- **TestError.** `Error` with 400, 404, 408 and 501. Expects a `text/plain` response whose
  body is the status text (for example `Not Found`). Exercises `response.Error`. A failure
  means error responses are not well-formed, as the lab requires.
- **TestStream.** `Stream(200, "text/plain", 5, "hello")` from a reader. Expects the full
  response. Exercises `response.Stream`, the function used to send files. A failure means
  `GET` responses are malformed.
- **TestEmpty.** `Empty(204)`. Expects only the status line and `Connection: close`, with no
  `Content-Type` or `Content-Length`. Exercises `response.Empty`. A failure means the
  long-poll timeout answer breaks RFC 9110.

### `app` (`app_test.go`)

Requests are parsed from a string with `http.ReadRequest` and passed to `App.Route` through
`net.Pipe()`, an in-memory pair of connected `net.Conn` (helper `roundTrip`). `net.Pipe` is
synchronous: a `Write` on one end blocks until the other end reads it. The test therefore
reads the response in a separate goroutine while `Route` writes it; reading only after
`Route` returned would deadlock on the first `Write`.

- **TestRouteStatusCodes.** A table of eight requests, each a subtest, checking only the
  status line. Exercises `knownMethods`, `App.Route`, `App.serveFile`, `App.storeFile` and
  the error mapping. A failure means the server answers a request with the wrong status code.

  | Subtest | Request | Expected |
  |---|---|---|
  | unknown method | `FOO /hello.txt` | `400` |
  | lowercase method | `get /hello.txt` | `400` (methods are case-sensitive) |
  | DELETE | `DELETE /hello.txt` | `501` |
  | PUT | `PUT /hello.txt` with a body | `501` |
  | GET missing | `GET /missing.html` | `404` |
  | GET bad extension | `GET /document.pdf` | `400` |
  | POST bad extension | `POST /document.pdf` | `400` |
  | POST missing directory | `POST /nodir/a.txt` | `400` |

- **TestRoutePostThenGet.** `POST /page.html` with `<h1>Hello</h1>`, then `GET /page.html`.
  Expects the exact `201 Created` response with `File stored successfully.`, then a `200`
  with `text/html` and the same body. Exercises the whole application path for files. A
  failure means a file stored over HTTP cannot be read back over HTTP.

### `broker` (`broker_test.go`)

The broker is tested without the network. Tests that expect `Wait` to return at once or to
time out use a 50 ms timeout (`short`) instead of the 15 s `pollTimeout`.

- **TestSubscribeGivesUniqueIDs.** Subscribes to `news`, `sports`, `news`. Expects three
  different ids. Exercises the single counter in `Broker.Subscribe`. A failure means two
  subscriptions could share a queue.
- **TestPublishThenWait.** Subscribes, publishes `first` and `second`, then one `Wait`.
  Expects both messages, with sequence numbers 1 and 2, in order. Exercises `Broker.Publish`
  and the draining loop in `Broker.Wait`. A failure means queued messages are lost, reordered
  or delivered one per poll.
- **TestWaitTimesOut.** `Wait` with nothing published. Expects `(nil, nil)` after at least
  50 ms. Exercises the `time.After` branch of `Broker.Wait`. A failure means a poll would not
  end with `204`, or would end too early.
- **TestWaitWakesUpOnPublish.** Starts a `Wait` with a 5 s timeout and publishes `hello`
  after 20 ms from another goroutine. Expects the message. Exercises the wake-up of a blocked
  `Wait`. A failure means long polling does not work: the subscriber would only see the
  message at its next poll.
- **TestOnlyMessagesOfOwnTopic.** Subscribes to `news`, publishes to `sports`. Expects
  nothing for the `news` subscriber. Exercises the topic index `Broker.topics`. A failure
  means messages leak between topics.
- **TestOnlyMessagesAfterSubscribing.** Publishes `old`, subscribes, publishes `new`. Expects
  only `new`. Exercises the empty queue of a new subscriber. A failure means a subscriber
  receives history it did not subscribe to.
- **TestUnknownSubscriber.** `Wait` with an unknown id, `Wait` and `Unsubscribe` with a valid
  id on the wrong topic. Expects `ErrNotFound` for all three. Exercises the id and topic
  check in `Wait` and `Unsubscribe`. A failure means `404` would not be returned, or a client
  could read another topic's queue with its id.
- **TestUnsubscribe.** Unsubscribes, publishes, then `Wait` and a second `Unsubscribe`.
  Expects no panic and `ErrNotFound` for both. Exercises `Broker.Unsubscribe` and the
  closed-channel safety argument. A failure (a panic) means `Publish` sent on a closed queue.
- **TestUnsubscribeWakesWaiter.** Starts a `Wait` with a 5 s timeout and unsubscribes after
  20 ms. Expects `ErrNotFound` within 1 s. Exercises `close(sub.queue)` and the `!open`
  branch of `Wait`. A failure means a waiting poll for a removed subscriber hangs until its
  timeout instead of getting `404`.
- **TestPublishNeverBlocks.** Queue size 2, 100 publishes in a goroutine. Expects them to
  finish within 1 s, and the subscriber to get only sequence numbers 1 and 2. Exercises the
  length check in `Broker.Publish`. A failure means one slow subscriber could block every
  publisher.
- **TestConcurrentPublishersKeepOrder.** 10 goroutines publish 50 messages each to a topic
  with two subscribers. Expects each subscriber to get all 500 messages with sequence numbers
  1 to 500, with no gaps, duplicates or reordering. Exercises the single critical section
  that assigns the number and queues the message. A failure means concurrent publishers are
  not totally ordered. Run with `-race`, it also checks for data races on the broker state.

## `scripts/atomic.sh`

**Purpose.** Shows that concurrent `POST`s of the same file are atomic: a `GET` always gets
one complete version.

**What it does.** It creates two 5 MB files, `A.txt` (all `a`) and `B.txt` (all `b`), uploads
the first to `big.txt`, then starts 20 rounds in parallel. Each round has one `POST` of the
`a` version, one `POST` of the `b` version and one `GET` into `got_<n>.txt`. Finally, it
reduces each downloaded file to its distinct letters and its size and counts identical lines.

**How to run it.** The script expects the server on port 8080 and writes `A.txt`, `B.txt` and
`got_*.txt` to the current directory (git ignores them in `scripts/`). Run it from `scripts/`
so these files stay there:

```bash
(cd scripts && bash atomic.sh)
```

**Expected output.** The split between `a` and `b` varies from run to run:

```
     13 a 5000000
      7 b 5000000
```

A correct server prints only lines with a single letter (`a` or `b`) and the full size
`5000000`, and the counts add up to 20. A mixed file would print both letters (for example
`ab` or `ba`), which would
mean that two `POST`s wrote into the same file. A truncated file would show a smaller size,
which would mean that a `GET` read a file while it was being written. It exercises
`Store.Put` (temporary file, `Sync`, `os.Rename`) and `Store.Get` (streaming from an open
descriptor).

The script overwrites `data/big.txt`. To remove the files it creates:

```bash
rm -f scripts/A.txt scripts/B.txt scripts/got_*.txt data/big.txt
```

## `scripts/broker_test.sh`

**Purpose.** Tests the pub-sub broker end to end, over real HTTP connections, against a
running server. It runs 46 checks in 8 groups and takes about 20 seconds, because one check
waits for the 15-second long-poll timeout.

```bash
./http_server 8080                  # terminal 1, from lab01
bash scripts/broker_test.sh 8080    # terminal 2, from lab01
```

Each check prints `PASS` or `FAIL`. The last line is `Result: 46 passed, 0 failed`, and the
exit status is 0 only if every check passes. Each run uses its own topic names (prefix
`run<PID>`), so it can be repeated without restarting the server, and sequence numbers start
at 1 on every topic. Before the checks, it sends one request to see if the server is up, and
stops with a message if it is not. The script stores one file, `data/run<PID>.txt`, which
can be removed with `rm -f data/run[0-9]*.txt`.

### 1. Subscribe (4 checks)

`POST /subscribe` returns `201 Created`, `Content-Type: application/json` and a body of the
form `{"id":N}`; three subscriptions on two topics get three different ids. Exercises
`App.subscribe`, `writeJSON` and `Broker.Subscribe`. A failure means the response does not
follow the lab's format, or ids are reused.

### 2. Bad requests (17 checks)

| Request | Expected | Code |
|---|---|---|
| `POST /subscribe` without topic, or with `topic=` | `400` | `topicParam` |
| `POST /publish` without topic | `400` | `topicParam` |
| `GET /events` without topic, without id, with `id=abc`, with `id=0` | `400` | `topicParam`, `idParam` |
| `POST /unsubscribe` with `id=abc` | `400` | `idParam` |
| `GET /events` with an unknown id, or with an id of another topic | `404` | `Broker.Wait` |
| `POST /unsubscribe` with an unknown id | `404` | `Broker.Unsubscribe` |
| `GET /subscribe`, `GET /publish`, `GET /unsubscribe`, `POST /events`, `DELETE /events` | `501` | `requireMethod` |
| `FOO /events` | `400` | `knownMethods` in `App.Route` |

A failure means a client mistake gets the wrong status code.

### 3. Publish and receive (5 checks)

A publish answers `200`, also on a topic with no subscribers. A queued message is delivered
as `[{"seq":1,"body":"hello"}]`. Three more publishes come back in one response with
sequence numbers `2 3 4`. A body with quotes and a newline comes back JSON-escaped
(`"say \"hi\"\nbye"`). Exercises `App.publish`, `Broker.Publish`, the draining loop in
`Broker.Wait` and `encoding/json`. A failure means messages are lost, reordered, or produce
invalid JSON.

### 4. Long polling (5 checks)

A poll is started in the background and a message is published 1 second later: the poll
receives it, and it took between 0.9 and 3 seconds, so it really waited and was answered
right after the publish. A second poll with nothing published answers
`HTTP/1.0 204 No Content`, without `Content-Length`, after at least 10 seconds. Exercises
`App.events`, `Broker.Wait` and `response.Empty`. A failure means the server answers
immediately (no long polling), never wakes up, or sends an invalid `204`.

### 5. Subscription rules (8 checks)

A subscriber only gets messages of its own topic, and only those published after it
subscribed (`seq` 2, not 1). Unsubscribe answers `200`; a later publish to that topic still
answers `200`; a poll and a second unsubscribe for the removed id answer `404`. A poll that
is waiting when its subscriber is unsubscribed is answered `404` at once (in less than
3 seconds). Exercises `Broker.Subscribe`, `Broker.Unsubscribe` and the closed-queue wake-up
in `Broker.Wait`. A failure means the lab's subscription rules are broken, or a waiting poll
hangs after unsubscribe.

### 6. Bounded queue, publish never blocks (3 checks)

100 publishes (queue size 64 + 36) to a subscriber that does not poll all finish (this check
always passes if the loop ends; the elapsed time is printed). The next poll returns sequence
numbers 1 to 64: the first 64 were kept, the rest were dropped. One more publish then arrives
with sequence number 101, and the gap shows the dropped messages. Exercises the length check
in `Broker.Publish`. A failure means the queue is unbounded, or a full queue blocks the
publisher. The script's `QUEUE=64` must match `broker.New(64)` in `main`.

### 7. Concurrent publishers keep the order (2 checks)

5 background processes publish 10 messages each to a topic with two subscribers. Each
subscriber gets sequence numbers 1 to 50, in order. Exercises the single critical section in
`Broker.Publish`. A failure means two subscribers could see concurrent messages in different
orders, or messages are lost.

### 8. Waiting polls do not use up the 10 connection slots (2 checks)

12 subscribers each start a poll (more than the 10 slots). After 1 second, a file is stored
and read back: the `GET` must answer `200` in less than 1 second. Then one publish must wake
all 12 polls with `200`. Exercises the early `release()` in `App.events` and the `sync.Once`
in `Server.Serve`. A failure of the first check means waiting polls hold connection slots, so
10 idle subscribers would block the whole server.

## Race detector

Build with the race detector, start the server and run the atomicity script against it:

```bash
go build -race -o http_server ./cmd/http_server
./http_server 8080
```

In a second terminal, run `(cd scripts && bash atomic.sh)`. If the race detector finds a data
race, the server prints `WARNING: DATA RACE` to standard error. With the current code it
prints nothing beyond the normal request log. Rebuild without `-race` afterwards
(`go build -o http_server ./cmd/http_server`).
