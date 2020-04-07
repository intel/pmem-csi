/*
Copyright 2019  Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/
package pmemstate

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/pkg/errors"
	"k8s.io/klog"
)

// StateManager manages the driver persistent state, i.e, volumes information
type StateManager interface {
	// Create creates an entry in the state with given id and data
	Create(id string, data interface{}) error
	// Delete deletes an entry found with the id from the state
	Delete(id string) error
	// Get retrives the entry data into location pointed by dataPtr.
	Get(id string, dataPtr interface{}) error
	// GetAll retrieves ids of all entries found in the state
	GetAll() ([]string, error)
}

// fileState Persists the state information into a file.
// This is is supposed to use by Nodes to persists the state.
type fileState struct {
	location string
	// lock holds read-write lock
	lock sync.RWMutex
	// stateDirLock holds lock on state directory
	stateDirLock sync.Mutex
}

var _ StateManager = &fileState{}

// NewFileState instantiates the file state manager with given directory
// location. It ensures the provided directory exists.
// Returns error, if fails to create the directory in case of not pre-existing.
func NewFileState(directory string) (StateManager, error) {
	if err := ensureLocation(directory); err != nil {
		return nil, err
	}

	return &fileState{
		location: directory,
	}, nil
}

// Create saves the volume metadata to file named <id>.json
func (fs *fileState) Create(id string, data interface{}) error {
	fs.lock.Lock()
	defer fs.lock.Unlock()

	file := path.Join(fs.location, id+".json")
	// Create new file for synchronous writes
	fp, err := os.OpenFile(file, os.O_WRONLY|os.O_SYNC|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.Wrapf(err, "file-state: failed to create metadata storage file %s", file)
	}

	if err := json.NewEncoder(fp).Encode(data); err != nil {
		// cleanup file entry before returning error
		fp.Close() //nolint: errcheck, gosec
		if e := os.Remove(file); e != nil {
			klog.Warningf("file-state: fail to remove file %s: %s", file, e.Error())
		}
		return errors.Wrap(err, "file-state: failed to encode metadata")
	}

	if err := fp.Close(); err != nil {
		return errors.Wrapf(err, "file-state: failed to close metadata storage file %s", file)
	}

	return fs.syncStateDir()
}

// Delete deletes the metadata file saved for given volume id
func (fs *fileState) Delete(id string) error {
	fs.lock.Lock()
	defer fs.lock.Unlock()

	file := path.Join(fs.location, id+".json")
	if err := os.Remove(file); err != nil && err != os.ErrNotExist {
		return errors.Wrapf(err, "file-state: failed to delete file %s", file)
	}

	return fs.syncStateDir()
}

// Get retrieves metadata for given volume id to pointer location of dataPtr
func (fs *fileState) Get(id string, dataPtr interface{}) error {
	return fs.readFileData(path.Join(fs.location, id+".json"), dataPtr)
}

// GetAll retrieves the names of all .json files found in fileState.location directory
func (fs *fileState) GetAll() ([]string, error) {
	fs.stateDirLock.Lock()
	files, err := ioutil.ReadDir(fs.location)
	fs.stateDirLock.Unlock()
	if err != nil {
		return nil, errors.Wrapf(err, "file-state: failed to read metadata from %s", fs.location)
	}

	ids := []string{}
	for _, fileInfo := range files {
		fileName := fileInfo.Name()
		if !strings.HasSuffix(fileName, ".json") {
			continue
		}

		ids = append(ids, fileName[0:len(fileName)-len(".json")])
	}

	return ids, nil
}

func ensureLocation(directory string) error {
	info, err := os.Stat(directory)
	if err != nil {
		if os.IsNotExist(err) {
			err = os.Mkdir(directory, 0750)
		}
	} else if !info.IsDir() {
		err = fmt.Errorf("State location(%s) must be a directory", directory)
	}

	return err
}

func (fs *fileState) readFileData(file string, dataPtr interface{}) error {
	fs.lock.RLock()
	defer fs.lock.RUnlock()

	fp, err := os.OpenFile(file, os.O_RDONLY|os.O_SYNC, 0) //nolint: gosec
	if err != nil {
		return errors.Wrapf(err, "file-state: failed to open file %s", file)
	}
	defer fp.Close() //nolint: errcheck

	if err := json.NewDecoder(fp).Decode(dataPtr); err != nil {
		return errors.Wrapf(err, "file-state: failed to decode metadata from file %s", file)
	}

	return nil
}

func (fs *fileState) syncStateDir() error {
	var rErr error
	fs.stateDirLock.Lock()
	defer fs.stateDirLock.Unlock()

	if fp, err := os.Open(fs.location); err != nil {
		rErr = errors.Wrap(err, "file-state: failed to open state directory for syncing")
	} else if err := fp.Sync(); err != nil {
		fp.Close() //nolint: errcheck
		rErr = errors.Wrap(err, "file-state: fsync failure on state directroy")
	} else if err := fp.Close(); err != nil {
		rErr = errors.Wrap(err, "file-state: failed to close state directory after sync")
	}

	return rErr
}
