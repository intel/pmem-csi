/*
Copyright 2022 Intel Corporation

SPDX-License-Identifier: Apache-2.0
*/

package xfs

// #include <linux/fs.h>
// #include <sys/ioctl.h>
// #include <errno.h>
// #include <string.h>
//
// char *getxattr(int fd, struct fsxattr *arg) {
//     return ioctl(fd, FS_IOC_FSGETXATTR, arg) == 0 ? 0 : strerror(errno);
// }
//
// char *setxattr(int fd, struct fsxattr *arg) {
//     return ioctl(fd, FS_IOC_FSSETXATTR, arg) == 0 ? 0 : strerror(errno);
// }
import "C"

import (
	"fmt"
	"os"
)

// ConfigureFS must be called after mkfs.xfs for the mounted
// XFS filesystem to prepare the volume for usage as fsdax.
// It is idempotent.
func ConfigureFS(path string) error {
	// Operate on root directory.
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %q: %v", path, err)
	}
	defer file.Close()
	fd := C.int(file.Fd())

	// Get extended attributes.
	var attr C.struct_fsxattr
	if errnostr := C.getxattr(fd, &attr); errnostr != nil {
		return fmt.Errorf("FS_IOC_FSGETXATTR for %q: %v", path, C.GoString(errnostr))
	}

	// Set extsize to 2m to enable hugepages in combination with
	// fsdax. This is equivalent to the "xfs_io -c 'extsize 2m'" invocation
	// mentioned in https://nvdimm.wiki.kernel.org/2mib_fs_dax
	attr.fsx_xflags |= C.FS_XFLAG_EXTSZINHERIT
	attr.fsx_extsize = 2 * 1024 * 1024
	if errnostr := C.setxattr(fd, &attr); errnostr != nil {
		return fmt.Errorf("FS_IOC_FSSETXATTR for %q: %v", path, C.GoString(errnostr))
	}

	return nil
}
