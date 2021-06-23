/*
Copyright 2020 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package exec

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	pmemlog "github.com/intel/pmem-csi/pkg/logger"
	"github.com/intel/pmem-csi/pkg/logger/testinglogger"
)

var testcases = map[string]struct {
	cmd []string

	expectedError  string
	expectedOutput string
	expectedResult string
}{
	"no-such-binary": {
		cmd:            []string{"no-such-binary", "foo"},
		expectedError:  `"no-such-binary foo": command failed with no output: exec: "no-such-binary": executable file not found in $PATH`,
		expectedResult: `"no-such-binary foo": command failed with no output: exec: "no-such-binary": executable file not found in $PATH`,
	},
	"bad-args": {
		cmd: []string{"ls", "/no/such/file"},
		expectedError: `"/bin/ls /no/such/file": command failed: exit status 2
Combined stderr/stdout output: ls: cannot access '/no/such/file': No such file or directory
`,
		expectedResult: `"/bin/ls /no/such/file": command failed: exit status 2
Output:
ls: cannot access '/no/such/file': No such file or directory
`,
	},
	"stdout": {
		cmd: []string{"echo", "hello", "world"},
		expectedOutput: `hello world
`,
		expectedResult: `"/bin/echo hello world":
hello world
`,
	},
	"stderr": {
		cmd: []string{"sh", "-c", "echo >&2 hello world"},
		expectedResult: `"/bin/sh -c echo >&2 hello world":
hello world
`,
	},
	"both": {
		cmd: []string{"sh", "-c", "echo >&2 hello; echo world"},
		expectedOutput: `world
`,
		expectedResult: `"/bin/sh -c echo >&2 hello; echo world":
hello
world
`,
	},
	"stdout-with-error": {
		cmd: []string{"sh", "-c", "echo hello world; exit 1"},
		expectedOutput: `hello world
`,
		expectedError: `"/bin/sh -c echo hello world; exit 1": command failed: exit status 1
Combined stderr/stdout output: hello world
`,
		expectedResult: `"/bin/sh -c echo hello world; exit 1": command failed: exit status 1
Output:
hello world
`,
	},
	"stderr-with-error": {
		cmd: []string{"sh", "-c", "echo >&2 hello world; exit 1"},
		expectedError: `"/bin/sh -c echo >&2 hello world; exit 1": command failed: exit status 1
Combined stderr/stdout output: hello world
`,
		expectedResult: `"/bin/sh -c echo >&2 hello world; exit 1": command failed: exit status 1
Output:
hello world
`,
	},
	// This test case cannot be run reliably because ordering of stdout/stderr lines is random.
	//
	// 	"both-with-error": {
	// 		cmd: []string{"sh", "-c", "echo >&2 hello; echo world; exit 1"},
	// 		expectedOutput: `world
	// `,
	// 		expectedError: `"/bin/sh -c echo >&2 hello; echo world; exit 1": command failed: exit status 1
	// Combined stderr/stdout output: world
	// hello
	// `,
	// 		expectedResult: `"/bin/sh -c echo >&2 hello; echo world; exit 1": command failed: exit status 1
	// Output:
	// hello
	// world
	// `,
	// },
}

func TestRun(t *testing.T) {
	for name, tc := range testcases {
		t.Run(name, func(t *testing.T) {
			ctx := pmemlog.Set(context.Background(), testinglogger.New(t))
			output, err := RunCommand(ctx, tc.cmd[0], tc.cmd[1:]...)
			assert.Equal(t, tc.expectedOutput, output, "output")
			errStr := ""
			if err != nil {
				errStr = err.Error()
			}
			assert.Equal(t, tc.expectedError, errStr, "error")
		})
	}
}

func TestResult(t *testing.T) {
	for name, tc := range testcases {
		t.Run(name, func(t *testing.T) {
			result := CmdResult(tc.cmd[0], tc.cmd[1:]...)
			assert.Equal(t, tc.expectedResult, result)
		})
	}
}
