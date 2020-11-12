/*
Copyright 2020 Intel Coporation.

SPDX-License-Identifier: Apache-2.0
*/

// Package errors contain some well-defined errors that may have to be
// passed up from low-level layers in the PMEM-CSI stack up to the
// gRPC interface.
//
// These errors must be wrapped (for example, with %w) so that the
// upper layers can use errors.Is to recognize these special errors if
// needed.
package errors

import (
	"errors"
)

var (
	// DeviceExists device with given id already exists
	DeviceExists = errors.New("device exists")

	// ErrDeviceNotFound device does not exists
	DeviceNotFound = errors.New("device not found")

	// ErrDeviceInUse device is in use
	DeviceInUse = errors.New("device in use")

	// ErrNotEnoughSpace no space to create the device
	NotEnoughSpace = errors.New("not enough space")
)
