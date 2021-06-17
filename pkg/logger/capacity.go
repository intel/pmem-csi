/*
Copyright 2020 Intel Coporation.

SPDX-License-Identifier: Apache-2.0
*/

package logger

import (
	"k8s.io/apimachinery/pkg/api/resource"
)

// CapacityRef returns an object that pretty-prints the given number of bytes.
func CapacityRef(size int64) *resource.Quantity {
	return resource.NewQuantity(size, resource.BinarySI)
}
