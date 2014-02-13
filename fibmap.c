// Linux ioctl FIBMAP/FIEMAP for Go
// Copyright (C) 2014 Andreas Klauer <Andreas.Klauer@metamorpher.de>
// License: GPL-2

#include "fibmap.h"
#include <stdio.h>

void fibmap() {
   int fd, block;
   ioctl(fd, FIBMAP, &block);
   printf("this is fibmap in C\n");
}

void fiemap() {
   int fd, fiemap;
   ioctl(fd, FS_IOC_FIEMAP, &fiemap);
   printf("this is fiemap in C\n");
}

int figetbsz(int fd, int* err) {
    int blocksize = 0;
    (*err) = ioctl(fd, FIGETBSZ, &blocksize);
    return blocksize;
}
