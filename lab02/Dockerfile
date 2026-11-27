# Build shared package stage
FROM golang:1.27.3-alpine AS shared-build-stage
WORKDIR /app
RUN apk add --no-cache gcc musl-dev
# COPY go.mod go.sum ./
COPY src/go.mod ./
RUN go mod download
COPY src ./src/
WORKDIR /app/src
RUN GOOS=linux go build -race -buildmode=plugin -o /wc.so ./mrapps/wc.go


# mrsequential build stage
FROM shared-build-stage AS sequential-build-stage
WORKDIR /app/src
RUN GOOS=linux go build -race -o /mrsequential ./main/mrsequential.go

# mrcoordinator build stage
FROM shared-build-stage AS coordinator-build-stage
WORKDIR /app/src
RUN GOOS=linux go build -race -o /mrcoordinator ./main/mrcoordinator.go

# mrworker build stage
FROM shared-build-stage AS worker-build-stage
WORKDIR /app/src
RUN GOOS=linux go build -race -o /mrworker ./main/mrworker.go


FROM golang:1.27.3-alpine AS shared-production-stage
WORKDIR /
COPY --from=shared-build-stage /wc.so /wc.so
COPY --from=shared-build-stage /app/src/main/pg*.txt /

FROM shared-production-stage AS sequential
COPY --from=sequential-build-stage /mrsequential /mrsequential
CMD ["sh", "-c", "./mrsequential wc.so pg*.txt && more mr-out-0"]

FROM shared-production-stage AS coordinator
COPY --from=coordinator-build-stage /mrcoordinator /mrcoordinator
CMD ["sh", "-c", "./mrcoordinator pg*.txt"]

FROM shared-production-stage AS worker
COPY --from=worker-build-stage /mrworker /mrworker
CMD ["sh", "-c", "ls && ./mrworker /wc.so"]

FROM shared-production-stage AS combined
COPY --from=coordinator /mrcoordinator /mrcoordinator
COPY --from=worker /mrworker /mrworker

ENV NR_WORKERS=3
CMD ["sh", "-c", "./mrcoordinator pg*.txt >> coordinator.log 2>&1 & for i in $(seq 1 $NR_WORKERS); do ./mrworker /wc.so >> worker_$i.log 2>&1 & done; wait && sleep infinity"]
# RUN chmod +x /mrsequential



# FROM build-stage AS run-test-stage
# RUN go test -v ./...