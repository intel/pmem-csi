/*
Copyright 2019 The Kubernetes Authors.
Copyright 2020 Intel Coporation.

SPDX-License-Identifier: Apache-2.0
*/

package klogr

import (
	"bytes"
	"errors"
	"flag"
	"testing"

	"k8s.io/klog/v2"

	"github.com/go-logr/logr"
)

func TestInfo(t *testing.T) {
	klog.InitFlags(nil)
	flag.CommandLine.Set("v", "10")
	flag.CommandLine.Set("skip_headers", "true")
	flag.CommandLine.Set("logtostderr", "false")
	flag.CommandLine.Set("alsologtostderr", "false")
	flag.CommandLine.Set("stderrthreshold", "10")
	flag.Parse()

	tests := map[string]struct {
		klogr          logr.Logger
		text           string
		keysAndValues  []interface{}
		names          []string
		err            error
		expectedOutput string
	}{
		"should log with values passed to keysAndValues": {
			klogr:         New().V(0),
			text:          "test",
			keysAndValues: []interface{}{"akey", "avalue"},
			expectedOutput: `test akey="avalue"
`,
		},
		"should support single name": {
			klogr:         New().V(0),
			names:         []string{"hello"},
			text:          "test",
			keysAndValues: []interface{}{"akey", "avalue"},
			expectedOutput: `hello: test akey="avalue"
`,
		},
		"should support multiple names": {
			klogr:         New().V(0),
			names:         []string{"hello", "world"},
			text:          "test",
			keysAndValues: []interface{}{"akey", "avalue"},
			expectedOutput: `hello/world: test akey="avalue"
`,
		},
		"should not print duplicate keys with the same value": {
			klogr:         New().V(0),
			text:          "test",
			keysAndValues: []interface{}{"akey", "avalue", "akey", "avalue"},
			expectedOutput: `test akey="avalue"
`,
		},
		"should only print the last duplicate key when the values are passed to Info": {
			klogr:         New().V(0),
			text:          "test",
			keysAndValues: []interface{}{"akey", "avalue", "akey", "avalue2"},
			expectedOutput: `test akey="avalue2"
`,
		},
		"should only print the duplicate key that is passed to Info if one was passed to the logger": {
			klogr:         New().WithValues("akey", "avalue"),
			text:          "test",
			keysAndValues: []interface{}{"akey", "avalue"},
			expectedOutput: `test akey="avalue"
`,
		},
		"should only print the key passed to Info when one is already set on the logger": {
			klogr:         New().WithValues("akey", "avalue"),
			text:          "test",
			keysAndValues: []interface{}{"akey", "avalue2"},
			expectedOutput: `test akey="avalue2"
`,
		},
		"should correctly handle odd-numbers of KVs": {
			text:          "test",
			keysAndValues: []interface{}{"akey", "avalue", "akey2"},
			expectedOutput: `test akey="avalue" akey2=<nil>
`,
		},
		"should correctly html characters": {
			text:          "test",
			keysAndValues: []interface{}{"akey", "<&>"},
			expectedOutput: `test akey="<&>"
`,
		},
		"should correctly handle odd-numbers of KVs in both log values and Info args": {
			klogr:         New().WithValues("basekey1", "basevar1", "basekey2"),
			text:          "test",
			keysAndValues: []interface{}{"akey", "avalue", "akey2"},
			expectedOutput: `test basekey1="basevar1" basekey2=<nil> akey="avalue" akey2=<nil>
`,
		},
		"should correctly print regular error types": {
			klogr:         New().V(0),
			text:          "test",
			keysAndValues: []interface{}{"err", errors.New("whoops")},
			expectedOutput: `test err="whoops"
`,
		},
		"should correctly print regular error types when using logr.Error": {
			klogr: New().V(0),
			text:  "test",
			err:   errors.New("whoops"),
			// The message is printed to three different log files (info, warning, error), so we see it three times in our output buffer.
			expectedOutput: `test err="whoops"
test err="whoops"
test err="whoops"
`,
		},
	}
	for n, test := range tests {
		t.Run(n, func(t *testing.T) {
			klogr := test.klogr
			if klogr == nil {
				klogr = New()
			}
			for _, name := range test.names {
				klogr = klogr.WithName(name)
			}

			// hijack the klog output
			tmpWriteBuffer := bytes.NewBuffer(nil)
			klog.SetOutput(tmpWriteBuffer)

			if test.err != nil {
				klogr.Error(test.err, test.text, test.keysAndValues...)
			} else {
				klogr.Info(test.text, test.keysAndValues...)
			}
			// call Flush to ensure the text isn't still buffered
			klog.Flush()

			actual := tmpWriteBuffer.String()
			if actual != test.expectedOutput {
				t.Errorf("expected %q did not match actual %q", test.expectedOutput, actual)
			}
		})
	}
}
