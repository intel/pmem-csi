/*
Copyright 2020 Intel Coporation.

SPDX-License-Identifier: Apache-2.0
*/

// Package math contains some arithmetic helper functions.
//
// Origin of the GCD and LCM is the Google Playground,
// with simplifications by Intel.
package math

// GCD returns the greatest common divisor, using the Euclidian algorithm.
func GCD(a, b uint64) uint64 {
	for b != 0 {
		t := b
		b = a % b
		a = t
	}
	return a
}

// LCM returns the least common multiple.
func LCM(a, b uint64) uint64 {
	return a * b / GCD(a, b)
}
