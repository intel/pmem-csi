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

	"k8s.io/klog/v2"
)

// WithName updates the logger in the context and returns the new logger and context.
func WithName(ctx context.Context, name string) (context.Context, klog.Logger) {
	logger := klog.FromContext(ctx).WithName(name)
	ctx = klog.NewContext(ctx, logger)
	return ctx, logger
}
