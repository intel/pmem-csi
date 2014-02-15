// Linux ioctl FIBMAP/FIEMAP for Go
// Copyright (C) 2014 Andreas Klauer <Andreas.Klauer@metamorpher.de>
// License: GPL-2

package fibmap

// #include "fibmap.h"
// #define FIEMAP_SIZE sizeof(struct fiemap)
// #define FIEMAP_EXTENT_SIZE sizeof(struct fiemap_extent)
import "C"
import "unsafe"
import "os"

const (
	fiemap_SIZE        = C.FIEMAP_SIZE
	fiemap_EXTENT_SIZE = C.FIEMAP_EXTENT_SIZE
	SEEK_DATA          = C.SEEK_DATA
	SEEK_HOLE          = C.SEEK_HOLE
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

// use SEEK_DATA, SEEK_HOLE to find allocated data ranges in a file
func Datahole(f *os.File) []int64 {
	old, _ := f.Seek(0, os.SEEK_CUR)
	f.Seek(0, os.SEEK_SET)

	var data, hole int64
	var datahole []int64

	for {
		data, _ = f.Seek(hole, SEEK_DATA)

		if data >= hole {
			hole, _ = f.Seek(data, SEEK_HOLE)

			if hole > data {
				datahole = append(datahole, data, hole-data)
				continue
			}
		}
		break
	}

	f.Seek(old, os.SEEK_SET)

	return datahole
}
