package service

import (
	"github.com/recoilme/pudge"
	"io/ioutil"
	"os"
	"strconv"
	"sync"
)

var path = os.Getenv("FILES_DIRECTORY")

var idMutex sync.Mutex

func SaveFile(data []byte, id int) error {
	err := ioutil.WriteFile(path+"/"+strconv.Itoa(id), data, 0644)
	return err
}

func ReadFile(id int) ([]byte, error) {
	data, err := ioutil.ReadFile(path + "/" + strconv.Itoa(id))
	return data, err
}

func nextId() int {
	idMutex.Lock()
	defer idMutex.Unlock()

	var id int
	_ = pudge.Get(path + "/metadata.df", "last_id", &id)
	id++
	_ = pudge.Set(path + "/metadata.df", "last_id", id)

	return id
}

func LoadFileMetadata(id int) (StoredFileMetadata, error) {
	var fm StoredFileMetadata
	err := pudge.Get(path + "/metadata.df", id, &fm)
	return fm, err
}

func SaveFileMetadata(fm StoredFileMetadata) (StoredFileMetadata, error) {
	if fm.Id == 0 {
		fm.Id = nextId()
	}
	err := pudge.Set(path + "/metadata.df", fm.Id, &fm)
	return fm, err
}

type StoredFileMetadata struct {
	Id int `json:"id"`
	Name string `json:"name"`
	FileType string `json:"type"`
	Size int64 `json:"size"`
	Description string `json:"description"`
}
