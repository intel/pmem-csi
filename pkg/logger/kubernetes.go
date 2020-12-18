/*
Copyright 2020 Intel Coporation.

SPDX-License-Identifier: Apache-2.0
*/

package logger

import (
	"fmt"

	"k8s.io/klog/v2"
)

// ObjectRef references a kubernetes object
type ObjectRefWithType struct {
	klog.ObjectRef
	Type string `json:"type,omitempty"`
}

func (ref ObjectRefWithType) String() string {
	return fmt.Sprintf("%s <%s>", ref.ObjectRef.String(), ref.Type)
}

// KObj returns ObjectRefWithType from ObjectMeta
func KObjWithType(obj klog.KMetadata) ObjectRefWithType {
	return ObjectRefWithType{
		ObjectRef: klog.KObj(obj),
		Type:      fmt.Sprintf("%T", obj),
	}
}
