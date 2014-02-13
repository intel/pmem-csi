#ifndef _FS1UP_FIBMAP_H
#define _FS1UP_FIBMAP_H

#include <fcntl.h>
#include <linux/fiemap.h>
#include <linux/fs.h>
#include <sys/ioctl.h>

extern int fibmap(int, int, int*);
extern void fiemap();
extern int figetbsz(int, int*);

#endif // _FS1UP_FIBMAP_H
