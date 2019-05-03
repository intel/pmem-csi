/*
Copyright 2019 Intel Coporation.

SPDX-License-Identifier: Apache-2.0
*/

package pmemcommon

import (
	"fmt"
	"io/ioutil"
	"os"
)

func ExitError(msg string, e error) {
	str := msg + ": " + e.Error()
	fmt.Println(str)
	terminationMsgPath := os.Getenv("TERMINATION_LOG_PATH")
	if terminationMsgPath != "" {
		err := ioutil.WriteFile(terminationMsgPath, []byte(str), os.FileMode(0644))
		if err != nil {
			fmt.Println("Can not create termination log file:" + terminationMsgPath)
		}
	}
}
