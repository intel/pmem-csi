// Linux ioctl FIBMAP/FIEMAP for Go
// Copyright (C) 2014 Andreas Klauer <Andreas.Klauer@metamorpher.de>
// License: GPL-2

#include "fibmap.h"

int fibmap(int fd, int block, int* err) {
    (*err) = ioctl(fd, FIBMAP, &block);
    return block;
}

int fiemap(int fd, int bufsize, char* buf) {
    struct fiemap* fie;
    int err;

    fie = (struct fiemap*) buf;
    fie->fm_start = 0;
    fie->fm_length = FIEMAP_MAX_OFFSET;
    fie->fm_flags = FIEMAP_FLAG_SYNC;
    fie->fm_extent_count = (bufsize - sizeof(struct fiemap))/sizeof(struct fiemap_extent);

    err = ioctl(fd, FS_IOC_FIEMAP, fie);

    return err;
}

int figetbsz(int fd, int* err) {
    int blocksize = 0;
    (*err) = ioctl(fd, FIGETBSZ, &blocksize);
    return blocksize;
}
