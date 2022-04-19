/*
Copyright 2020 Intel Coporation.

SPDX-License-Identifier: Apache-2.0
*/

package logger

import (
	"flag"

	"k8s.io/component-base/logs"
	_ "k8s.io/component-base/logs/json/register"
)

func NewFlag() *Options {
	f := &Options{
		Options: *logs.NewOptions(),
	}
	flag.Var(f, "logging-format", "determines log output format, 'text' and 'json' are supported")
	return f
}

// Options is a wrapper around Options which makes
// it usable with flags.Var.
type Options struct {
	logs.Options
}

func (f *Options) Set(value string) error {
	f.Config.Format = value
	return f.ValidateAndApply(nil)
}

func (f *Options) String() string {
	return f.Config.Format
}
