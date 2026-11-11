package main

import (
	"net"

	"github.com/CristianoCorona/DistributedSystemsLabs/lab01/src/internal/handlers"
)

func main() {
    var PORT string = "8080"
    ln, err := net.Listen("tcp", ":" + PORT)
    if err != nil {
        // handle error
    }
    defer ln.Close()
    for {
        conn, err := ln.Accept() // TODO: only allow 10 simultaneous connections
        if err != nil {
            // handle error
        }
        go handlers.HandleConnection(conn)
    }
}