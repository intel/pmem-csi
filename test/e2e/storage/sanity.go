/*
Copyright 2017 The Kubernetes Authors.

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

package storage

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/kubernetes-csi/csi-test/pkg/sanity"
	sanityutils "github.com/kubernetes-csi/csi-test/utils"
	"google.golang.org/grpc"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/test/e2e/framework"
	testutils "k8s.io/kubernetes/test/utils"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var (
	cleanup func()
)

// Run the csi-test sanity tests against a pmem-csi driver
var _ = Describe("sanity", func() {
	workerSocatAddresses := []string{}
	config := sanity.Config{
		TestVolumeSize: 1 * 1024 * 1024,
		// The actual directories will be created as unique
		// temp directories inside these directories.
		// We intentionally do not use the real /var/lib/kubelet/pods as
		// root for the target path, because kubelet is monitoring it
		// and deletes all extra entries that it does not know about.
		TargetPath:  "/var/lib/kubelet/plugins/kubernetes.io/csi/pv/pmem-sanity-target.XXXXXX",
		StagingPath: "/var/lib/kubelet/plugins/kubernetes.io/csi/pv/pmem-sanity-staging.XXXXXX",
	}

	f := framework.NewDefaultFramework("pmem")
	f.SkipNamespaceCreation = true // We don't need a per-test namespace and skipping it makes the tests run faster.

	BeforeEach(func() {
		cs := f.ClientSet

		// This test expects that PMEM-CSI was deployed with
		// socat port forwarding enabled (see deploy/kustomize/testing/README.md).
		// This is not the case when deployed in production mode.
		if os.Getenv("TEST_DEPLOYMENTMODE") == "production" {
			framework.Skipf("driver deployed in production mode")
		}

		hosts, err := framework.NodeSSHHosts(cs)
		framework.ExpectNoError(err, "failed to find external/internal IPs for every node")
		if len(hosts) <= 1 {
			framework.Failf("not enough nodes with external IP")
		}
		workerSocatAddresses = nil
		for _, sshHost := range hosts[1:] {
			host := strings.Split(sshHost, ":")[0] // Instead of duplicating the NodeSSHHosts logic we simply strip the ssh port.
			workerSocatAddresses = append(workerSocatAddresses, fmt.Sprintf("dns:///%s:%d", host, 9735))
		}
		// Node #1 is expected to have a PMEM-CSI node driver
		// instance. If it doesn't, connecting to the PMEM-CSI
		// node service will fail.
		host := strings.Split(hosts[1], ":")[0]
		config.Address = workerSocatAddresses[0]
		// The cluster controller service can be reached via
		// any node, what matters is the service port.
		config.ControllerAddress = fmt.Sprintf("dns:///%s:%d", host, getServicePort(cs, "pmem-csi-controller-testing"))

		exec := func(args ...string) string {
			// Wait for socat pod on that node. We need it for
			// creating directories.  We could use the PMEM-CSI
			// node container, but that then forces us to have
			// mkdir and rmdir in that container, which we might
			// not want long-term.
			socat := getAppInstance(cs, "pmem-csi-node-testing", host)

			// f.ExecCommandInContainerWithFullOutput assumes that we want a pod in the test's namespace,
			// so we have to set one.
			f.Namespace = &v1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "default",
				},
			}

			stdout, stderr, err := f.ExecCommandInContainerWithFullOutput(socat.Name, "socat", args...)
			framework.ExpectNoError(err, "%s in socat container, stderr:\n%s", args, stderr)
			Expect(stderr).To(BeEmpty(), "unexpected stderr from %s in socat container", args)
			By("Exec Output: " + stdout)
			return stdout
		}
		mkdir := func(path string) (string, error) {
			return exec("mktemp", "-d", path), nil
		}
		rmdir := func(path string) error {
			exec("rmdir", path)
			return nil
		}

		config.CreateTargetDir = mkdir
		config.CreateStagingDir = mkdir
		config.RemoveTargetPath = rmdir
		config.RemoveStagingPath = rmdir
	})

	AfterEach(func() {
		if cleanup != nil {
			cleanup()
		}
	})

	var _ = sanity.DescribeSanity("pmem csi", func(sc *sanity.SanityContext) {
		var (
			cl      *sanity.Cleanup
			nc      csi.NodeClient
			cc, ncc csi.ControllerClient
			nodeID  string
		)

		BeforeEach(func() {
			nc = csi.NewNodeClient(sc.Conn)
			cc = csi.NewControllerClient(sc.ControllerConn)
			ncc = csi.NewControllerClient(sc.Conn) // This works because PMEM-CSI exposes the node, controller, and ID server via its csi.sock.
			cl = &sanity.Cleanup{
				Context:                    sc,
				NodeClient:                 nc,
				ControllerClient:           cc,
				ControllerPublishSupported: true,
				NodeStageSupported:         true,
			}
			nid, err := nc.NodeGetInfo(
				context.Background(),
				&csi.NodeGetInfoRequest{})
			framework.ExpectNoError(err, "get node ID")
			nodeID = nid.GetNodeId()
		})

		AfterEach(func() {
			cl.DeleteVolumes()
		})

		It("stores state across reboots for single volume", func() {
			namePrefix := "state-volume"

			// We intentionally check the state of the controller on the node here.
			// The master caches volumes and does not get rebooted.
			initialVolumes, err := ncc.ListVolumes(context.Background(), &csi.ListVolumesRequest{})
			framework.ExpectNoError(err, "list volumes")

			volName, vol := createVolume(cc, sc, cl, namePrefix, 11*1024*1024, nodeID)
			createdVolumes, err := ncc.ListVolumes(context.Background(), &csi.ListVolumesRequest{})
			framework.ExpectNoError(err, "Failed to list volumes after reboot")
			Expect(createdVolumes.Entries).To(HaveLen(len(initialVolumes.Entries)+1), "one more volume on : %s", nodeID)
			// Restart.
			restartNode(f.ClientSet, nodeID, sc)

			// Once we get an answer, it is expected to be the same as before.
			By("checking volumes")
			restartedVolumes, err := ncc.ListVolumes(context.Background(), &csi.ListVolumesRequest{})
			framework.ExpectNoError(err, "Failed to list volumes after reboot")
			Expect(restartedVolumes.Entries).To(ConsistOf(createdVolumes.Entries), "same volumes as before node reboot")

			deleteVolume(cc, vol, volName, cl)
		})

		It("can mount again after reboot", func() {
			namePrefix := "mount-volume"

			name, vol := createVolume(cc, sc, cl, namePrefix, 22*1024*1024, nodeID)
			// Publish for the second time.
			nodeID := publishVolume(cc, nc, sc, cl, name, vol)

			// Restart.
			restartNode(f.ClientSet, nodeID, sc)

			_, err := ncc.ListVolumes(context.Background(), &csi.ListVolumesRequest{})
			framework.ExpectNoError(err, "Failed to list volumes after reboot")

			// No failure, is already unpublished.
			// TODO: In practice this fails with "no mount point specified".

			unpublishVolume(cc, nc, sc, vol, nodeID)

			// Publish for the second time.
			publishVolume(cc, nc, sc, cl, name, vol)

			unpublishVolume(cc, nc, sc, vol, nodeID)
			deleteVolume(cc, vol, name, cl)
		})

		It("capacity is restored after controller restart", func() {
			capacity, err := cc.GetCapacity(context.Background(), &csi.GetCapacityRequest{})
			framework.ExpectNoError(err, "get capacity before restart")

			restartControllerNode(f, sc)

			By("waiting for full capacity")
			Eventually(func() int64 {
				currentCapacity, err := cc.GetCapacity(context.Background(), &csi.GetCapacityRequest{})
				if err != nil {
					// Probably not running again yet.
					return 0
				}
				return currentCapacity.AvailableCapacity
			}, "3m", "5s").Should(Equal(capacity.AvailableCapacity), "total capacity after controller restart")
		})

		Context("cluster", func() {
			type nodeClient struct {
				host    string
				conn    *grpc.ClientConn
				nc      csi.NodeClient
				cc      csi.ControllerClient
				volumes []*csi.ListVolumesResponse_Entry
			}
			var (
				nodes []nodeClient
			)

			BeforeEach(func() {
				nodes = nil
				for i, addr := range workerSocatAddresses {
					conn, err := sanityutils.Connect(addr)
					framework.ExpectNoError(err, "connect to socat instance on node #%d via %s", i+1, addr)
					nodes = append(nodes, nodeClient{
						host: addr,
						conn: conn,
						nc:   csi.NewNodeClient(conn),
						cc:   csi.NewControllerClient(conn),
					})
					initialVolumes, err := nodes[len(nodes)-1].cc.ListVolumes(context.Background(), &csi.ListVolumesRequest{})
					framework.ExpectNoError(err, "list volumes on node #%d", i+1)
					nodes[len(nodes)-1].volumes = initialVolumes.Entries
				}
			})

			AfterEach(func() {
				for _, node := range nodes {
					node.conn.Close()
				}
			})

			It("supports cache volumes", func() {
				// Create a cache volume with as many instances as nodes.
				sc.Config.TestVolumeParameters = map[string]string{
					"persistencyModel": "cache",
					"cacheSize":        fmt.Sprintf("%d", len(nodes)),
				}
				sizeInBytes := int64(33 * 1024 * 1024)
				volName, vol := createVolume(cc, sc, cl, "cache", sizeInBytes, "")
				sc.Config.TestVolumeParameters = map[string]string{}
				var expectedTopology []*csi.Topology
				// These node names are sorted.
				for _, node := range framework.GetReadySchedulableNodesOrDie(f.ClientSet).Items {
					expectedTopology = append(expectedTopology, &csi.Topology{
						Segments: map[string]string{
							"pmem-csi.intel.com/node": node.Name,
						},
					})
				}
				// vol.AccessibleTopology isn't, so we have to sort before comparing.
				sort.Slice(vol.AccessibleTopology, func(i, j int) bool {
					return strings.Compare(
						vol.AccessibleTopology[i].Segments["pmem-csi.intel.com/node"],
						vol.AccessibleTopology[j].Segments["pmem-csi.intel.com/node"],
					) < 0
				})
				Expect(vol.AccessibleTopology).To(Equal(expectedTopology), "cache volume topology")

				// Each node now should have one additional volume,
				// and its size should match the requested one.
				for i, node := range nodes {
					currentVolumes, err := node.cc.ListVolumes(context.Background(), &csi.ListVolumesRequest{})
					framework.ExpectNoError(err, "list volumes on node #%d via %s", i+1)
					Expect(len(currentVolumes.Entries)).To(Equal(len(node.volumes)+1), "one additional volume on node #%d", i+1)
					for i, e := range currentVolumes.Entries {
						if e.Volume.VolumeId == vol.VolumeId {
							Expect(e.Volume.CapacityBytes).To(Equal(sizeInBytes), "additional volume size on node #%d(%s)", i+1, node.host)
							break
						}
					}
				}

				deleteVolume(cc, vol, volName, cl)

				// Now those volumes are gone again.
				for i, node := range nodes {
					currentVolumes, err := node.cc.ListVolumes(context.Background(), &csi.ListVolumesRequest{})
					framework.ExpectNoError(err, "list volumes on node #%d via %s", i+1)
					Expect(len(currentVolumes.Entries)).To(Equal(len(node.volumes)), "same volumes as before on node #%d", i+1)
				}
			})
		})
	})

	// This adds several tests that just get skipped.
	// TODO: static definition of driver capabilities (https://github.com/kubernetes-csi/csi-test/issues/143)
	sanity.GinkgoTest(&config)
})

func getServicePort(cs clientset.Interface, serviceName string) int32 {
	var port int32
	Eventually(func() bool {
		service, err := cs.CoreV1().Services("default").Get(serviceName, metav1.GetOptions{})
		if err != nil {
			return false
		}
		port = service.Spec.Ports[0].NodePort
		return port != 0
	}, "3m").Should(BeTrue(), "%s service running", serviceName)
	return port
}

func getAppInstance(cs clientset.Interface, app string, ip string) *v1.Pod {
	var pod *v1.Pod
	Eventually(func() bool {
		pods, err := cs.CoreV1().Pods("default").List(metav1.ListOptions{})
		if err != nil {
			return false
		}
		for _, p := range pods.Items {
			if p.Labels["app"] == app &&
				(p.Status.HostIP == ip || p.Status.PodIP == ip) {
				pod = &p
				return true
			}
		}
		return false
	}, "3m").Should(BeTrue(), "%s app running on host %s", app, ip)
	return pod
}

func getDaemonSet(cs clientset.Interface, setName string) *appsv1.DaemonSet {
	var set *appsv1.DaemonSet
	Eventually(func() bool {
		s, err := cs.AppsV1().DaemonSets("default").Get(setName, metav1.GetOptions{})
		if err != nil {
			return false
		}
		set = s
		return set != nil
	}, "3m").Should(BeTrue(), "%s pod running", setName)
	return set
}

func createVolume(s csi.ControllerClient, sc *sanity.SanityContext, cl *sanity.Cleanup, namePrefix string, sizeInBytes int64, nodeID string) (string, *csi.Volume) {
	var err error
	name := sanity.UniqueString(namePrefix)

	// Create Volume First
	By("creating a single node writer volume")
	req := &csi.CreateVolumeRequest{
		Name: name,
		VolumeCapabilities: []*csi.VolumeCapability{
			{
				AccessType: &csi.VolumeCapability_Mount{
					Mount: &csi.VolumeCapability_MountVolume{},
				},
				AccessMode: &csi.VolumeCapability_AccessMode{
					Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
				},
			},
		},
		CapacityRange: &csi.CapacityRange{
			RequiredBytes: sizeInBytes,
		},
		Parameters: sc.Config.TestVolumeParameters,
	}
	if nodeID != "" {
		req.AccessibilityRequirements = &csi.TopologyRequirement{
			Requisite: []*csi.Topology{
				{
					Segments: map[string]string{
						"pmem-csi.intel.com/node": nodeID,
					},
				},
			},
			Preferred: []*csi.Topology{
				{
					Segments: map[string]string{
						"pmem-csi.intel.com/node": nodeID,
					},
				},
			},
		}
	}
	vol, err := s.CreateVolume(
		context.Background(), req,
	)
	framework.ExpectNoError(err, "create volume")
	Expect(vol).NotTo(BeNil())
	Expect(vol.GetVolume()).NotTo(BeNil())
	Expect(vol.GetVolume().GetVolumeId()).NotTo(BeEmpty())
	Expect(vol.GetVolume().GetCapacityBytes()).To(Equal(sizeInBytes), "volume capacity")
	cl.RegisterVolume(name, sanity.VolumeInfo{VolumeID: vol.GetVolume().GetVolumeId()})

	return name, vol.GetVolume()
}

func publishVolume(s csi.ControllerClient, c csi.NodeClient, sc *sanity.SanityContext, cl *sanity.Cleanup, name string, vol *csi.Volume) string {
	var err error

	By("getting a node id")
	nid, err := c.NodeGetInfo(
		context.Background(),
		&csi.NodeGetInfoRequest{})
	framework.ExpectNoError(err, "get node ID")
	Expect(nid).NotTo(BeNil())
	Expect(nid.GetNodeId()).NotTo(BeEmpty())

	var conpubvol *csi.ControllerPublishVolumeResponse
	By("controller publishing volume")

	By("node staging volume")
	nodestagevol, err := c.NodeStageVolume(
		context.Background(),
		&csi.NodeStageVolumeRequest{
			VolumeId: vol.GetVolumeId(),
			VolumeCapability: &csi.VolumeCapability{
				AccessType: &csi.VolumeCapability_Mount{
					Mount: &csi.VolumeCapability_MountVolume{},
				},
				AccessMode: &csi.VolumeCapability_AccessMode{
					Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
				},
			},
			StagingTargetPath: sc.StagingPath,
			VolumeContext:     vol.GetVolumeContext(),
			PublishContext:    conpubvol.GetPublishContext(),
		},
	)
	framework.ExpectNoError(err, "node stage volume")
	Expect(nodestagevol).NotTo(BeNil())

	// NodePublishVolume
	By("publishing the volume on a node")
	nodepubvol, err := c.NodePublishVolume(
		context.Background(),
		&csi.NodePublishVolumeRequest{
			VolumeId:          vol.GetVolumeId(),
			TargetPath:        sc.TargetPath + "/target",
			StagingTargetPath: sc.StagingPath,
			VolumeCapability: &csi.VolumeCapability{
				AccessType: &csi.VolumeCapability_Mount{
					Mount: &csi.VolumeCapability_MountVolume{},
				},
				AccessMode: &csi.VolumeCapability_AccessMode{
					Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
				},
			},
			VolumeContext:  vol.GetVolumeContext(),
			PublishContext: conpubvol.GetPublishContext(),
		},
	)
	framework.ExpectNoError(err, "node publish volume")
	Expect(nodepubvol).NotTo(BeNil())

	return nid.GetNodeId()
}

func unpublishVolume(s csi.ControllerClient, c csi.NodeClient, sc *sanity.SanityContext, vol *csi.Volume, nodeID string) {
	var err error

	By("cleaning up calling nodeunpublish")
	nodeunpubvol, err := c.NodeUnpublishVolume(
		context.Background(),
		&csi.NodeUnpublishVolumeRequest{
			VolumeId:   vol.GetVolumeId(),
			TargetPath: sc.TargetPath + "/target",
		})
	framework.ExpectNoError(err, "node unpublish volume")
	Expect(nodeunpubvol).NotTo(BeNil())

	By("cleaning up calling nodeunstage")
	nodeunstagevol, err := c.NodeUnstageVolume(
		context.Background(),
		&csi.NodeUnstageVolumeRequest{
			VolumeId:          vol.GetVolumeId(),
			StagingTargetPath: sc.StagingPath,
		},
	)
	framework.ExpectNoError(err, "node unstage volume")
	Expect(nodeunstagevol).NotTo(BeNil())
}

func deleteVolume(s csi.ControllerClient, vol *csi.Volume, volName string, cl *sanity.Cleanup) {
	var err error

	By("Deleting the volume: " + vol.GetVolumeId())
	_, err = s.DeleteVolume(
		context.Background(),
		&csi.DeleteVolumeRequest{
			VolumeId: vol.GetVolumeId(),
		},
	)
	if err != nil {
		By("Deleting volume  " + vol.GetVolumeId() + " reply: " + err.Error())
	}

	cl.UnregisterVolume(volName)
	framework.ExpectNoError(err, "delete volume %s", vol.GetVolumeId())
}

// restartNode works only for one of the nodes in the QEMU virtual cluster.
// It does a hard poweroff via SysRq and relies on Docker to restart the
// "failed" node.
func restartNode(cs clientset.Interface, nodeID string, sc *sanity.SanityContext) {
	cc := csi.NewControllerClient(sc.ControllerConn)
	capacity, err := cc.GetCapacity(context.Background(), &csi.GetCapacityRequest{})
	framework.ExpectNoError(err, "get capacity before restart")

	if !regexp.MustCompile(`worker\d+$`).MatchString(nodeID) {
		framework.Skipf("node %q not one of the expected QEMU nodes (worker<number>))", nodeID)
	}
	node := strings.Split(nodeID, "worker")[1]
	ssh := fmt.Sprintf("%s/_work/%s/ssh.%s",
		os.Getenv("REPO_ROOT"),
		os.Getenv("CLUSTER"),
		node)
	// We detect a successful reboot because a temporary file in the
	// tmpfs /tmp will be gone after the reboot.
	out, err := exec.Command(ssh, "touch", "/tmp/delete-me").CombinedOutput()
	framework.ExpectNoError(err, "%s touch /tmp/delete-me:\n%s", ssh, string(out))

	// Shutdown via SysRq b (https://major.io/2009/01/29/linux-emergency-reboot-or-shutdown-with-magic-commands/).
	// TCPKeepAlive is necessary because otherwise ssh can hang for a long time when the remote end dies without
	// closing the TCP connection.
	By(fmt.Sprintf("shutting down node %s", nodeID))
	shutdown := exec.Command(ssh)
	shutdown.Stdin = bytes.NewBufferString(`sudo sh -c 'echo 1 > /proc/sys/kernel/sysrq'
sudo sh -c 'echo b > /proc/sysrq-trigger'`)
	out, _ = shutdown.CombinedOutput()

	// Wait for node to reboot.
	By("waiting for node to restart")
	Eventually(func() bool {
		test := exec.Command(ssh)
		test.Stdin = bytes.NewBufferString("test ! -e /tmp/delete-me")
		out, err := test.CombinedOutput()
		if err == nil {
			return true
		}
		framework.Logf("test for /tmp/delete-me with %s:\n%s\n%s", ssh, err, out)
		return false
	}, "5m", "1s").Should(Equal(true), "node up again")

	By("Node reboot success! Waiting for driver restore connections")
	Eventually(func() int64 {
		currentCapacity, err := cc.GetCapacity(context.Background(), &csi.GetCapacityRequest{})
		if err != nil {
			// Probably not running again yet.
			return 0
		}
		return currentCapacity.AvailableCapacity
	}, "3m", "2s").Should(Equal(capacity.AvailableCapacity), "total capacity after node restart")

	By("Probing node")
	Eventually(func() bool {
		if _, err := csi.NewIdentityClient(sc.Conn).Probe(context.Background(), &csi.ProbeRequest{}); err != nil {
			return false
		}
		By("Node driver: Probe success")
		return true
	}, "5m", "2s").Should(Equal(true), "node driver not ready")
}

// restartControllerNode determines where the PMEM-CSI controller runs, then restarts
// that node.
func restartControllerNode(f *framework.Framework, sc *sanity.SanityContext) {
	By("Fetching pmem-csi-controller pod name")
	pods, err := WaitForPodsWithLabelRunningReady(f.ClientSet, "default",
		labels.Set{"app": "pmem-csi-controller"}.AsSelector(), 1 /* one replica */, time.Minute)
	framework.ExpectNoError(err, "PMEM-CSI controller running with one replica")
	node := pods.Items[0].Spec.NodeName
	By(fmt.Sprintf("restarting controller node %s", node))
	restartNode(f.ClientSet, node, sc)
	_, err = WaitForPodsWithLabelRunningReady(f.ClientSet, "default",
		labels.Set{"app": "pmem-csi-controller"}.AsSelector(), 1 /* one replica */, 5*time.Minute)
	framework.ExpectNoError(err, "PMEM-CSI controller running again with one replica")
}

// This is a copy from framework/utils.go with the fix from https://github.com/kubernetes/kubernetes/pull/78687
// TODO: update to Kubernetes 1.15 (assuming that PR gets merged in time for that) and remove this function.
func WaitForPodsWithLabelRunningReady(c clientset.Interface, ns string, label labels.Selector, num int, timeout time.Duration) (pods *v1.PodList, err error) {
	var current int
	err = wait.Poll(2*time.Second, timeout,
		func() (bool, error) {
			pods, err = framework.WaitForPodsWithLabel(c, ns, label)
			if err != nil {
				framework.Logf("Failed to list pods: %v", err)
				if testutils.IsRetryableAPIError(err) {
					return false, nil
				}
				return false, err
			}
			current = 0
			for _, pod := range pods.Items {
				if flag, err := testutils.PodRunningReady(&pod); err == nil && flag == true {
					current++
				}
			}
			if current != num {
				framework.Logf("Got %v pods running and ready, expect: %v", current, num)
				return false, nil
			}
			return true, nil
		})
	return pods, err
}
