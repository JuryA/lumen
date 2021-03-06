package store

import (
	"encoding/json"
	"io/ioutil"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type fileEntry struct {
	Value     string    `json:"value"`
	NoExpire  bool      `json:"bool"`
	ExpiresOn time.Time `json:"expires_on"`
}

func (e fileEntry) expired() bool {
	return !e.NoExpire && time.Now().After(e.ExpiresOn)
}

type fileData struct {
	Version string               `json:"version"`
	Seq     uint64               `json:"seq"`
	Pairs   map[string]fileEntry `json:"pairs"`
}

func newFileData() *fileData {
	return &fileData{
		Version: "1",
		Seq:     0,
		Pairs:   make(map[string]fileEntry),
	}
}

// newFileDataFromFile tries to load data from fileName, creating a
// new file with empty data if it can't read it. Returns error if it
// can't parse an existing file, or reads invalid data.
func newFileDataFromFile(fileName string) (*fileData, error) {
	fileData := newFileData()

	logrus.WithFields(logrus.Fields{"type": "filestore", "method": "new"}).Debugf("reading file: %s", fileName)
	data, err := ioutil.ReadFile(fileName)
	if err != nil {
		logrus.WithFields(logrus.Fields{"type": "filestore", "method": "new"}).Debugf("read error: %v", err)
		logrus.WithFields(logrus.Fields{"type": "filestore", "method": "new"}).Infof("creating new file: %s", fileName)
		return fileData, fileData.sync(fileName)
	}

	err = json.Unmarshal(data, &fileData)

	if err != nil {
		logrus.WithFields(logrus.Fields{"type": "filestore", "method": "new"}).Errorf("parse error: %v", err)
		return nil, errors.Errorf("invalid content in %s: %v", fileName, err)
	}

	return fileData, nil
}

func (data *fileData) sync(fileName string) error {
	jsonData, err := json.Marshal(*data)

	if err != nil {
		logrus.WithFields(logrus.Fields{"type": "filestore", "method": "sync"}).Errorf("marshaling error: %v", err)
		return errors.Errorf("could not marshall json: %v", err)
	}

	logrus.WithFields(logrus.Fields{"type": "filestore", "method": "sync"}).Debugf("writing to file: %s", fileName)
	err = ioutil.WriteFile(fileName, jsonData, 0600)
	if err != nil {
		logrus.WithFields(logrus.Fields{"type": "filestore", "method": "sync"}).Errorf("write error: %v", err)
		return errors.Errorf("could not write to file: %v", err)
	}

	return nil
}

// DataStore represents the conntection to the Google Cloud Datastore.
type FileStore struct {
	*Store
	path string
	mu   *sync.RWMutex // protects data
	data *fileData
}

func NewFileStore(path string) (*FileStore, error) {
	fileData, err := newFileDataFromFile(path)

	if err != nil {
		return nil, errors.Wrap(err, "can't read or create file store")
	}

	// Try to connect
	fileStore := &FileStore{
		Store: &Store{
			driver:     "file",
			parameters: path,
		},
		path: path,
		mu:   &sync.RWMutex{},
		data: fileData,
	}
	return fileStore, nil
}

// sync must be called under mu
func (fs *FileStore) sync() error {
	return fs.data.sync(fs.path)
}

func (fs *FileStore) Set(k string, v string, ttl time.Duration) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	logrus.WithFields(logrus.Fields{"type": "filestore", "method": "set", "key": k}).Debugf("writing val: %s (ttl: %v)", v, ttl)
	fs.data.Pairs[k] = fileEntry{
		Value:     v,
		NoExpire:  ttl == 0,
		ExpiresOn: time.Now().Add(ttl),
	}

	fs.data.Seq++
	return fs.sync()
}

func (fs *FileStore) Get(k string) (string, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	val, ok := fs.data.Pairs[k]
	if !ok || val.expired() {
		logrus.WithFields(logrus.Fields{"type": "filestore", "method": "get", "key": k}).Debugf("not found, expired: %v", ok && val.expired())
		return "", errors.Errorf("not found: %s", k)
	}

	logrus.WithFields(logrus.Fields{"type": "filestore", "method": "get", "key": k}).Debugf("got val: %s (expires: %v, expires_on: %v)", val.Value, !val.NoExpire, val.ExpiresOn)
	return val.Value, nil
}

func (fs *FileStore) Delete(k string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	logrus.WithFields(logrus.Fields{"type": "filestore", "method": "delete", "key": k}).Debugf("deleting")
	delete(fs.data.Pairs, k)
	return fs.sync()
}
