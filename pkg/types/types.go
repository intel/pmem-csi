/*
Copyright 2021 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

// Package types contains some type definitions that are used in
// various places.
package types

import (
	"bytes"
	"encoding/json"
	"strings"
)

// NodeSelector is a set of unique keys and their values.
type NodeSelector map[string]string

// Set converts a JSON representation into a NodeSelector.
func (n *NodeSelector) Set(value string) error {
	// Decoding into a plain map yields better error messages:
	// "cannot unmarshal string into Go value of type types.NodeSelector"
	// vs.
	// "cannot unmarshal string into Go value of type map[string]string"
	var m map[string]string
	if err := json.NewDecoder(bytes.NewBufferString(value)).Decode(&m); err != nil {
		return err
	}
	*n = m
	return nil
}

// String converts into the JSON representation expected by Set.
func (n *NodeSelector) String() string {
	var value bytes.Buffer
	if err := json.NewEncoder(&value).Encode(n); err != nil {
		panic(err)
	}
	return strings.TrimSpace(value.String())
}

// MatchesLabels returns true if all key/value pairs in the selector
// are set in the labels.
func (n *NodeSelector) MatchesLabels(labels map[string]string) bool {
	for key, value := range *n {
		if labels[key] != value {
			return false
		}
	}
	return true
}
