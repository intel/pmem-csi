/*
Copyright 2020 Intel Coporation.

SPDX-License-Identifier: Apache-2.0
*/

package logger

import (
	"flag"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/component-base/logs"
)

func NewFlag() *Options {
	f := &Options{}
	flag.Var(f, "logging-format", "determines log output format, 'text' and 'json' are supported")
	return f
}

// Options is a wrapper around Options which makes
// it usable with flags.Var.
type Options struct {
	logs.Options
}

func (f *Options) Set(value string) error {
	f.LogFormat = value
	return utilerrors.NewAggregate(f.Validate())
}

func (f *Options) String() string {
	return f.LogFormat
}
