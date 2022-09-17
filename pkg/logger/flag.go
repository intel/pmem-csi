/*
Copyright 2020 Intel Coporation.

SPDX-License-Identifier: Apache-2.0
*/

package logger

import (
	"flag"

	"k8s.io/component-base/featuregate"
	logsapi "k8s.io/component-base/logs/api/v1"
	_ "k8s.io/component-base/logs/json/register"
)

func NewFlag() *Options {
	f := &Options{
		LoggingConfiguration: *logsapi.NewLoggingConfiguration(),
	}
	flag.Var(f, "logging-format", "determines log output format, 'text' and 'json' are supported")
	return f
}

// Options is a wrapper around Options which makes
// it usable with flags.Var.
type Options struct {
	logsapi.LoggingConfiguration
}

func (f *Options) Set(value string) error {
	f.Format = value

	// We want contextual logging to be enabled.
	featureGate := featuregate.NewFeatureGate()
	logsapi.AddFeatureGates(featureGate)
	featureGate.SetFromMap(map[string]bool{
		string(logsapi.ContextualLogging): true,
	})

	return logsapi.ValidateAndApply(&f.LoggingConfiguration, featureGate)
}

func (f *Options) String() string {
	return f.Format
}
