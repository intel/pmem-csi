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
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/kubernetes-csi/csi-test/pkg/sanity"
	sanityutils "github.com/kubernetes-csi/csi-test/utils"
	"google.golang.org/grpc"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
	clientexec "k8s.io/client-go/util/exec"
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
	var execOnTestNode func(args ...string) string

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

		// f.ExecCommandInContainerWithFullOutput assumes that we want a pod in the test's namespace,
		// so we have to set one.
		f.Namespace = &v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "default",
			},
		}

		// Avoid looking for the socat pod unless we actually need it.
		// Some tests might just get skipped. This has to be thread-safe
		// for the sanity stress test.
		var socat *v1.Pod
		var mutex sync.Mutex
		getSocatPod := func() *v1.Pod {
			mutex.Lock()
			defer mutex.Unlock()
			if socat != nil {
				return socat
			}
			socat = getAppInstance(cs, "pmem-csi-node-testing", host)
			return socat
		}

		execOnTestNode = func(args ...string) string {
			// Wait for socat pod on that node. We need it for
			// creating directories.  We could use the PMEM-CSI
			// node container, but that then forces us to have
			// mkdir and rmdir in that container, which we might
			// not want long-term.
			socat := getSocatPod()

			for {
				stdout, stderr, err := f.ExecCommandInContainerWithFullOutput(socat.Name, "socat", args...)
				if err != nil {
					exitErr, ok := err.(clientexec.ExitError)
					if ok && exitErr.ExitStatus() == 126 {
						// This doesn't necessarily mean that the actual binary cannot
						// be executed. It also can be an error in the code which
						// prepares for running in the container.
						framework.Logf("126 = 'cannot execute' error, trying again")
						continue
					}
				}
				framework.ExpectNoError(err, "%s in socat container, stderr:\n%s", args, stderr)
				Expect(stderr).To(BeEmpty(), "unexpected stderr from %s in socat container", args)
				By("Exec Output: " + stdout)
				return stdout
			}
		}
		mkdir := func(path string) (string, error) {
			return execOnTestNode("mktemp", "-d", path), nil
		}
		rmdir := func(path string) error {
			execOnTestNode("rmdir", path)
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
			v       volume
			cancel  func()
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
			// Default timeout for tests.
			ctx, c := context.WithTimeout(context.Background(), 5*time.Minute)
			cancel = c
			v = volume{
				namePrefix: "unset",
				ctx:        ctx,
				sc:         sc,
				cc:         cc,
				nc:         nc,
				cl:         cl,
			}
		})

		AfterEach(func() {
			cl.DeleteVolumes()
			cancel()
		})

		It("stores state across reboots for single volume", func() {
			canRestartNode(nodeID)

			execOnTestNode("sync")
			v.namePrefix = "state-volume"

			// We intentionally check the state of the controller on the node here.
			// The master caches volumes and does not get rebooted.
			initialVolumes, err := ncc.ListVolumes(context.Background(), &csi.ListVolumesRequest{})
			framework.ExpectNoError(err, "list volumes")

			volName, vol := v.create(11*1024*1024, nodeID)
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

			v.remove(vol, volName)
		})

		It("can mount again after reboot", func() {
			canRestartNode(nodeID)
			execOnTestNode("sync")
			v.namePrefix = "mount-volume"

			name, vol := v.create(22*1024*1024, nodeID)
			// Publish for the second time.
			nodeID := v.publish(name, vol)

			// Restart.
			restartNode(f.ClientSet, nodeID, sc)

			_, err := ncc.ListVolumes(context.Background(), &csi.ListVolumesRequest{})
			framework.ExpectNoError(err, "Failed to list volumes after reboot")

			// No failure, is already unpublished.
			v.unpublish(vol, nodeID)

			// Publish for the second time.
			v.publish(name, vol)

			v.unpublish(vol, nodeID)
			v.remove(vol, name)
		})

		It("capacity is restored after controller restart", func() {
			By("Fetching pmem-csi-controller pod name")
			pods, err := WaitForPodsWithLabelRunningReady(f.ClientSet, "default",
				labels.Set{"app": "pmem-csi-controller"}.AsSelector(), 1 /* one replica */, time.Minute)
			framework.ExpectNoError(err, "PMEM-CSI controller running with one replica")
			controllerNode := pods.Items[0].Spec.NodeName
			canRestartNode(controllerNode)

			execOnTestNode("sync")
			capacity, err := cc.GetCapacity(context.Background(), &csi.GetCapacityRequest{})
			framework.ExpectNoError(err, "get capacity before restart")

			restartNode(f.ClientSet, controllerNode, sc)

			_, err = WaitForPodsWithLabelRunningReady(f.ClientSet, "default",
				labels.Set{"app": "pmem-csi-controller"}.AsSelector(), 1 /* one replica */, 5*time.Minute)
			framework.ExpectNoError(err, "PMEM-CSI controller running again with one replica")

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

		var (
			numWorkers = flag.Int("pmem.sanity.workers", 10, "number of worker creating volumes in parallel and thus also the maximum number of volumes at any time")
			numVolumes = flag.Int("pmem.sanity.volumes",
				func() int {
					switch os.Getenv("TEST_DEVICEMODE") {
					case "direct":
						// The minimum volume size in direct mode is 2GB, which makes
						// testing a lot slower than in LVM mode. Therefore we create less
						// volumes.
						return 20
					default:
						return 100
					}
				}(), "number of total volumes to create")
			volumeSize = flag.String("pmem.sanity.volume-size", "15Mi", "size of each volume")
		)
		It("stress test", func() {
			// The load here consists of n workers which
			// create and test volumes in parallel until
			// we've created m volumes.
			wg := sync.WaitGroup{}
			volumes := int64(0)
			volSize, err := resource.ParseQuantity(*volumeSize)
			framework.ExpectNoError(err, "parsing pmem.sanity.volume-size parameter value %s", *volumeSize)
			wg.Add(*numWorkers)

			// Constant time plus variable component for shredding.
			// When using multiple workers, they either share IO bandwidth (parallel shredding)
			// or do it sequentially, therefore we have to multiply by the maximum number
			// of shredding operations.
			secondsPerGigabyte := 10 * time.Second // 2s/GB masured for direct mode in a VM on a fast machine, probably slower elsewhere
			timeout := 300*time.Second + time.Duration(int64(*numWorkers)*volSize.Value()/1024/1024/1024)*secondsPerGigabyte

			By(fmt.Sprintf("creating %d volumes of size %s in %d workers, with a timeout per volume of %s", *numVolumes, volSize.String(), *numWorkers, timeout))
			for i := 0; i < *numWorkers; i++ {
				i := i
				go func() {
					// Order is relevant (first-in-last-out): when returning,
					// we first let GinkgoRecover do its job, which
					// may include marking the test as failed, then tell the
					// parent goroutine that it can proceed.
					defer func() {
						By(fmt.Sprintf("worker-%d terminating", i))
						wg.Done()
					}()
					defer GinkgoRecover() // must be invoked directly by defer, otherwise it doesn't see the panic

					// Each worker must use its own pair of directories.
					targetPath := fmt.Sprintf("%s/worker-%d", sc.TargetPath, i)
					stagingPath := fmt.Sprintf("%s/worker-%d", sc.StagingPath, i)
					execOnTestNode("mkdir", targetPath)
					defer execOnTestNode("rmdir", targetPath)
					execOnTestNode("mkdir", stagingPath)
					defer execOnTestNode("rmdir", stagingPath)

					for {
						volume := atomic.AddInt64(&volumes, 1)
						if volume > int64(*numVolumes) {
							return
						}

						lv := v
						lv.namePrefix = fmt.Sprintf("worker-%d-volume-%d", i, volume)
						lv.targetPath = targetPath
						lv.stagingPath = stagingPath
						func() {
							ctx, cancel := context.WithTimeout(context.Background(), timeout)
							start := time.Now()
							success := false
							defer func() {
								cancel()
								if !success {
									duration := time.Since(start)
									By(fmt.Sprintf("%s: failed after %s", duration))

									// Stop testing.
									atomic.AddInt64(&volumes, int64(*numVolumes))
								}
							}()
							lv.ctx = ctx
							volName, vol := lv.create(volSize.Value(), nodeID)
							lv.publish(volName, vol)
							lv.unpublish(vol, nodeID)
							lv.remove(vol, volName)

							// Success!
							duration := time.Since(start)
							success = true
							By(fmt.Sprintf("%s: done, in %s", lv.namePrefix, duration))
						}()
					}
				}()
			}
			wg.Wait()
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
				v.namePrefix = "cache"

				// Create a cache volume with as many instances as nodes.
				sc.Config.TestVolumeParameters = map[string]string{
					"persistencyModel": "cache",
					"cacheSize":        fmt.Sprintf("%d", len(nodes)),
				}
				sizeInBytes := int64(33 * 1024 * 1024)
				volName, vol := v.create(sizeInBytes, "")
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

				v.remove(vol, volName)

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

type volume struct {
	namePrefix  string
	ctx         context.Context
	sc          *sanity.SanityContext
	cc          csi.ControllerClient
	nc          csi.NodeClient
	cl          *sanity.Cleanup
	stagingPath string
	targetPath  string
}

func (v volume) getStagingPath() string {
	if v.stagingPath != "" {
		return v.stagingPath
	}
	return v.sc.StagingPath
}

func (v volume) getTargetPath() string {
	if v.targetPath != "" {
		return v.targetPath
	}
	return v.sc.TargetPath
}

func (v volume) create(sizeInBytes int64, nodeID string) (string, *csi.Volume) {
	var err error
	name := sanity.UniqueString(v.namePrefix)

	// Create Volume First
	create := fmt.Sprintf("%s: creating a single node writer volume", v.namePrefix)
	By(create)
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
		Parameters: v.sc.Config.TestVolumeParameters,
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
	var vol *csi.CreateVolumeResponse
	err = v.retry(func() error {
		vol, err = v.cc.CreateVolume(
			v.ctx, req,
		)
		return err
	}, "CreateVolume")
	v.cl.MaybeRegisterVolume(name, vol, err)
	framework.ExpectNoError(err, create)
	Expect(vol).NotTo(BeNil())
	Expect(vol.GetVolume()).NotTo(BeNil())
	Expect(vol.GetVolume().GetVolumeId()).NotTo(BeEmpty())
	Expect(vol.GetVolume().GetCapacityBytes()).To(Equal(sizeInBytes), "volume capacity")

	return name, vol.GetVolume()
}

func (v volume) publish(name string, vol *csi.Volume) string {
	var err error

	By(fmt.Sprintf("%s: getting a node id", v.namePrefix))
	nid, err := v.nc.NodeGetInfo(
		v.ctx,
		&csi.NodeGetInfoRequest{})
	framework.ExpectNoError(err, "get node ID")
	Expect(nid).NotTo(BeNil())
	Expect(nid.GetNodeId()).NotTo(BeEmpty())

	var conpubvol *csi.ControllerPublishVolumeResponse
	stage := fmt.Sprintf("%s: node staging volume", v.namePrefix)
	By(stage)
	var nodestagevol interface{}
	err = v.retry(func() error {
		nodestagevol, err = v.nc.NodeStageVolume(
			v.ctx,
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
				StagingTargetPath: v.getStagingPath(),
				VolumeContext:     vol.GetVolumeContext(),
				PublishContext:    conpubvol.GetPublishContext(),
			},
		)
		return err
	}, "NodeStageVolume")
	framework.ExpectNoError(err, stage)
	Expect(nodestagevol).NotTo(BeNil())

	// NodePublishVolume
	publish := fmt.Sprintf("%s: publishing the volume on a node", v.namePrefix)
	By(publish)
	var nodepubvol interface{}
	v.retry(func() error {
		nodepubvol, err = v.nc.NodePublishVolume(
			v.ctx,
			&csi.NodePublishVolumeRequest{
				VolumeId:          vol.GetVolumeId(),
				TargetPath:        v.getTargetPath() + "/target",
				StagingTargetPath: v.getStagingPath(),
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
		return err
	}, "NodePublishVolume")
	framework.ExpectNoError(err, publish)
	Expect(nodepubvol).NotTo(BeNil())

	return nid.GetNodeId()
}

func (v volume) unpublish(vol *csi.Volume, nodeID string) {
	var err error

	unpublish := fmt.Sprintf("%s: cleaning up calling nodeunpublish", v.namePrefix)
	By(unpublish)
	var nodeunpubvol interface{}
	err = v.retry(func() error {
		nodeunpubvol, err = v.nc.NodeUnpublishVolume(
			v.ctx,
			&csi.NodeUnpublishVolumeRequest{
				VolumeId:   vol.GetVolumeId(),
				TargetPath: v.getTargetPath() + "/target",
			})
		return err
	}, "NodeUnpublishVolume")
	framework.ExpectNoError(err, unpublish)
	Expect(nodeunpubvol).NotTo(BeNil())

	unstage := fmt.Sprintf("%s: cleaning up calling nodeunstage", v.namePrefix)
	By(unstage)
	var nodeunstagevol interface{}
	err = v.retry(func() error {
		nodeunstagevol, err = v.nc.NodeUnstageVolume(
			v.ctx,
			&csi.NodeUnstageVolumeRequest{
				VolumeId:          vol.GetVolumeId(),
				StagingTargetPath: v.getStagingPath(),
			},
		)
		return err
	}, "NodeUnstageVolume")
	framework.ExpectNoError(err, unstage)
	Expect(nodeunstagevol).NotTo(BeNil())
}

func (v volume) remove(vol *csi.Volume, volName string) {
	var err error

	delete := fmt.Sprintf("%s: deleting the volume %s", v.namePrefix, vol.GetVolumeId())
	By(delete)
	var deletevol interface{}
	err = v.retry(func() error {
		deletevol, err = v.cc.DeleteVolume(
			v.ctx,
			&csi.DeleteVolumeRequest{
				VolumeId: vol.GetVolumeId(),
			},
		)
		return err
	}, "DeleteVolume")
	framework.ExpectNoError(err, delete)
	Expect(deletevol).NotTo(BeNil())

	v.cl.UnregisterVolume(volName)
}

// retry will execute the operation rapidly until it succeeds or the
// context times out. Each failure gets logged. This is meant for
// operations that are slow (and therefore delay the loop themselves
// with some explicit sleep) and unlikely to fail (hence logging all
// failures).
func (v volume) retry(operation func() error, what string) error {
	for i := 0; ; i++ {
		err := operation()
		if err == nil {
			return nil
		}
		select {
		case <-v.ctx.Done():
			framework.Logf("%s: %s failed and deadline exceeded, giving up", v.namePrefix, what)
			return err
		default:
			framework.Logf("%s: %s failed at attempt %#d, will try again: %s", v.namePrefix, what, i, err)
		}
	}
}

func canRestartNode(nodeID string) {
	if !regexp.MustCompile(`worker\d+$`).MatchString(nodeID) {
		framework.Skipf("node %q not one of the expected QEMU nodes (worker<number>))", nodeID)
	}
}

// restartNode works only for one of the nodes in the QEMU virtual cluster.
// It does a hard poweroff via SysRq and relies on Docker to restart the
// "failed" node.
func restartNode(cs clientset.Interface, nodeID string, sc *sanity.SanityContext) {
	cc := csi.NewControllerClient(sc.ControllerConn)
	capacity, err := cc.GetCapacity(context.Background(), &csi.GetCapacityRequest{})
	framework.ExpectNoError(err, "get capacity before restart")

	node := strings.Split(nodeID, "worker")[1]
	ssh := fmt.Sprintf("%s/_work/%s/ssh.%s",
		os.Getenv("REPO_ROOT"),
		os.Getenv("CLUSTER"),
		node)
	// We detect a successful reboot because a temporary file in the
	// tmpfs /run will be gone after the reboot.
	out, err := exec.Command(ssh, "sudo", "touch", "/run/delete-me").CombinedOutput()
	framework.ExpectNoError(err, "%s touch /run/delete-me:\n%s", ssh, string(out))

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
		test.Stdin = bytes.NewBufferString("test ! -e /run/delete-me")
		out, err := test.CombinedOutput()
		if err == nil {
			return true
		}
		framework.Logf("test for /run/delete-me with %s:\n%s\n%s", ssh, err, out)
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
