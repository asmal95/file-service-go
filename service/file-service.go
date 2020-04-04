package service

import (
	"github.com/recoilme/pudge"
	uuid "github.com/satori/go.uuid"
	"io/ioutil"
	"os"
	"sync"
)

var path = os.Getenv("FILES_DIRECTORY")

var idMutex sync.Mutex

func SaveFile(data []byte, id string) error {
	err := ioutil.WriteFile(path+"/"+id, data, 0644)
	return err
}

func ReadFile(id string) ([]byte, error) {
	data, err := ioutil.ReadFile(path + "/" + id)
	return data, err
}

func nextId() string {
	id := uuid.NewV4()
	return id.String()
}

func LoadFileMetadata(id string) (StoredFileMetadata, error) {
	var fm StoredFileMetadata
	err := pudge.Get(path+"/metadata.df", id, &fm)
	return fm, err
}

func SaveFileMetadata(fm StoredFileMetadata) (StoredFileMetadata, error) {
	if fm.Id == "" {
		fm.Id = nextId()
	}
	err := pudge.Set(path+"/metadata.df", fm.Id, &fm)
	return fm, err
}

type StoredFileMetadata struct {
	Id          string `json:"id"`
	Name        string `json:"name"`
	FileType    string `json:"type"`
	Size        int64  `json:"size"`
	Description string `json:"description"`
}
