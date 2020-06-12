/*
Copyright 2017 The Kubernetes Authors.
Copyright 2018 Intel Coporation.

SPDX-License-Identifier: Apache-2.0
*/

/*
 This helper program is meant to cause a page fault event for
 dax-mounted hugepage related testing. This program runs in a pod.
 It creates a file and accesses data in it so that kernel gets a page fault,
 while worker host has enabled tracing showing paging events.
 This program prints out inode number and page address of data accessed,
 (catenated together without whitespace) which is then used by e2e testing
 code to verify that traced event was related to the action made here.
*/

package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

func handleError(err error) {
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func runCommand(cmd string, args ...string) (string, error) {
	output, err := exec.Command(cmd, args...).CombinedOutput()
	handleError(err)
	strOutput := string(output)
	return strOutput, err
}

func main() {
	const fname = "/mnt/hugepagedata"
	var stat syscall.Stat_t
	const size = 2*1024*1024 + 4
	runPid := os.Getpid()
	// Create the file
	map_file, err := os.Create(fname)
	handleError(err)
	_, err = map_file.Seek(int64(size-1), 0)
	handleError(err)
	_, err = map_file.Write([]byte(" "))
	handleError(err)
	// Get inode number of the file
	err = syscall.Stat(fname, &stat)
	handleError(err)

	mmap, err := syscall.Mmap(int(map_file.Fd()), 0, size, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	handleError(err)
	mapped := (*[size]byte)(unsafe.Pointer(&mmap[0]))
	for i := 1; i < size; i++ {
		mapped[i] = byte(runPid)
	}

	err = syscall.Munmap(mmap)
	handleError(err)
	err = map_file.Close()
	handleError(err)
	fmt.Printf("0x%x%p", stat.Ino, mapped)
}
