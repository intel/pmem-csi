/*
Copyright 2020 Intel Coporation.

SPDX-License-Identifier: Apache-2.0
*/

// Package logger defines an interface for adding a structured logger
// to a context and retrieving it again. The fallback when a context
// doesn't have a logger is the global klog logger.
//
// This uses the same context key as logr and thus is compatible
// with code that uses that interface. The difference is that
// the Get function here never returns nil.
//
// Also contains an extension of klog.KObj which includes the
// type of the object in the log output.
package logger

import (
	"context"

	"k8s.io/klog/v2/klogr"

	"github.com/go-logr/logr"
)

// Set returns a context with the given structured logger added to the given context.
func Set(ctx context.Context, logger logr.Logger) context.Context {
	return logr.NewContext(ctx, logger)
}

// Get returns the structured logger stored in the context or (if not set)
// a klog based logger.
func Get(ctx context.Context) logr.Logger {
	l := logr.FromContext(ctx)
	if l != nil {
		return l
	}
	return klogr.NewWithOptions(klogr.WithFormat(klogr.FormatKlog))
}
