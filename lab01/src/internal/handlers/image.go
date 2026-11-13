package handlers

import (
	"net/http"
	"os"

	"github.com/CristianoCorona/DistributedSystemsLabs/lab01/src/internal/storage"
)

func HandlePostImageRequest(req *http.Request, res *http.Response) *http.Response {
	defer req.Body.Close()
	
	if err := storage.SaveImage(req.URL.Path, req.Body); err != nil {
		setStatusCode(res, http.StatusInternalServerError)
		return res
	}
	setStatusCode(res, http.StatusCreated)
	setBodyTextContent(res, "File saved")
	return res
}


func HandleGetImageRequest(req *http.Request, res *http.Response) *http.Response {
    defer req.Body.Close()

	file := &os.File{}
	if err := storage.GetImage(req.URL.Path, file); err != nil {
		setStatusCode(res, http.StatusNotFound)
		setBodyTextContent(res, "File was not found")
		return res
	}

    setStatusCode(res, http.StatusOK)
	setBodyImageContent(res, file)

    return res
}