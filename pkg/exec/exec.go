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

	"k8s.io/klog/v2"
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
	r2, w2 := io.Pipe()
	cmd.Stdout = w
	cmd.Stderr = w2
	var stdout, both bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(2)
	// Collect stdout and stderr separately. Storing in the
	// combined buffer is a bit racy, but we need to know which
	// output is stdout.
	go dumpOutput(&wg, r, []io.Writer{&stdout, &both}, cmd.Path+": stdout: ")
	go dumpOutput(&wg, r2, []io.Writer{&both}, cmd.Path+": stderr: ")
	err := cmd.Run()
	w.Close()
	w2.Close()
	wg.Wait()
	klog.V(4).Infof("%s terminated, with %d bytes of stdout, %d of combined output and error %v", cmd.Path, stdout.Len(), both.Len(), err)

	switch {
	case err != nil && both.Len() > 0:
		err = fmt.Errorf("%q: command failed: %v\nCombined stderr/stdout output: %s", cmd, err, string(both.Bytes()))
	case err != nil:
		err = fmt.Errorf("%q: command failed with no output: %v", cmd, err)
	}
	return string(stdout.Bytes()), err
}

func dumpOutput(wg *sync.WaitGroup, in io.Reader, out []io.Writer, prefix string) {
	defer wg.Done()
	scanner := bufio.NewScanner(in)
	for scanner.Scan() {
		for _, o := range out {
			o.Write(scanner.Bytes())
			o.Write([]byte("\n"))
		}
		klog.V(5).Infof("%s%s", prefix, scanner.Text())
	}
}

// CmdResult always returns an informative description of what command ran and what the
// outcome (stdout+stderr, exit code if any) was. Logging is left entirely to the caller.
func CmdResult(cmd string, args ...string) string {
	c := exec.Command(cmd, args...)
	output, err := c.CombinedOutput()
	result := fmt.Sprintf("%q:", c)
	switch {
	case err != nil && len(output) == 0:
		result += fmt.Sprintf(" command failed with no output: %v", err)
	case err != nil:
		result += fmt.Sprintf(" command failed: %v\nOutput:\n%s", err, string(output))
	case len(output) > 0:
		result += fmt.Sprintf("\n%s", output)
	default:
		result += " no output"
	}
	return result
}
