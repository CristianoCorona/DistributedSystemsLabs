package main

import (
	"log"
	"net"
	"os"
	"strconv"
	"sync"

	"github.com/CristianoCorona/DistributedSystemsLabs/lab01/src/internal/handlers"
)

func main() {
    if len(os.Args) < 2 {
        log.Fatal("You need to specify port number as the first command-line argument")
    }
    port := os.Args[1]
    if _, err := strconv.Atoi(port); err != nil {
        log.Fatalf("Invalid port number %v", port)
    }
    ln, err := net.Listen("tcp", ":" + port)
    if err != nil {
        log.Fatalf("Could not open listening socket using TCP on port %s", port)
    }
    defer ln.Close()
    lock := &sync.Mutex{}
    cond := sync.NewCond(lock)
    workerCount := 0
    for {
        conn, err := ln.Accept()
        if err != nil {
            log.Default().Printf("Could not accrpt incomming connection %v", conn)
        }
        go func () {
            cond.L.Lock()
            for workerCount >= 10 {
                cond.Wait()
            }
            workerCount++
            cond.L.Unlock()
            
            handlers.HandleConnection(conn)

            cond.L.Lock()
            workerCount--
            cond.L.Unlock()
            cond.Signal()
        }()
    }
}