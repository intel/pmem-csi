/*

Copyright (c) 2017-2019 Intel Corporation

SPDX-License-Identifier: Apache-2.0

*/

package imagefile

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/stretchr/testify/assert"
)

func TestNsdax(t *testing.T) {
	type testcase struct {
		dataOffset uint
		alignment  uint
		odDump     string
	}

	// Expected output comes from:
	// - curl -O https://github.com/kata-containers/osbuilder/raw/726f798ff795ef4a8300201cab8d83e83c1496a5/image-builder/nsdax.gpl.c
	// - gcc -o nsdax.gpl nsdax.gpl.c
	// - truncate -s 0 /tmp/image
	// - ./fsdax.gpl /tmp/image <offset> <alignment>
	// - tail -c +$((0x00001001)) /tmp/image | od -t x1 -a
	//
	// 0x00001001 = SZ_4K + 1
	tests := []struct {
		dataOffset uint
		alignment  uint
		odDump     string
	}{
		{1024, 2048,
			`0000000  4e  56  44  49  4d  4d  5f  50  46  4e  5f  49  4e  46  4f  00
          N   V   D   I   M   M   _   P   F   N   _   I   N   F   O nul
0000020  00  00  00  00  00  00  00  00  00  00  00  00  00  00  00  00
        nul nul nul nul nul nul nul nul nul nul nul nul nul nul nul nul
*
0000060  00  00  00  00  00  00  02  00  00  04  00  00  00  00  00  00
        nul nul nul nul nul nul stx nul nul eot nul nul nul nul nul nul
0000100  00  00  00  00  00  00  00  00  01  00  00  00  00  00  00  00
        nul nul nul nul nul nul nul nul soh nul nul nul nul nul nul nul
0000120  00  00  00  00  00  08  00  00  00  00  00  00  00  00  00  00
        nul nul nul nul nul  bs nul nul nul nul nul nul nul nul nul nul
0000140  00  00  00  00  00  00  00  00  00  00  00  00  00  00  00  00
        nul nul nul nul nul nul nul nul nul nul nul nul nul nul nul nul
*
0007760  00  00  00  00  00  00  00  00  30  44  54  e3  2b  23  ea  6c
        nul nul nul nul nul nul nul nul   0   D   T   c   +   #   j   l
0010000
`},
		{1, 2, `0000000  4e  56  44  49  4d  4d  5f  50  46  4e  5f  49  4e  46  4f  00
          N   V   D   I   M   M   _   P   F   N   _   I   N   F   O nul
0000020  00  00  00  00  00  00  00  00  00  00  00  00  00  00  00  00
        nul nul nul nul nul nul nul nul nul nul nul nul nul nul nul nul
*
0000060  00  00  00  00  00  00  02  00  01  00  00  00  00  00  00  00
        nul nul nul nul nul nul stx nul soh nul nul nul nul nul nul nul
0000100  00  00  00  00  00  00  00  00  01  00  00  00  00  00  00  00
        nul nul nul nul nul nul nul nul soh nul nul nul nul nul nul nul
0000120  00  00  00  00  02  00  00  00  00  00  00  00  00  00  00  00
        nul nul nul nul stx nul nul nul nul nul nul nul nul nul nul nul
0000140  00  00  00  00  00  00  00  00  00  00  00  00  00  00  00  00
        nul nul nul nul nul nul nul nul nul nul nul nul nul nul nul nul
*
0007760  00  00  00  00  00  00  00  00  33  38  54  e3  f3  0e  bb  6c
        nul nul nul nul nul nul nul nul   3   8   T   c   s  so   ;   l
0010000
`},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(fmt.Sprintf("offset %d, alignment %d", tt.dataOffset, tt.alignment),
			func(t *testing.T) {
				t.Parallel()
				data := nsdax(tt.dataOffset, tt.alignment)
				cmd := exec.Command("od", "-t", "x1", "-a")
				cmd.Stdin = bytes.NewBuffer(data)
				out, err := cmd.Output()
				if err != nil {
					t.Fatalf("od failed: %v", err)
				}
				assert.Equal(t, tt.odDump, string(out))
			})
	}
}

func rmTmpfile(file *os.File) {
	file.Close()
	os.Remove(file.Name())
}

