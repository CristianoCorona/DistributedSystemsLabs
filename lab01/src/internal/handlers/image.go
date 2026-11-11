package handlers

import (
	"net/http"
	"os"
	"slices"
	"strings"

	"tda596/grp74/lab1/src/internal/storage"
)

func HandlePostImageRequest(req *http.Request, res *http.Response) {
	setStatusCode(res, http.StatusNotImplemented) // TODO: implement POST request handling
}


func HandleGetImageRequest(req *http.Request, res *http.Response) *http.Response {
    defer req.Body.Close()
	
    pathSlice := strings.Split(req.URL.Path, ".")
    fileEnding := pathSlice[len(pathSlice) - 1]
    acceptedFileEndings := []string{"jpg", "jpeg", "css"}
    if (!slices.Contains(acceptedFileEndings, fileEnding)) {
        setStatusCode(res, http.StatusBadRequest)
		setBodyTextContent(res, "Filetype not supported")
        return res
    }


	file := &os.File{}
	if err := storage.GetImage(req.URL.Path, file); err != nil {
		if err == os.ErrNotExist {
			setStatusCode(res, http.StatusNotFound)
			setBodyTextContent(res, "File was not found")
			return res
		}
	}

    setStatusCode(res, http.StatusOK)
	setBodyImageContent(res, file)

    return res
}