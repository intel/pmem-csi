/*
Copyright 2020 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package exec

import (
	"strings"
	"testing"

	"k8s.io/klog/v2"
)

func init() {
	klog.InitFlags(nil)
}

func Test_Failure(t *testing.T) {
	output, err := RunCommand("no-such-binary", "foo")
	if err == nil {
		t.Errorf("did not get expected error")
	}
	if output != "" {
		t.Errorf("unexpected output: %q", output)
	}
}

func Test_BadArgs(t *testing.T) {
	badFile := "/no/such/file"
	output, err := RunCommand("ls", badFile)
	if err == nil {
		t.Errorf("did not get expected error")
	}
	if strings.Index(output, badFile) == -1 {
		t.Errorf("file name not in output: %q", output)
	}
}

func Test_Echo(t *testing.T) {
	output, err := RunCommand("echo", "hello", "world")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if strings.Index(output, "hello world") == -1 {
		t.Errorf("'hello world' not in output: %q", output)
	}
}

func Test_Lines(t *testing.T) {
	output, err := RunCommand("echo", "hello\nworld\n")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if strings.Index(output, "hello\nworld") == -1 {
		t.Errorf("'hello\nworld' not in output: %q", output)
	}
}

func Test_Error(t *testing.T) {
	output, err := RunCommand("sh", "-c", "echo hello world && false")
	if err == nil {
		t.Fatal("unexpected success")
	}
	t.Logf("got expected error: %v", err)
	if strings.Index(output, "hello world") == -1 {
		t.Errorf("'hello world' not in output: %q", output)
	}
	msg := err.Error()
	if strings.Index(msg, "Output: hello world") == -1 {
		t.Errorf("'Output: hello world' not in error: %v", err)
	}
}
