// Linux ioctl FIBMAP/FIEMAP for Go
// Copyright (C) 2014 Andreas Klauer <Andreas.Klauer@metamorpher.de>
// License: GPL-2

package fibmap

// #include "fibmap.h"
import "C"

func Fibmap() {
	C.fibmap()
}

func Fiemap() {
	C.fiemap()
}

func Figetbsz(fd uintptr) (int, int) {
	var err C.int
	return int(C.figetbsz(C.int(fd), &err)), int(err)
}
