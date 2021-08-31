/*
Copyright 2020 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package exec

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"

	pmemlog "github.com/intel/pmem-csi/pkg/logger"
)

// RunCommand executes the command with logging through klog, with
// output processed line-by-line with the command path as prefix. It
// returns the combined output and, if there was a problem, includes
// that output and the command in the error.
func RunCommand(ctx context.Context, cmd string, args ...string) (string, error) {
	return Run(ctx, exec.Command(cmd, args...))
}

// Run does the same as RunCommand but takes a pre-populated
// cmd. Stdout and stderr are ignored and replaced with the output
// handling described for RunCommand.
func Run(ctx context.Context, cmd *exec.Cmd) (string, error) {
	logger := pmemlog.Get(ctx).WithValues("command", cmd.Path)
	logger.V(4).Info("Starting command", "args", cmd.Args)

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
	go dumpOutput(pmemlog.Set(ctx, logger.WithName("stdout")), &wg, r, []io.Writer{&stdout, &both})
	go dumpOutput(pmemlog.Set(ctx, logger.WithName("stderr")), &wg, r2, []io.Writer{&both})
	err := cmd.Run()
	w.Close()
	w2.Close()
	wg.Wait()
	logger.V(4).Info("Command terminated", "stdout-len", stdout.Len(), "combined-len", both.Len(), "error", err)

	switch {
	case err != nil && both.Len() > 0:
		err = fmt.Errorf("%q: command failed: %v\nCombined stderr/stdout output: %s", cmd, err, both.String())
	case err != nil:
		err = fmt.Errorf("%q: command failed with no output: %v", cmd, err)
	}
	return stdout.String(), err
}

func dumpOutput(ctx context.Context, wg *sync.WaitGroup, in io.Reader, out []io.Writer) {
	logger := pmemlog.Get(ctx)
	defer wg.Done()
	scanner := bufio.NewScanner(in)
	for scanner.Scan() {
		for _, o := range out {
			_, _ = o.Write(scanner.Bytes())
			_, _ = o.Write([]byte("\n"))
		}
		logger.V(5).Info(scanner.Text())
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
