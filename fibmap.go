// Linux ioctl FIBMAP/FIEMAP for Go
// Copyright (C) 2014 Andreas Klauer <Andreas.Klauer@metamorpher.de>
// License: GPL-2

package fibmap

// #include "fibmap.h"
// #define FIEMAP_SIZE sizeof(struct fiemap)
// #define FIEMAP_EXTENT_SIZE sizeof(struct fiemap_extent)
import "C"
import "unsafe"

const (
	fiemap_SIZE        = C.FIEMAP_SIZE
	fiemap_EXTENT_SIZE = C.FIEMAP_EXTENT_SIZE
)

func Fibmap(fd uintptr, block int) (int, int) {
	var err C.int
	return int(C.fibmap(C.int(fd), C.int(block), &err)), int(err)
}

func Fiemap(fd uintptr) ([]byte, int) {
	buffer := make([]byte, fiemap_SIZE+fiemap_EXTENT_SIZE)
	err := C.fiemap(C.int(fd), C.int(cap(buffer)), (*C.char)(unsafe.Pointer(&buffer[0])))
	return buffer, int(err)
}

func Figetbsz(fd uintptr) (int, int) {
	var err C.int
	return int(C.figetbsz(C.int(fd), &err)), int(err)
}
