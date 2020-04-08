/*
Copyright 2015 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package e2e

import (
	"flag"
	"os"
	"testing"

	"k8s.io/component-base/logs"
	"k8s.io/klog"
	"k8s.io/kubernetes/test/e2e/framework"
	"k8s.io/kubernetes/test/e2e/framework/config"
	"k8s.io/kubernetes/test/e2e/framework/testfiles"

	. "github.com/onsi/ginkgo"

	// test sources
	_ "github.com/intel/pmem-csi/test/e2e/gotests"
	_ "github.com/intel/pmem-csi/test/e2e/storage"
	_ "github.com/intel/pmem-csi/test/e2e/tls"

	"github.com/intel/pmem-csi/test/e2e/deploy"
	"github.com/intel/pmem-csi/test/e2e/operator"
)

func TestMain(m *testing.M) {
	klog.SetOutput(GinkgoWriter)
	logs.InitLogs()

	operator.DefineTests()
	deploy.DefineTests()

	config.CopyFlags(config.Flags, flag.CommandLine)
	framework.RegisterCommonFlags(flag.CommandLine)
	framework.RegisterClusterFlags(flag.CommandLine)
	// Skip slow or distruptive tests by default.
	flag.Set("ginkgo.skip", `\[Slow|Disruptive\]`)
	flag.Parse()

	// Register framework flags, then handle flags.
	framework.AfterReadingAllFlags(&framework.TestContext)

	// We need extra files at runtime.
	repoRoot := os.Getenv("REPO_ROOT")
	if repoRoot != "" {
		testfiles.AddFileSource(RootFileSource{Root: repoRoot})
	}

	// Now run the test suite.
	os.Exit(m.Run())
}

func TestE2E(t *testing.T) {
	RunE2ETests(t)
}
