/*
Copyright 2020 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package deploy

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1beta1"

	"github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/assert"
)

type Volumes struct {
	d      *Deployment
	output []string
}

// GetHostVolumes list all volumes (LVM and namespaces) on all nodes.
func GetHostVolumes(d *Deployment) Volumes {
	var output []string

	// Instead of trying to find out number of hosts, we trust the set of
	// ssh.N helper scripts matches running hosts, which should be the case in
	// correctly running tester system. We run ssh.N commands until a ssh.N
	// script appears to be "no such file".
	for host := 0; ; host++ {
		sshcmd := fmt.Sprintf("%s/_work/%s/ssh.%d", os.Getenv("REPO_ROOT"), os.Getenv("CLUSTER"), host)
		if _, err := os.Stat(sshcmd); err == nil {
			output = append(output,
				listVolumes(sshcmd, fmt.Sprintf("host #%d, LVM: ", host), "sudo lvs --foreign --noheadings")...)
			// On the master node we never expect to leak
			// namespaces. On workers it is a bit more
			// tricky: when starting in LVM mode, the
			// namespace created for that by the driver is
			// left behind, which is okay.
			//
			// Detecting that particular namespace is
			// tricky, so for the sake of simplicity we
			// skip leak detection of namespaces unless we
			// know for sure that the test doesn't use LVM
			// mode. Unfortunately, that is currently only
			// the case when the device mode is explicitly
			// set to direct mode. For operator tests that
			// mode is unset and thus namespace leaks are
			// not detected.
			if host == 0 || d.Mode == api.DeviceModeDirect {
				output = append(output,
					listVolumes(sshcmd, fmt.Sprintf("host #%d, direct: ", host), "sudo ndctl list")...)
			}
		} else {
			// ssh wrapper does not exist: all nodes handled.
			break
		}
	}
	return Volumes{
		d:      d,
		output: output,
	}
}

// Some lines are allowed to change, for example the enumeration of
// namespaces and devices because those change when rebooting a
// node. We filter out those lines.
var ignored = regexp.MustCompile(`direct: "dev":"namespace|direct: "blockdev":"pmem`)

func listVolumes(sshcmd, prefix, cmd string) []string {
	for i := 0; ; i++ {
		ssh := exec.Command(sshcmd, cmd)
		// Intentional Output instead of CombinedOutput to dismiss warnings from stderr.
		// lvs may emit lvmetad-related WARNING msg which can't be silenced using -q option.
		out, err := ssh.Output()
		if err != nil {
			if i >= 3 {
				ginkgo.Fail(fmt.Sprintf("%s: repeated ssh attempts failed: %v", sshcmd, err))
			}
			ginkgo.By(fmt.Sprintf("%s %s: attempt #%d failed, retry: %v", sshcmd, cmd, i, err))
			time.Sleep(10 * time.Second)
		} else {
			var lines []string
			for _, line := range strings.Split(string(out), "\n") {
				volumeLine := prefix + strings.TrimSpace(line)
				if !ignored.MatchString(volumeLine) {
					lines = append(lines, volumeLine)
				}
			}
			return lines
		}
	}
}

// CheckForLeftovers lists volumes again after test, diff means leftovers.
func (v Volumes) CheckForLeaks() {
	volNow := GetHostVolumes(v.d)
	if !assert.Equal(ginkgo.GinkgoT(), v, volNow) {
		ginkgo.Fail("volume leak")
	}
}

type NdctlOutput struct {
	Regions []Region `json:"regions"`
}

type Region struct {
	Size          int64       `json:"size"`
	AvailableSize int64       `json:"available_size"`
	Namespaces    []Namespace `json:"namespaces"`
}

type Namespace struct {
	Mode string `json:"mode"`
}

func ParseNdctlOutput(out []byte) (NdctlOutput, error) {
	var parsed NdctlOutput

	// `ndctl list` output is inconsistent:
	// [ { "dev":"region0", ...
	// vs.
	// { regions: [ {"dev": "region0" ...
	var err error
	if strings.HasPrefix(string(out), "[") {
		err = json.Unmarshal(out, &parsed.Regions)
	} else {
		err = json.Unmarshal(out, &parsed)
	}
	return parsed, err
}

// CheckPMEM ensures that a test does not permanently use more than
// half of the available PMEM in each region. We want that to ensure that
// tests in direct mode still have space to work with.
//
// Volume leaks (in direct mode) or allocating all space for a volume group (in LVM mode)
// trigger this check.
func CheckPMEM() {
	for worker := 1; ; worker++ {
		sshcmd := fmt.Sprintf("%s/_work/%s/ssh.%d", os.Getenv("REPO_ROOT"), os.Getenv("CLUSTER"), worker)
		ssh := exec.Command(sshcmd, "sudo ndctl list -Rv")
		out, err := ssh.CombinedOutput()
		if err != nil && os.IsNotExist(err) {
			break
		}
		Expect(err).ShouldNot(HaveOccurred(), "unexpected output for `ndctl list` on on host #%d:\n%s", worker, string(out))
		parsed, err := ParseNdctlOutput(out)
		Expect(err).ShouldNot(HaveOccurred(), "unexpected error parsing the ndctl output %q: %v", string(out), err)
		regions := parsed.Regions
		Expect(regions).ShouldNot(BeEmpty(), "unexpected `ndctl list` output on host #%d, no regions: %s", worker, string(out))
		Expect(err).ShouldNot(HaveOccurred(), "unexpected JSON parsing error for `ndctl list` output on on host #%d:\n%s", worker, string(out))
		for i, region := range regions {
			if region.AvailableSize < region.Size/2 {
				ginkgo.Fail(fmt.Sprintf("more than half of region #%d is in use:\n%s", i, string(out)))
			}
		}
	}
}
