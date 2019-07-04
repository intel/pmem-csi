package pmemexec

import (
	"os/exec"
	"strings"
	"sync"

	"k8s.io/klog/glog"
)

// command mutex, global in driver context:
var Commandmutex = &sync.Mutex{}

// RunCommand wrapper around exec.Command()
func RunCommand(cmd string, args ...string) (string, error) {
	// one system command at a time, to avoid troubles by
	// multiple operations from different threads
	Commandmutex.Lock()
	defer Commandmutex.Unlock()
	glog.V(5).Infof("Executing: %s %s", cmd, strings.Join(args, " "))
	output, err := exec.Command(cmd, args...).CombinedOutput()
	strOutput := string(output)
	glog.V(5).Infof("Output: %s", output)

	return strOutput, err
}

// Same as RunCommand wrapper but without grabbing Commandmutex
func RunCommandNoMutex(cmd string, args ...string) (string, error) {
	glog.V(5).Infof("Executing without mutex: %s %s", cmd, strings.Join(args, " "))
	output, err := exec.Command(cmd, args...).CombinedOutput()
	strOutput := string(output)
	glog.V(5).Infof("Output: %s", output)

	return strOutput, err
}
