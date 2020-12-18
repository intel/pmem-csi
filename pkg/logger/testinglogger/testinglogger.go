/*
Copyright 2019 The Kubernetes Authors.
Copyright 2020 Intel Coporation.

SPDX-License-Identifier: Apache-2.0
*/

// Package testinglogger contains an implementation of the logr interface
// which is logging through a function like testing.TB.Log function.
// Serialization of the structured log parameters is done in the format
// used also by klog/klogr.
//
// The code had to be copied because:
// - klog/klogr always prints to the global klog
// - go-logr/stdr (a very similar code) allows redirecting output,
//   but its own functions then appear as source code location
//   because they are not calling t.Helper()
// - we want to output *all* messages regardless of their log level;
//   "go test" will suppress output by default unless a test
//   failed, in which case we want all output that we can get
package testinglogger

import (
	"bytes"
	"fmt"

	"github.com/go-logr/logr"
)

// Logger is the relevant subset of testing.TB.
type Logger interface {
	Helper()
	Log(args ...interface{})
}

// New constructs a new logger for the given test interface.
func New(l Logger) logr.Logger {
	return tlogger{
		l:      l,
		prefix: "",
		values: nil,
	}
}

type tlogger struct {
	l      Logger
	prefix string
	values []interface{}
}

func (l tlogger) clone() tlogger {
	return tlogger{
		l:      l.l,
		prefix: l.prefix,
		values: copySlice(l.values),
	}
}

func copySlice(in []interface{}) []interface{} {
	out := make([]interface{}, len(in))
	copy(out, in)
	return out
}

// trimDuplicates will deduplicates elements provided in multiple KV tuple
// slices, whilst maintaining the distinction between where the items are
// contained.
func trimDuplicates(kvLists ...[]interface{}) [][]interface{} {
	// maintain a map of all seen keys
	seenKeys := map[interface{}]struct{}{}
	// build the same number of output slices as inputs
	outs := make([][]interface{}, len(kvLists))
	// iterate over the input slices backwards, as 'later' kv specifications
	// of the same key will take precedence over earlier ones
	for i := len(kvLists) - 1; i >= 0; i-- {
		// initialise this output slice
		outs[i] = []interface{}{}
		// obtain a reference to the kvList we are processing
		kvList := kvLists[i]

		// start iterating at len(kvList) - 2 (i.e. the 2nd last item) for
		// slices that have an even number of elements.
		// We add (len(kvList) % 2) here to handle the case where there is an
		// odd number of elements in a kvList.
		// If there is an odd number, then the last element in the slice will
		// have the value 'null'.
		for i2 := len(kvList) - 2 + (len(kvList) % 2); i2 >= 0; i2 -= 2 {
			k := kvList[i2]
			// if we have already seen this key, do not include it again
			if _, ok := seenKeys[k]; ok {
				continue
			}
			// make a note that we've observed a new key
			seenKeys[k] = struct{}{}
			// attempt to obtain the value of the key
			var v interface{}
			// i2+1 should only ever be out of bounds if we handling the first
			// iteration over a slice with an odd number of elements
			if i2+1 < len(kvList) {
				v = kvList[i2+1]
			}
			// add this KV tuple to the *start* of the output list to maintain
			// the original order as we are iterating over the slice backwards
			outs[i] = append([]interface{}{k, v}, outs[i]...)
		}
	}
	return outs
}

// Serialization from klog.Infos (https://github.com/kubernetes/klog/blob/199a06da05a146312d7e58c2eeda84f069b1b932/klog.go#L798-L841).
const missingValue = "(MISSING)"

func kvListFormat(keysAndValues ...interface{}) string {
	b := bytes.Buffer{}
	for i := 0; i < len(keysAndValues); i += 2 {
		var v interface{}
		k := keysAndValues[i]
		if i+1 < len(keysAndValues) {
			v = keysAndValues[i+1]
		} else {
			v = missingValue
		}
		if i > 0 {
			b.WriteByte(' ')
		}

		switch v.(type) {
		case string, error:
			b.WriteString(fmt.Sprintf("%s=%q", k, v))
		default:
			if _, ok := v.(fmt.Stringer); ok {
				b.WriteString(fmt.Sprintf("%s=%q", k, v))
			} else {
				b.WriteString(fmt.Sprintf("%s=%+v", k, v))
			}
		}
	}
	return b.String()
}

func (l tlogger) Info(msg string, kvList ...interface{}) {
	l.l.Helper()
	trimmed := trimDuplicates(l.values, kvList)
	fixedStr := kvListFormat(trimmed[0]...)
	userStr := kvListFormat(trimmed[1]...)
	l.log("INFO", l.prefix, msg, fixedStr, userStr)
}

func (l tlogger) Enabled() bool {
	return true
}

func (l tlogger) Error(err error, msg string, kvList ...interface{}) {
	l.l.Helper()
	errStr := kvListFormat("err", err)
	trimmed := trimDuplicates(l.values, kvList)
	fixedStr := kvListFormat(trimmed[0]...)
	userStr := kvListFormat(trimmed[1]...)
	l.log("ERROR", l.prefix, msg, errStr, fixedStr, userStr)
}

func (l tlogger) log(what, prefix string, pieces ...string) {
	args := []interface{}{what}
	if prefix != "" {
		args = append(args, prefix+":")
	}
	for _, piece := range pieces {
		if piece != "" {
			args = append(args, piece)
		}
	}
	l.l.Log(args...)
}

func (l tlogger) V(level int) logr.Logger {
	// No-op, level is ignored.
	return l
}

// WithName returns a new logr.Logger with the specified name appended.  klogr
// uses '/' characters to separate name elements.  Callers should not pass '/'
// in the provided name string, but this library does not actually enforce that.
func (l tlogger) WithName(name string) logr.Logger {
	new := l.clone()
	if len(l.prefix) > 0 {
		new.prefix = l.prefix + "/"
	}
	new.prefix += name
	return new
}

func (l tlogger) WithValues(kvList ...interface{}) logr.Logger {
	new := l.clone()
	new.values = append(new.values, kvList...)
	return new
}

var _ logr.Logger = tlogger{}
