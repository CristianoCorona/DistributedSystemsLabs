package main

import (
	"net"
	"sync"

	"github.com/CristianoCorona/DistributedSystemsLabs/lab01/src/internal/handlers"
)

func main() {
    var PORT string = "8080"
    ln, err := net.Listen("tcp", ":" + PORT)
    if err != nil {
        // handle error
    }
    defer ln.Close()
    lock := &sync.Mutex{}
    cond := sync.NewCond(lock)
    workerCount := 0
    for {
        conn, err := ln.Accept()
        if err != nil {
            // handle error
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