package handlers

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
)

func HandleConnection(conn net.Conn) {
	defer conn.Close()
    res := createResponse()
	req, err := http.ReadRequest(bufio.NewReader(conn))
    if err != nil {
        setStatusCode(res, http.StatusBadRequest)
		setBodyTextContent(res, "Invalid request")
        res.Write(conn)
        return
    }

	switch req.Method {
	case http.MethodGet:
		HandleGetImageRequest(req, res)
	case http.MethodPost:
		HandlePostImageRequest(req, res)
	}

	res.Write(conn)
}

func setStatusCode(res *http.Response, code int) {
	res.StatusCode = code
	res.Status = http.StatusText(code)
}

func setBodyTextContent(res *http.Response, content string) {
	res.Body = io.NopCloser(strings.NewReader(content))
	res.ContentLength = int64(len(content))
}

func setBodyImageContent(res *http.Response, image *os.File) {
	res.Body = image
	res.ContentLength = -1
}

func createResponse() *http.Response {
	res := &http.Response{
        Proto:         "HTTP/1.1",
        ProtoMajor:    1,
        ProtoMinor:    1,
        Header:        make(http.Header),
    }
    res.Header.Set("Connection", "close")
	return res
}