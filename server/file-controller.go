package server

import (
	"bytes"
	"encoding/json"
	"file-service-go/service"
	"github.com/gorilla/mux"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
)

var serverUrl = os.Getenv("SERVER_URL")

func init() {
	router := mux.NewRouter()
	fileSubrouter := router.PathPrefix("/file").Subrouter()
	fileSubrouter.HandleFunc("/{id:[0-9]+}/info", fileInfo)
	fileSubrouter.HandleFunc("/{id:[0-9]+}/download", downloadFile)
	fileSubrouter.HandleFunc("/upload", uploadFile)

	http.Handle("/file/", router)
}


func fileInfo(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, _ := strconv.Atoi(vars["id"])

	var sfm, err = service.LoadFileMetadata(id)

	if err != nil {
		log.Printf("Error during loading file metadata: %v", err)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("404 - File metadata not found!"))
		return
	}

	var fm = FileMetadata{Id:sfm.Id, Name:sfm.Name, FileType:sfm.FileType,
		Description:sfm.Description, Size:sfm.Size, Url:resolveUrl(sfm.Id)}

	w.Header().Set("Content-Type", "application/json")
	js, _ := json.Marshal(fm)
	_, _ = w.Write(js)

}

func downloadFile(w http.ResponseWriter, r *http.Request)  {
	vars := mux.Vars(r)
	id, _ := strconv.Atoi(vars["id"])

	data, errFile := service.ReadFile(id)
	var sfm, errMeta = service.LoadFileMetadata(id)

	if errFile != nil || errMeta != nil {
		log.Printf("Error during loading file. Metadata: %v, File: %v", errMeta, errFile)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("404 - File not found!"))
		return
	}

	w.Header().Set("Content-Type", sfm.FileType)
	_, _ = w.Write(data)
}

func uploadFile(w http.ResponseWriter, r *http.Request)  {

	var Buf bytes.Buffer
	file, header, errFileForm := r.FormFile("file")
	if errFileForm != nil {
		log.Printf("File was not passed: %v", errFileForm)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("400 - Bad request, file must be specified!"))
		return
	}

	var contentType = header.Header.Get("Content-Type")
	var name = header.Filename
	var size = header.Size
	var description = r.FormValue("description")

	defer file.Close()
	_, _ = io.Copy(&Buf, file)
	sfm := service.StoredFileMetadata{Name: name, Description: description, Size: size, FileType: contentType}

	res, errMeta := service.SaveFileMetadata(sfm)
	if errMeta != nil {
		log.Printf("Can't save metadata: %v", errMeta)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("400 - Bad request!"))
		return
	}

	errFile := service.SaveFile(Buf.Bytes(), res.Id)
	if errFile != nil {
		log.Printf("Error during save file: %v", errFile)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("500 - Can't save the file!"))
		return
	}
	_, _ = w.Write([]byte(strconv.Itoa(res.Id)))
}


func resolveUrl(id int) string {
	var url = serverUrl + "/file/" + strconv.Itoa(id) + "/download"
	return url
}

type FileMetadata struct {
	Id int `json:"id"`
	Name string `json:"name"`
	FileType string `json:"type"`
	Size int64 `json:"size"`
	Description string `json:"description"`
	Url string `json:"url"`
}