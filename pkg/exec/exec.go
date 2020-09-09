/*
Copyright 2020 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package exec

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"sync"

	"k8s.io/klog"
)

// RunCommand executes the command with logging through klog, with
// output processed line-by-line with the command path as prefix. It
// returns the combined output and, if there was a problem, includes
// that output and the command in the error.
func RunCommand(cmd string, args ...string) (string, error) {
	return Run(exec.Command(cmd, args...))
}

// Run does the same as RunCommand but takes a pre-populated
// cmd. Stdout and stderr are ignored and replaced with the output
// handling described for RunCommand.
func Run(cmd *exec.Cmd) (string, error) {
	klog.V(4).Infof("Executing: %s", cmd)

	r, w := io.Pipe()
	cmd.Stdout = w
	cmd.Stderr = w
	buffer := new(bytes.Buffer)
	r2 := io.TeeReader(r, buffer)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(r2)
		for scanner.Scan() {
			klog.V(5).Infof("%s: %s", cmd.Path, scanner.Text())
		}
	}()
	err := cmd.Run()
	w.Close()
	wg.Wait()
	klog.V(4).Infof("%s terminated, with %d bytes of output and error %v", cmd.Path, buffer.Len(), err)

	output := string(buffer.Bytes())
	if err != nil {
		err = fmt.Errorf("command %q failed: %v\nOutput: %s", cmd, err, output)
	}
	return output, err
}
