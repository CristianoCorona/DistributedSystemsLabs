# --- Stage 1: compile ---
FROM golang:1.27.3 AS build

WORKDIR /src

COPY . .

# CGO_ENABLED=0 produces a statically linked binary.
RUN CGO_ENABLED=0 GOOS=linux go build -o /http_server ./cmd/http_server

# --- Stage 2: run --- Smaller stage
FROM alpine:latest
WORKDIR /app
COPY --from=build /http_server .
RUN mkdir data
EXPOSE 8080
ENTRYPOINT ["./http_server"]
# argument (the port number is still configurable via the CMD instruction)
CMD ["8080"] 