FROM golang:1.27.3 AS build-stage

WORKDIR /app

# COPY go.mod go.sum ./
COPY go.mod ./
RUN go mod download

COPY src ./src/

RUN ls

RUN CGO_ENABLED=0 GOOS=linux go build -o /http_server ./src/cmd/server

# FROM build-stage AS run-test-stage
# RUN go test -v ./...

FROM golang:1.27.3-alpine AS production-stage

WORKDIR /

COPY --from=build-stage /http_server /http_server

EXPOSE 8080

CMD ["sh", "-c", "/http_server ${PORT}"]