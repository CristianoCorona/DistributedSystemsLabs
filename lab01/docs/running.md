# Building and running

The [README](../README.md#build-and-run) has the short version. This page has the details.

## Locally

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

- The only argument is the TCP port, an integer from 1 to 65535. With anything other than
  exactly one argument, or with an invalid port, the program logs
  `Usage: ./http_server <port>` and exits with status 1.
- Files are read from and written to `./data`, relative to the **current working directory**.
  Start the server from `lab01`, or it will create and use a different `data` directory.
- If `./data` does not exist, the server creates it with mode `0755`. If it cannot (for
  example, if the current directory is not writable), it logs
  `Cannot create document root: ...` and exits with status 1.
- The server logs one line for each request (`Received request: GET /index.html`) and one
  for each error to standard error.
- The connection limit (10) and the timeouts (5 seconds for the headers, 60 seconds for the
  rest of the request) are fixed in `main`. They cannot be set on the command line. The
  broker's queue size (64) is also fixed in `main`, and the long-poll timeout (15 seconds) is
  the constant `pollTimeout` in `internal/app/pubsub.go`.

To stop the server, press Ctrl+C. The process exits at once and the operating system frees
the port. There is no graceful shutdown: requests in progress are cut off. Ctrl+Z only
suspends the process and keeps the port bound. If you pressed it, resume with `fg`, then
press Ctrl+C.

## With Docker or Podman

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
