/*
Copyright 2018 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

// Package sanity is a Ginkgo test suite that installs csi-pmem on the
// test cluster and then runs the csi-test sanity tests against it.
//
// Like test/e2e, it must be run with REPO_ROOT=<full path> to ensure
// that it can find the _work directory. Parallel test runs are not
// supported because the csi-pmem driver needs exclusive control
// over PMEM.
package sanity

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kubernetes-csi/csi-test/pkg/sanity"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var (
	sshWrapper = os.ExpandEnv("${REPO_ROOT}/_work/ssh-clear-kvm.1")
	binaries   = []string{
		"pmem-csi-driver",
		// Not used at the moment, see below.
		// "pmem-ns-init",
		// "pmem-vgm",
	}
	localTmp, targetTmp string
	port                io.Closer
	config              = sanity.Config{
		TestVolumeSize: 1 * 1024 * 1024,
	}
)

func TestSanity(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "csi-pmem sanity test suite")
}

var _ = SynchronizedBeforeSuite(func() []byte {
	// Run only on Ginkgo node 1

	By("deploying csi-pmem driver")
	var err error
	// This uses a temp directory on the target and
	// locally, with Unix domain sockets inside
	// those. This way we we don't need to worry about
	// conflicting use of IP ports.
	localTmp, err = ioutil.TempDir("", "pmem-sanity")
	Expect(err).NotTo(HaveOccurred())
	targetTmp = ssh("mktemp", "-d", "-p", "/var/tmp")
	targetTmp = strings.Trim(targetTmp, "\n")
	for _, binary := range binaries {
		installBinary(binary, targetTmp)
	}
	csiSock := filepath.Join(targetTmp, "csi.sock")
	csiEndpoint := "unix://" + csiSock
	localSock := filepath.Join(localTmp, "csi.sock")
	config.Address = "unix://" + localSock
	config.TargetPath = filepath.Join(targetTmp, "target")
	config.StagingPath = filepath.Join(targetTmp, "staging")

	By("initializing PMEM")
	// TODO (?): same operations as in pmem-csi.yaml init containers.
	//
	// We cannot run those binaries directly because they have dependencies
	// on the local host's C libraries. We would need to run the init
	// containers. For now let's assume that E2E testing was at least
	// done once and has initialized the PMEM NVDIMMs.
	//
	// ssh(path.Join(targetTmp, "pmem-ns-init"), "-v=5", "-namespacesize=9")
	// ssh(path.Join(targetTmp, "pmem-vgm"), "-v=5")

	By("running pmem-csi-driver")
	// The driver itself can be copied and started, as long as we build on
	// x86-64.
	port, _, err = forwardPort(localSock, csiSock,
		path.Join(targetTmp, "pmem-csi-driver"),
		"-v=5",
		"--endpoint", csiEndpoint,
		"--nodeid=host-1", // we expect to run on the second host in the test cluster
		"--mode=unified",
	)
	Expect(err).NotTo(HaveOccurred())

	return nil

}, func(data []byte) {
	// Run on all Ginkgo nodes
})

var _ = SynchronizedAfterSuite(func() {
	// Run on all Ginkgo nodes
}, func() {
	// Run only Ginkgo on node 1
	By("uninstalling CSI OIM driver")
	var err error
	if port != nil {
		err = port.Close()
		Expect(err).NotTo(HaveOccurred())
	}
	if localTmp != "" {
		err = os.RemoveAll(localTmp)
		Expect(err).NotTo(HaveOccurred())
	}
	if targetTmp != "" {
		ssh("rm", "-rf", targetTmp)
	}
})

var _ = Describe("pmem", func() {
	sanity.GinkgoTest(&config)
})

func ssh(args ...string) string {
	cmd := exec.Command(sshWrapper, args...)
	out, err := cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "%s %s failed with error %s: %s", sshWrapper, args, err, out)
	return string(out)
}

func install(path string, data io.Reader, mode os.FileMode) {
	cmd := exec.Command(sshWrapper, fmt.Sprintf("rm -f '%[1]s' && cat > '%[1]s' && chmod %d '%s'", path, mode, path))
	cmd.Stdin = data
	out, err := cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "Installing %s failed with error %s: %s", path, err, out)
}

func installBinary(binary, targetDir string) {
	from := os.ExpandEnv("${REPO_ROOT}/_output/" + binary)
	to := filepath.Join(targetDir, binary)
	f, err := os.Open(from)
	defer f.Close()
	Expect(err).NotTo(HaveOccurred())
	install(to, f, 0555)
}

type forwardPortInstance struct {
	ssh        *exec.Cmd
	terminated <-chan interface{}
}

// ForwardPort activates port forwarding from a listen socket on the
// current host to another port inside the virtual machine. Errors can
// occur while setting up forwarding as well as later, in which case the
// returned channel will be closed. To stop port forwarding, call the
// io.Closer.
//
// The to and from specification can be ints (for ports) or strings (for
// Unix domaain sockets).
//
// Optionally a command can be run. If none is given, ssh is invoked with -N.
func forwardPort(from interface{}, to interface{}, cmd ...string) (io.Closer, <-chan interface{}, error) {
	fromStr := portToString(from)
	toStr := portToString(to)
	args := []string{
		"-L", fmt.Sprintf("%s:%s", fromStr, toStr),
	}
	what := fmt.Sprintf("%.8s->%.8s", fromStr, toStr)
	if len(cmd) == 0 {
		args = append(args, "-N")
		what = what + "ssh"
	} else {
		args = append(args, cmd...)
		what = filepath.Base(cmd[0]) + " " + what
	}
	fp := forwardPortInstance{
		// ssh closes all extra file descriptors, thus defeating our
		// CmdMonitor. Instead we wait for completion in a goroutine.
		ssh: exec.Command(sshWrapper, args...),
	}
	fp.ssh.Stdout = GinkgoWriter
	fp.ssh.Stderr = GinkgoWriter
	terminated := make(chan interface{})
	fp.terminated = terminated
	if err := fp.ssh.Start(); err != nil {
		return nil, nil, err
	}
	go func() {
		defer close(terminated)
		fp.ssh.Wait()
	}()
	return &fp, terminated, nil
}

func portToString(port interface{}) string {
	if v, ok := port.(int); ok {
		return fmt.Sprintf("localhost:%d", v)
	}
	return fmt.Sprintf("%s", port)
}

func (fp *forwardPortInstance) Close() error {
	fp.ssh.Process.Kill()
	<-fp.terminated
	return nil
}
