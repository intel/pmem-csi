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

func Figetbsz() {
	C.figetbsz()
}
