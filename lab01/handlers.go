package main

import (
	"log"
	"net"
	"net/http"
)

func serveFile(conn net.Conn, req *http.Request) {
	//TODO: stub implementation
	log.Println("serveFile called.")
}

func storeFile(conn net.Conn, req *http.Request) {
	//TODO: stub implementation
	log.Println("storeFile called.")
}