func TestExtents(t *testing.T) {
	file, err := ioutil.TempFile("", "extents")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer rmTmpfile(file)

	// Create a sparse file with one byte at 1MB, 2 bytes at 2MB, 4 bytes at 4MB, etc.
	// The file then should have one extent at each of these offsets.
	const numExtents = initialExtentSize
	offset := int64(0)
	for count := 1; count <= numExtents; count++ {
		if _, err := file.Seek(offset, os.SEEK_SET); err != nil {
			t.Fatalf("seek to %d: %v", offset, err)
		}
		if _, err := file.Write(make([]byte, count)); err != nil {
			t.Fatalf("write %d bytes at %d: %v", count, offset, err)
		}
		if offset == 0 {
			offset = 1024 * 1024
		} else {
			offset = offset * 2
		}
	}

	verify := func(filename string) {
		extents, err := getAllExtents(filename)
		if err != nil {
			var errno syscall.Errno
			if errors.As(err, &errno) && errno == syscall.ENOTSUP {
				t.Skipf("getting extents not supported for %q", filename)
			}
			t.Fatalf("could not get extents: %v", err)
		}
		assert.Equal(t, numExtents, len(extents), "number of extents")
		offset = 0
		for count := 1; count <= numExtents && count <= len(extents); count++ {
			extent := extents[count-1]
			assert.Equal(t, extent.Logical, uint64(offset), "offset of extent %d", count)
			if offset == 0 {
				offset = 1024 * 1024
			} else {
				offset = offset * 2
			}
		}
	}

	verify(file.Name())

	// Now create a sparse copy. This should be almost
	// instantaneous, despite the nominally large file.
	copy, err := ioutil.TempFile(".", "copy")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer rmTmpfile(copy)
	start := time.Now()
	if err := dd(file.Name(), copy.Name(), true /* sparse */, 0); err != nil {
		t.Fatalf("failed to copy: %v", err)
	}
	delta := time.Since(start)
	assert.Less(t, delta.Seconds(), 10.0, "time for copying file")
	verify(copy.Name())
}

func logStderr(t *testing.T, err error) {
	if err == nil {
		return
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		t.Logf("command failed, stderr:\n%s", string(exitError.Stderr))
	}
}

func TestImageFile(t *testing.T) {
	tooSmallSize := HeaderSize - 1
	toString := func(size Bytes) string {
		return resource.NewQuantity(int64(size), resource.BinarySI).String()
	}

	// Try with ext4 and XFS.
	run := func(fs FsType) {
		t.Run(string(fs), func(t *testing.T) {
			// Try with a variety of sizes because the image file is
			// sensitive to alignment problems.
			tests := []struct {
				size          string
				expectedError string
			}{
				{size: toString(tooSmallSize), expectedError: fmt.Sprintf("invalid image file size %d, must be larger than HeaderSize=%d", tooSmallSize, HeaderSize)},
				{size: "512Mi"},
				{size: "511Mi"},
			}
			for _, tt := range tests {
				tt := tt
				t.Run(tt.size, func(t *testing.T) {
					quantity := resource.MustParse(tt.size)
					testImageFile(t, fs, Bytes(quantity.Value()), tt.expectedError)
				})
			}
		})
	}
	run(Ext4)
	run(Xfs)
}

func testImageFile(t *testing.T, fs FsType, size Bytes, expectedError string) {
	if _, err := exec.LookPath("parted"); err != nil {
		t.Skipf("parted not found: %v", err)
	}

	file, err := ioutil.TempFile("", "image")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer rmTmpfile(file)

	err = Create(file.Name(), size, fs)
	switch {
	case expectedError == "" && err != nil:
		logStderr(t, err)
		t.Fatalf("failed to create image file: %v", err)
	case expectedError != "" && err == nil:
		t.Fatalf("did not fail with %q", expectedError)
	case expectedError != "" && err != nil:
		assert.Equal(t, err.Error(), expectedError, "wrong error message")
		return
	}

	fi, err := file.Stat()
	if err != nil {
		t.Fatalf("failed to stat image file: %v", err)
	}
	assert.GreaterOrEqual(t, fi.Size(), int64(size), "nominal image size")

	if os.Getenv("TEST_WORK") == "" || os.Getenv("VM_IMAGE") == "" {
		t.Log("for testing the image under QEMU, download files for a cluster and set TEST_WORK and VM_IMAGE")
		return
	}
	cmd := exec.Cmd{
		Path: "test/check-imagefile.sh",
		Args: []string{"check-imagefile.sh"},
		Env: append(os.Environ(),
			"EXISTING_VM_FILE="+file.Name(),
		),
		Dir: filepath.Join(os.Getenv("TEST_WORK"), ".."),
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to set up pipe: %v", err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		t.Fatalf("start check-imagefile.sh: %v", err)
	}
	scanner := bufio.NewScanner(stdout)
	success := ""
	for scanner.Scan() {
		line := scanner.Text()
		t.Logf("check-imagefile.sh: %s", line)
		if strings.HasPrefix(line, "SUCCESS: ") {
			success = line
		}
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("check-imagefile.sh failed: %v", err)
	}
	fsReadableForm := fs // %T output from stat
	if fs == Ext4 {
		// We don't bother with checking what is actually mounted,
		// this is close enough.
		fsReadableForm = "ext2/ext3"
	}
	assert.Equal(t,
		fmt.Sprintf("SUCCESS: fstype=%s partition_size=%d partition_start=%d block_size=%d",
			fsReadableForm,
			size-HeaderSize,
			DaxAlignment,
			BlockSize),
		success, "filesystem attributes")
}
