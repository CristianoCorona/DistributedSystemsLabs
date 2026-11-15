# Lab 01: Concurrent HTTP server

## Overview

`http_server` is an HTTP file server written in Go for TDA596 Distributed Systems. It is built
directly on TCP sockets from the `net` package. From `net/http` it uses only
`http.ReadRequest` to parse requests and the status code constants and `http.StatusText`.
Responses are written by hand.

- `GET` returns a file from the document root `./data`.
- `POST` stores the request body as a file in `./data`. The update is atomic.
- Each connection is handled in its own goroutine. At most 10 connections are handled at the
  same time.
- Each connection carries one request and is then closed.
- At startup the server creates `./data` if it is missing and removes temporary upload files
  left behind by a previous run.

Only the Basic part of the assignment is implemented. The pub-sub broker (Bonus part) is not:
`app.route` has only a `TODO` for it.

## Requirements coverage

This table covers the Basic part of `docs/assignment.md`.

| Requirement | Where | How |
|---|---|---|
| A new goroutine for each client | `Server.Serve` (`server.go`) | After each `Accept`, `Serve` starts a goroutine that runs `Server.handleConn`. |
| Binary called `http_server`, port as first argument, no hard-coded port | `main` (`main.go`) | Build with `go build -o http_server .`. With anything other than exactly one argument, the program logs `Usage: ./http_server <port>` and exits with status 1. |
| Compiles without errors or warnings | the whole module | `go build`, `go vet ./...` and `gofmt -l .` produce no output. |
| No fixed IP address for the server or its clients | `main`, `Server.Serve` | Listens on `":" + port`, which means all interfaces (IPv4 and IPv6). Client addresses are not checked. |
| Networking with `net` only | `Server.Serve` | `net.Listen("tcp", addr)` and `listener.Accept()`. |
| `net/http` only for parsing | `Server.handleConn`, `writeResponse` (`response.go`) | `http.ReadRequest` parses the request. The status line and headers are written with `fmt.Fprintf`. `http.Response`, `http.Serve` and `http.ListenAndServe` are not used. |
| At most 10 concurrent connections; wait when the limit is reached | `NewServer`, `Server.Serve` | The `Server.sem` field is a buffered channel with capacity 10 (set in `main`) that works as a counting semaphore. A slot is taken **before** `Accept` and released when the connection goroutine returns. |
| Check that the request is properly formatted | `Server.handleConn` | If `http.ReadRequest` fails (malformed request line or headers), the server answers `400 Bad Request`. |
| Serve `html`, `txt`, `gif`, `jpeg`, `jpg`, `css` with the right `Content-Type` | `mimeTypes`, `Store.resolve` (`filestore/filestore.go`) | `mimeTypes` maps `.html`→`text/html`, `.txt`→`text/plain`, `.gif`→`image/gif`, `.jpeg`/`.jpg`→`image/jpeg` and `.css`→`text/css`. |
| Any other extension → 400 | `Store.resolve`, `app.serveFile`, `app.storeFile` (`handlers.go`) | `resolve` returns `ErrBadExtension`, which the handlers map to `400` for both `GET` and `POST`. Files with no extension are also rejected. |
| File does not exist → 404 | `Store.Get`, `app.serveFile` | `os.ErrNotExist` becomes `ErrNotFound`, which the handler maps to `404`. |
| `GET` and `POST` implemented; other valid methods → 501 | `knownMethods`, `app.route` (`router.go`) | The nine methods of RFC 9110 and RFC 5789 are known. `GET` and `POST` are dispatched to handlers. The other seven get `501 Not Implemented`. |
| Invalid request → 400 | `app.route`, `Server.handleConn` | An unknown method (for example `FOO`) or an unparsable request gets `400`. |
| Any other error condition → 400 | `app.serveFile`, `app.storeFile` | Unexpected filesystem errors are logged and answered with `400`. |
| `POST` stores the file and a later `GET` returns it | `app.storeFile`, `Store.Put` | `Put` writes the body to `data/<path>`. A successful `POST` returns `200 OK`. |
| Concurrent `POST`s are atomic; `GET` sees one complete version | `Store.Put` | Write to a temporary file, then `os.Rename`. See [Atomic POST](#atomic-post). |
| Well-formed error responses | `writeError` (`response.go`) | Every error is a full HTTP/1.0 response with `Content-Type: text/plain`, `Content-Length` and a body that holds the status text (for example `Not Found`). |
| No web frameworks | `go.mod` | No dependencies outside the standard library. |
| Containers (recommended) | `Dockerfile` | Multi-stage build. See [Run with Docker or Podman](#run-with-docker-or-podman). |

## Project structure and layering

```
lab01/
├── main.go               # argument check, document root setup, temp file cleanup, startup
├── server.go             # TCP listener, connection limit, deadline, request parsing
├── router.go             # method validation and dispatch
├── handlers.go           # GET and POST handlers, error -> status code mapping
├── response.go           # writes HTTP/1.0 responses
├── filestore/
│   └── filestore.go      # extension check, path cleaning, file read, atomic write
├── docs/
│   └── assignment.md     # lab text
├── scripts/
│   └── atomic.sh         # concurrency test for atomic POST
├── data/                 # document root, created at startup, ignored by git
├── Dockerfile
├── .dockerignore
├── go.mod
└── README.md
```

The code has three layers. Dependencies go downwards only.

1. **Transport** (`Server` in `server.go`): accepts TCP connections, enforces the connection
   limit and the deadline, and parses the request with `http.ReadRequest`. It does not know
   about files. It passes each parsed request to a handler function of type
   `func(net.Conn, *http.Request)`.
2. **Protocol** (`app` in `router.go` and `handlers.go`, plus `response.go`): decides what to
   do with a request and maps errors from the domain layer to status codes. `app.route` is the
   handler that `main` passes to the server.
3. **Domain** (`filestore.Store`): `Store.Get` and `Store.Put` resolve a URL path to a file
   under the root directory and read or write it. They return the errors `ErrNotFound` and
   `ErrBadExtension` and do not know about HTTP.

`main` wires the layers together. The document root is the constant `docRoot` (`./data`).
Before the server starts, `main` creates the directory with `os.MkdirAll(docRoot, 0o755)` and
calls `removeTempFiles`. Git ignores `data/`, so its contents are never committed.

## Build and run locally

Requires Go 1.27.3 or later (see `go.mod`). From the `lab01` folder:

```bash
go vet ./...
go build -o http_server .
./http_server 8080
```

The server prints two lines at startup:

```
2026/11/16 00:51:08 Removed 0 leftover temporary file(s) from ./data
2026/11/16 00:51:08 Listening on :8080
```

- The only argument is the TCP port.
- Files are read from and written to `./data`, relative to the **current working directory**.
  Start the server from `lab01`, or it will create and use a different `data` directory.
- If `./data` does not exist, the server creates it with mode `0755`. If it cannot (for
  example, if the current directory is not writable), it logs
  `Cannot create document root: ...` and exits with status 1.
- The server logs one line for each request (`Received request: GET /index.html`) and one
  for each error to standard error.
- The connection limit (10) and the timeout (5 seconds) are fixed in `main`. They cannot be
  set on the command line.

To stop the server, press Ctrl+C. The process exits at once and the operating system frees
the port. There is no graceful shutdown: requests in progress are cut off. Ctrl+Z only
suspends the process and keeps the port bound. If you pressed it, resume with `fg`, then
press Ctrl+C.

## Run with Docker or Podman

The `Dockerfile` compiles a static binary in a `golang:1.27.3` stage and copies it into an
`alpine` image. The working directory in the image is `/app`, so the document root is
`/app/data`. That directory is empty in the image.

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
HTTP/1.0 200 OK
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

Timeout. Each connection has a 5-second deadline:

```bash
sleep 7 | nc localhost 8080                                         # nothing sent: 408 Request Timeout after 5 s
(printf 'GET /hello.txt HTTP/1.1\r\n'; sleep 7) | nc localhost 8080   # headers never finished: 408 after 5 s
(printf 'POST /slow.txt HTTP/1.1\r\nContent-Length: 10\r\n\r\nabc'; sleep 7) | nc localhost 8080   # body incomplete: no response, connection closed after 5 s
```

`nc` exits when `sleep` ends, about 7 seconds after it starts. The response arrives after 5
seconds. If the client connects and closes without sending anything
(`nc localhost 8080 < /dev/null`), the server logs it and closes the connection without a
response. In the last case nothing is stored and no temporary file remains.

### Clean up

```bash
rm -f data/hello.txt data/page.html data/sample.jpg sample.jpg out.jpg
```

## Testing

### Functional checks

The curl and nc commands above cover each status code the server can return: `200`, `400`,
`404`, `408` and `501`. To check the connection limit, open 10 connections that send nothing,
then make an eleventh request:

```bash
for i in $(seq 10); do (sleep 8 | nc localhost 8080 > /dev/null) & done
sleep 0.5
curl -s -o /dev/null -w '%{http_code} after %{time_total}s\n' localhost:8080/missing.html
wait
```

The eleventh connection is established at once (the kernel accepts it into the listen
backlog). The server reads it only when a slot frees up, which happens when the 10 idle
connections time out. Expected output: a line like `404 after 4.498701s`. If you repeat the
test with 9 idle connections, the answer is immediate.

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
go build -race -o http_server .
./http_server 8080
```

In a second terminal, run `(cd scripts && bash atomic.sh)`. If the race detector finds a data
race, the server prints `WARNING: DATA RACE` to standard error. With the current code it
prints nothing beyond the normal request log. Rebuild without `-race` afterwards
(`go build -o http_server .`).

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
- **One deadline for the whole connection.** Right after `Accept`, `Server.handleConn` calls
  `conn.SetDeadline` with a 5-second deadline for reads and writes. It covers reading the
  request line, the headers and the body, and writing the response. A slow or idle client
  cannot hold a slot for more than about 5 seconds.
  - If the deadline expires before the headers have been parsed, the server extends the write
    deadline by 1 second and sends `408 Request Timeout`.
  - If it expires later, for example while a `POST` body is being received, no response can be
    sent anymore: the error is logged and the connection is closed.
- **Document root setup.** The root path is the single constant `docRoot` in `main.go`. `main`
  creates it with `os.MkdirAll` before the server starts, so a fresh clone works without
  manual setup. If it cannot be created, the server exits.
- **Startup cleanup of temporary files.** If the process stops during an upload, its
  `tmp-*` file stays on disk, because deferred cleanup does not run. Before the server starts
  accepting connections, `removeTempFiles` deletes these files from `docRoot` and logs how
  many it removed. It only deletes regular files named `tmp-` followed by digits, the form
  `os.CreateTemp` produces. A client file such as `tmp-notes.txt` is kept. Subdirectories are
  not scanned.
- **File permissions.** Every stored file gets mode `0644` (owner read/write, everyone
  read), whether it is new or replaces an existing file. `os.CreateTemp` would otherwise
  leave it at `0600`.
- **`POST` status.** A successful `POST` returns `200 OK` with the body
  `File stored successfully.`, for both new and replaced files. To tell the two cases apart,
  the server would have to check whether the file exists before the rename, and with
  concurrent uploads that check could be outdated by the time of the rename.
- **Path handling.** `Store.resolve` cleans the URL path with `filepath.Clean("/" + path)`
  and joins it to `docRoot`. `..` segments therefore stop at the root. Subdirectories of
  `data/` work if they exist. A `POST` to a missing subdirectory fails with `400`. The server
  never creates subdirectories.
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
`os.CreateTemp(dir, "tmp-*")`, copies the body into it with `io.Copy`, sets mode `0644` and
closes it, then replaces the target with `os.Rename`. It checks the error from closing the
file, because some write errors only appear then. If any step fails, a deferred `os.Remove`
deletes the temporary file and the target is left unchanged.

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

**Atomicity is not durability.** The rename makes the new version visible atomically, but the
data is not `fsync`ed. After an operating system crash or a power loss, the new file may be
empty or incomplete. Calling `Sync()` on the temporary file before closing it would prevent
this, at the cost of slower uploads.

The [atomicity test](#atomicity) checks this behaviour.

## Known limitations

- **No graceful shutdown.** Ctrl+C or `SIGTERM` ends the process at once, and requests in
  progress are cut off. Temporary files left in `data/` are removed at the next startup, but
  only from the top level of `data/`, not from its subdirectories.
- **No resource limits on transfers.** The server does not limit the size of a `POST` body, so
  a client can fill the disk. `Store.Get` reads each file into memory before sending it, so
  memory use grows with file size and with the number of concurrent `GET`s.
- **The 5-second deadline is fixed and covers the whole transfer.** A large upload or download
  over a slow link is cut off after 5 seconds. An upload cut off this way gets no response.
  Ten clients that connect and send nothing block all other clients for up to 5 seconds.
- **Atomic but not durable.** Stored files are not `fsync`ed. See [Atomic POST](#atomic-post).
- **Missing `Host` header is accepted.** HTTP/1.1 requires a `Host` header, but
  `http.ReadRequest` does not enforce it, and the server answers such requests normally.
- **Bonus part not implemented.** There is no pub-sub broker.
