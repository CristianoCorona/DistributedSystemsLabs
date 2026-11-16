package handlers

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"path"
	"slices"
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
    
    if (!isValidFileExt(req.URL.Path)) {
        setStatusCode(res, http.StatusBadRequest)
		setBodyTextContent(res, "Filetype not supported")
		res.Write(conn)
        return
    }

	switch req.Method {
	case http.MethodGet:
		HandleGetFileRequest(req, res)
	case http.MethodPost:
		HandlePostFileRequest(req, res)
	}

	res.Write(conn)
}

func setStatusCode(res *http.Response, code int) {
	res.StatusCode = code
	res.Status = http.StatusText(code)
}

func setBodyTextContent(res *http.Response, content string) {
	res.Header.Add("Content-Type", "text/plain")
	res.Body = io.NopCloser(strings.NewReader(content))
	res.ContentLength = int64(len(content))
}

func setBodyFileContent(res *http.Response, file io.ReadCloser) {
	res.Body = file
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

func isValidFileExt(pathName string) bool {
	ext := path.Ext(pathName)
    acceptedFileExt := []string{".jpg", ".jpeg", ".css", ".html", ".txt", ".gif"}
	return slices.Contains(acceptedFileExt, ext)
}