/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package driver

import (
	"encoding/json"
	"fmt"
	"strings"

	v1 "k8s.io/api/core/v1"
	storageframework "k8s.io/kubernetes/test/e2e/storage/framework"
)

// StorageClassParameters can be used in combination with DynamicDriver to implement test patterns
// that encode additional parameters in the test pattern name. This is a workaround for the
// fixed content of the original test pattern struct.
type StorageClassParameters struct {
	FSType     string
	Parameters map[string]string
}

func (scp *StorageClassParameters) Encode() (string, error) {
	data, err := json.Marshal(scp)
	return string(data), err
}

func (scp *StorageClassParameters) MustEncode() string {
	data, err := scp.Encode()
	if err != nil {
		panic(err)
	}
	return data
}

func (scp *StorageClassParameters) Decode(parameters string) error {
	return json.Unmarshal([]byte(parameters), scp)
}

func EncodeTestPatternName(volType storageframework.TestVolType, volMode v1.PersistentVolumeMode, scp StorageClassParameters) string {
	return fmt.Sprintf("%s %s %s", volType, volMode, scp.MustEncode())
}

func DecodeTestPatternName(name string) (volType storageframework.TestVolType, volMode v1.PersistentVolumeMode, scp *StorageClassParameters, err error) {
	parts := strings.SplitN(name, " ", 3)
	if len(parts) != 3 {
		err = fmt.Errorf("not of format '<vol type> <vol mode> {<parameters>}': %s", name)
		return
	}
	scp = &StorageClassParameters{}
	volType = storageframework.TestVolType(parts[0])
	volMode = v1.PersistentVolumeMode(parts[1])
	err = scp.Decode(parts[2])
	return
}
