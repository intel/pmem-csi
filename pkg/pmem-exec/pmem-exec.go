package pmemexec

import (
	"os/exec"
	"strings"

	"k8s.io/klog/glog"
)

// RunCommand wrapper around exec.Command()
func RunCommand(cmd string, args ...string) (string, error) {
	glog.V(5).Infof("Executing: %s %s", cmd, strings.Join(args, " "))
	output, err := exec.Command(cmd, args...).CombinedOutput()
	strOutput := string(output)
	glog.V(5).Infof("Output: %s", output)

	return strOutput, err
}
