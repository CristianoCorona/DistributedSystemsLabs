package handlers

import (
	"bytes"
	"io"
	"mime"
	"net/http"
	"path"

	"github.com/CristianoCorona/DistributedSystemsLabs/lab01/src/internal/storage"
)

func HandlePostFileRequest(req *http.Request, res *http.Response) *http.Response {
	defer req.Body.Close()
	
	if err := storage.SaveFile(req.URL.Path, req.Body); err != nil {
		setStatusCode(res, http.StatusInternalServerError)
		return res
	}
	setStatusCode(res, http.StatusCreated)
	setBodyTextContent(res, "File saved")
	return res
}


func HandleGetFileRequest(req *http.Request, res *http.Response) *http.Response {
    defer req.Body.Close()

	file := &bytes.Buffer{}
	if err := storage.GetFile(req.URL.Path, file); err != nil {
		setStatusCode(res, http.StatusNotFound)
		setBodyTextContent(res, "File was not found")
		return res
	}
	
    setStatusCode(res, http.StatusOK)
	res.Header.Add("Content-Type", mime.TypeByExtension(path.Ext(req.URL.Path)))
	setBodyFileContent(res, io.NopCloser(file))

    return res
}