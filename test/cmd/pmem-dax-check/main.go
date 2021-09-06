/*
Copyright 2017 The Kubernetes Authors.
Copyright 2018 Intel Coporation.

SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	wait := flag.Bool("wait", false, "wait for CTRL-C while memory is mapped")
	size := flag.Int("size", 4096, "size of the mapped region")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintf(os.Stderr, "need exactly one file name as parameter\n")
		os.Exit(2)
	}

	code, err := run(flag.Arg(0), *size, *wait)
	if err != nil {
		fmt.Fprintf(os.Stdout, "%v\n", err)
	}
	os.Exit(code)
}

func run(filename string, size int, wait bool) (int, error) {
	prot := syscall.PROT_READ
	created := false

	// Open the file.
	file, err := os.OpenFile(filename, os.O_RDONLY, 0)
	if err != nil {
		if !os.IsNotExist(err) {
			return 2, err
		}
		file, err = os.Create(filename)
		if err != nil {
			return 2, err
		}
		defer os.Remove(filename)
		created = true
	}
	defer file.Close()

	info, err := os.Stat(filename)
	if err != nil {
		return 2, err
	}
	if info.Mode().IsRegular() && info.Size() < int64(size) {
		if err := syscall.Ftruncate(int(file.Fd()), int64(size)); err != nil {
			return 2, fmt.Errorf("enlarge %q to %d bytes: %v", filename, size, err)
		}
	}

	// Some flags are currently (Go 1.13.6) not available. This value is from
	// /usr/include/x86_64-linux-gnu/bits/mman.h and /usr/include/asm-generic/mman.h.
	const MAP_SYNC = 0x80000
	const MAP_SHARED_VALIDATE = 0x03

	// Try to mmap in different ways to determine whether that works in principle
	// (it should) and whether MAP_SYNC works (may or may not).
	data, err := syscall.Mmap(int(file.Fd()), 0, size, prot, MAP_SHARED_VALIDATE)
	if err != nil {
		return 2, fmt.Errorf("%s: mmap without MAP_SYNC failed: %v", filename, err)
	}
	if err := syscall.Munmap(data); err != nil {
		return 2, fmt.Errorf("%s: mmunmap failed: %v", filename, err)
	}

	data, err = syscall.Mmap(int(file.Fd()), 0, size, prot, MAP_SHARED_VALIDATE|MAP_SYNC)
	if err != nil {
		return 1, fmt.Errorf("%s: does not support MAP_SYNC: %v", filename, err)
	}

	// Check for non-zero bytes. This has the intended side effect that all pages
	// are really touched instead of just being mapped.
	zeroed := true
	for i := 0; i < size; i++ {
		if data[i] != 0 {
			zeroed = false
		}
	}

	if !zeroed && created {
		return 2, fmt.Errorf("%s: MAP_SYNC succeeded, but the newly created file contained garbage", filename)
	}

	if wait {
		fmt.Printf("map(MAP_SYNC) succeeded. Continue with CTRL-C.\n")
		exitSignal := make(chan os.Signal, 2)
		signal.Notify(exitSignal, syscall.SIGINT, syscall.SIGTERM)
		<-exitSignal
	}

	if err := syscall.Munmap(data); err != nil {
		return 2, fmt.Errorf("%s: mmunmap failed: %v", filename, err)
	}
	return 0, fmt.Errorf("%s: supports MAP_SYNC, content is zeroed: %v", filename, zeroed)
}
