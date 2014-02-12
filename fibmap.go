// Linux ioctl FIBMAP/FIEMAP for Go
// Copyright (C) 2014 Andreas Klauer <Andreas.Klauer@metamorpher.de>
// License: GPL-2

package fibmap

// #include <fcntl.h>
// #include <linux/fiemap.h>
// #include <linux/fs.h>
// #include <sys/ioctl.h>
//
// void figetbsz() {
//     int fd, blocksize;
//     ioctl(fd, FIGETBSZ, &blocksize);
// }
//
// void fibmap() {
//     int fd, block;
//     ioctl(fd, FIBMAP, &block);
// }
//
// void fiemap() {
//     int fd, fiemap;
//     ioctl(fd, FS_IOC_FIEMAP, &fiemap);
// }
import "C"

func Fibmap() {
}

func Fiemap() {
}
