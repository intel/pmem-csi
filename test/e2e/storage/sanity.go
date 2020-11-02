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
	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1alpha1"
	"github.com/kubernetes-csi/csi-test/v3/pkg/sanity"
	sanityutils "github.com/kubernetes-csi/csi-test/v3/utils"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/clock"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
	clientexec "k8s.io/client-go/util/exec"
	"k8s.io/kubernetes/test/e2e/framework"
	e2enode "k8s.io/kubernetes/test/e2e/framework/node"
	e2epod "k8s.io/kubernetes/test/e2e/framework/pod"
	"k8s.io/kubernetes/test/e2e/framework/skipper"
	testutils "k8s.io/kubernetes/test/utils"

	"github.com/intel/pmem-csi/test/e2e/deploy"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var (
	numSanityWorkers = flag.Int("pmem.sanity.workers", 10, "number of worker creating volumes in parallel and thus also the maximum number of volumes at any time")
	// 0 = default is overridden below.
	numSanityVolumes = flag.Int("pmem.sanity.volumes", 0, "number of total volumes to create")
	sanityVolumeSize = flag.String("pmem.sanity.volume-size", "15Mi", "size of each volume")
)

// Run the csi-test sanity tests against a PMEM-CSI driver.
var _ = deploy.DescribeForSome("sanity", func(d *deploy.Deployment) bool {
	// This test expects that PMEM-CSI was deployed with
	// socat port forwarding enabled (see deploy/kustomize/testing/README.md).
	// This is not the case when deployed in production mode.
	return d.Testing
}, func(d *deploy.Deployment) {
	config := sanity.NewTestConfig()
	config.TestVolumeSize = 1 * 1024 * 1024
	// The actual directories will be created as unique
	// temp directories inside these directories.
	// We intentionally do not use the real /var/lib/kubelet/pods as
	// root for the target path, because kubelet is monitoring it
	// and deletes all extra entries that it does not know about.
	config.TargetPath = "/var/lib/kubelet/plugins/kubernetes.io/csi/pv/pmem-sanity-target.XXXXXX"
	config.StagingPath = "/var/lib/kubelet/plugins/kubernetes.io/csi/pv/pmem-sanity-staging.XXXXXX"
	config.ControllerDialOptions = []grpc.DialOption{
		// For our restart tests.
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			PermitWithoutStream: true,
			// This is the minimum. Specifying it explicitly
			// avoids some log output from gRPC.
			Time: 10 * time.Second,
		}),
		// For plain HTTP.
		grpc.WithInsecure(),
	}

	f := framework.NewDefaultFramework("pmem")
	f.SkipNamespaceCreation = true // We don't need a per-test namespace and skipping it makes the tests run faster.
	var execOnTestNode func(args ...string) string
	var cleanup func()
	var cluster *deploy.Cluster

	BeforeEach(func() {
		cs := f.ClientSet

		var err error
		cluster, err = deploy.NewCluster(cs, f.DynamicClient)
		framework.ExpectNoError(err, "query cluster")

		config.Address, config.ControllerAddress, err = deploy.LookupCSIAddresses(cluster, d.Namespace)
		framework.ExpectNoError(err, "find CSI addresses")

		framework.Logf("sanity: using controller %s and node %s", config.ControllerAddress, config.Address)

		// f.ExecCommandInContainerWithFullOutput assumes that we want a pod in the test's namespace,
		// so we have to set one.
		f.Namespace = &v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: d.Namespace,
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
			socat = cluster.WaitForAppInstance("pmem-csi-node-testing", cluster.NodeIP(1), d.Namespace)
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
			path = execOnTestNode("mktemp", "-d", path)
			// Ensure that the path that we created
			// survives a sudden power loss (as during the
			// restart tests below), otherwise rmdir will
			// fail when it's gone.
			execOnTestNode("sync", "-f", path)
			return path, nil
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

	// This adds several tests that just get skipped.
	// TODO: static definition of driver capabilities (https://github.com/kubernetes-csi/csi-test/issues/143)
	sc := sanity.GinkgoTest(&config)

	// The test context caches a connection to the CSI driver.
	// When we re-deploy, gRPC will try to reconnect for that connection,
	// leading to log messages about connection errors because the node port
	// is allocated dynamically and changes when redeploying. Therefore
	// we register a hook which clears the connection when PMEM-CSI
	// gets re-deployed.
	scFinalize := func() {
		sc.Finalize()
		// Not sure why this isn't in Finalize - a bug?
		sc.Conn = nil
		sc.ControllerConn = nil
	}
	deploy.AddUninstallHook(func(deploymentName string) {
		framework.Logf("sanity: deployment %s is gone, closing test connections to controller %s and node %s.",
			deploymentName,
			config.ControllerAddress,
			config.Address)
		scFinalize()
	})

	var _ = Describe("PMEM-CSI", func() {
		var (
			cl       *sanity.Cleanup
			nc       csi.NodeClient
			cc, ncc  csi.ControllerClient
			nodeID   string
			v        volume
			cancel   func()
			rebooted bool
		)

		BeforeEach(func() {
			sc.Setup()
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
			rebooted = false
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
			sc.Teardown()

			if rebooted {
				// Remove all cached connections, too.
				scFinalize()

				// Rebooting a node increases the restart counter of
				// the containers. This is normal in that case, but
				// for the next test triggers the check that
				// containers shouldn't restart. To get around that,
				// we delete all PMEM-CSI pods after a reboot test.
				By("stopping all PMEM-CSI pods after rebooting some node(s)")
				d.DeleteAllPods(cluster)
			}
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
			rebooted = true
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
			rebooted = true
			restartNode(f.ClientSet, nodeID, sc)

			_, err := ncc.ListVolumes(context.Background(), &csi.ListVolumesRequest{})
			framework.ExpectNoError(err, "Failed to list volumes after reboot")

			// No failure expected although rebooting already unmounted the volume.
			// We still need to remove the target path, if it has survived
			// the hard power off.
			v.unpublish(vol, nodeID)

			// Publish for the second time.
			v.publish(name, vol)

			v.unpublish(vol, nodeID)
			v.remove(vol, name)
		})

		It("can publish volume after a node driver restart", func() {
			restartPod := ""
			v.namePrefix = "mount-volume"

			pods, err := WaitForPodsWithLabelRunningReady(f.ClientSet, d.Namespace,
				labels.Set{"app": "pmem-csi-node"}.AsSelector(), cluster.NumNodes()-1, time.Minute)
			framework.ExpectNoError(err, "All node drivers are not ready")

			name, vol := v.create(22*1024*1024, nodeID)
			defer v.remove(vol, name)

			nodeID := v.publish(name, vol)
			defer v.unpublish(vol, nodeID)

			for _, p := range pods.Items {
				if p.Spec.NodeName == nodeID {
					restartPod = p.Name
				}
			}

			// delete driver on node
			err = e2epod.DeletePodWithWaitByName(f.ClientSet, d.Namespace, restartPod)
			Expect(err).ShouldNot(HaveOccurred(), "Failed to stop driver pod %s", restartPod)

			// Wait till the driver pod get restarted
			err = e2epod.WaitForPodsReady(f.ClientSet, d.Namespace, restartPod, time.Now().Minute())
			framework.ExpectNoError(err, "Node driver '%s' pod is not ready", restartPod)

			_, err = ncc.ListVolumes(context.Background(), &csi.ListVolumesRequest{})
			framework.ExpectNoError(err, "Failed to list volumes after reboot")

			// Try republish
			v.publish(name, vol)
		})

		It("capacity is restored after controller restart", func() {
			By("Fetching pmem-csi-controller pod name")
			pods, err := WaitForPodsWithLabelRunningReady(f.ClientSet, d.Namespace,
				labels.Set{"app": "pmem-csi-controller"}.AsSelector(), 1 /* one replica */, time.Minute)
			framework.ExpectNoError(err, "PMEM-CSI controller running with one replica")
			controllerNode := pods.Items[0].Spec.NodeName
			canRestartNode(controllerNode)

			execOnTestNode("sync")
			capacity, err := cc.GetCapacity(context.Background(), &csi.GetCapacityRequest{})
			framework.ExpectNoError(err, "get capacity before restart")

			rebooted = true
			restartNode(f.ClientSet, controllerNode, sc)

			_, err = WaitForPodsWithLabelRunningReady(f.ClientSet, d.Namespace,
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

		It("should return right capacity", func() {
			resp, err := ncc.GetCapacity(context.Background(), &csi.GetCapacityRequest{})
			Expect(err).Should(BeNil(), "Failed to get node initial capacity")
			nodeCapacity := resp.AvailableCapacity
			i := 0
			volSize := int64(1) * 1024 * 1024 * 1024

			// Keep creating volumes till there is change in node capacity
			Eventually(func() bool {
				volName := fmt.Sprintf("get-capacity-check-%d", i)
				i++
				By(fmt.Sprintf("creating volume '%s' on node '%s'", volName, nodeID))
				req := &csi.CreateVolumeRequest{
					Name: volName,
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
						RequiredBytes: volSize,
					},
				}
				volResp, err := ncc.CreateVolume(context.Background(), req)
				Expect(err).Should(BeNil(), "Failed to create volume on node")
				cl.MaybeRegisterVolume(volName, volResp, err)

				resp, err := ncc.GetCapacity(context.Background(), &csi.GetCapacityRequest{})
				Expect(err).Should(BeNil(), "Failed to get node capacity after volume creation")

				// find if change in capacity
				ret := nodeCapacity != resp.AvailableCapacity
				// capture the new capacity
				nodeCapacity = resp.AvailableCapacity

				return ret
			})

			By("Getting controller capacity")
			resp, err = cc.GetCapacity(context.Background(), &csi.GetCapacityRequest{
				AccessibleTopology: &csi.Topology{
					Segments: map[string]string{
						"pmem-csi.intel.com/node": nodeID,
					},
				},
			})
			Expect(err).Should(BeNil(), "Failed to get capacity of controller")
			Expect(resp.AvailableCapacity).To(Equal(nodeCapacity), "capacity mismatch")
		})

		It("excessive message sizes should be rejected", func() {
			req := &csi.GetCapacityRequest{
				AccessibleTopology: &csi.Topology{
					Segments: map[string]string{},
				},
			}
			for i := 0; i < 100000; i++ {
				req.AccessibleTopology.Segments[fmt.Sprintf("pmem-csi.intel.com/node%d", i)] = nodeID
			}
			_, err := cc.GetCapacity(context.Background(), req)
			Expect(err).ShouldNot(BeNil(), "unexpected success for too large request")
			status, ok := status.FromError(err)
			Expect(ok).Should(BeTrue(), "expected status in error, got: %v", err)
			Expect(status.Message()).Should(ContainSubstring("grpc: received message larger than max"))
		})

		It("delete volume should fail with appropriate error", func() {
			v.namePrefix = "delete-volume"

			name, vol := v.create(2*1024*1024, nodeID)
			// Publish for the second time.
			nodeID := v.publish(name, vol)

			_, err := v.cc.DeleteVolume(v.ctx, &csi.DeleteVolumeRequest{
				VolumeId: vol.GetVolumeId(),
			})
			Expect(err).ShouldNot(BeNil(), fmt.Sprintf("Volume(%s) in use cannot be deleted", name))
			s, ok := status.FromError(err)
			Expect(ok).Should(BeTrue(), "Expected a status error")
			Expect(s.Code()).Should(BeEquivalentTo(codes.FailedPrecondition), "Expected device busy error")

			v.unpublish(vol, nodeID)

			v.remove(vol, name)
		})

		It("CreateVolume should return ResourceExhausted", func() {
			v.namePrefix = "resource-exhausted"

			v.create(1024*1024*1024*1024*1024, nodeID, codes.ResourceExhausted)
		})

		It("stress test", func() {
			// The load here consists of n workers which
			// create and test volumes in parallel until
			// we've created m volumes.
			wg := sync.WaitGroup{}
			volumes := int64(0)
			volSize, err := resource.ParseQuantity(*sanityVolumeSize)
			framework.ExpectNoError(err, "parsing pmem.sanity.volume-size parameter value %s", *sanityVolumeSize)
			wg.Add(*numSanityWorkers)

			// Constant time plus variable component for shredding.
			// When using multiple workers, they either share IO bandwidth (parallel shredding)
			// or do it sequentially, therefore we have to multiply by the maximum number
			// of shredding operations.
			secondsPerGigabyte := 10 * time.Second // 2s/GB masured for direct mode in a VM on a fast machine, probably slower elsewhere
			timeout := 300*time.Second + time.Duration(int64(*numSanityWorkers)*volSize.Value()/1024/1024/1024)*secondsPerGigabyte

			// The default depends on the driver deployment and thus has to be calculated here.
			sanityVolumes := *numSanityVolumes
			if sanityVolumes == 0 {
				switch d.Mode {
				case api.DeviceModeDirect:
					// The minimum volume size in direct mode is 2GB, which makes
					// testing a lot slower than in LVM mode. Therefore we create less
					// volumes.
					sanityVolumes = 20
				default:
					sanityVolumes = 100
				}
			}

			// Also adapt the overall test timeout.
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(int64(timeout)*int64(sanityVolumes)))
			defer cancel()
			v.ctx = ctx

			By(fmt.Sprintf("creating %d volumes of size %s in %d workers, with a timeout per volume of %s", sanityVolumes, volSize.String(), *numSanityWorkers, timeout))
			for i := 0; i < *numSanityWorkers; i++ {
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
						if volume > int64(sanityVolumes) {
							return
						}

						lv := v
						lv.namePrefix = fmt.Sprintf("worker-%d-volume-%d", i, volume)
						lv.targetPath = targetPath
						lv.stagingPath = stagingPath
						func() {
							ctx, cancel := context.WithTimeout(v.ctx, timeout)
							start := time.Now()
							success := false
							defer func() {
								cancel()
								if !success {
									duration := time.Since(start)
									By(fmt.Sprintf("%s: failed after %s", lv.namePrefix, duration))

									// Stop testing.
									atomic.AddInt64(&volumes, int64(sanityVolumes))
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
				nodes map[string]nodeClient
			)

			BeforeEach(func() {
				// Worker nodes with PMEM.
				nodes = make(map[string]nodeClient)
				for i := 1; i < cluster.NumNodes(); i++ {
					addr := cluster.NodeServiceAddress(i, deploy.SocatPort)
					conn, err := sanityutils.Connect(addr, grpc.WithInsecure())
					framework.ExpectNoError(err, "connect to socat instance on node #%d via %s", i, addr)
					node := nodeClient{
						host: addr,
						conn: conn,
						nc:   csi.NewNodeClient(conn),
						cc:   csi.NewControllerClient(conn),
					}
					info, err := node.nc.NodeGetInfo(context.Background(), &csi.NodeGetInfoRequest{})
					framework.ExpectNoError(err, "node name #%d", i+1)
					initialVolumes, err := node.cc.ListVolumes(context.Background(), &csi.ListVolumesRequest{})
					framework.ExpectNoError(err, "list volumes on node #%d", i)
					node.volumes = initialVolumes.Entries

					//nodes = append(nodes, node)
					nodes[info.NodeId] = node
				}
			})

			AfterEach(func() {
				for _, node := range nodes {
					node.conn.Close()
				}
			})

			It("supports persistent volumes", func() {
				sizeInBytes := int64(33 * 1024 * 1024)
				volName, vol := v.create(sizeInBytes, "")

				Expect(len(vol.AccessibleTopology)).To(Equal(1), "accessible topology mismatch")
				volNodeName := vol.AccessibleTopology[0].Segments["pmem-csi.intel.com/node"]
				Expect(volNodeName).NotTo(BeNil(), "wrong topology")

				// Node now should have one additional volume only one node,
				// and its size should match the requested one.
				for nodeName, node := range nodes {
					currentVolumes, err := node.cc.ListVolumes(context.Background(), &csi.ListVolumesRequest{})
					framework.ExpectNoError(err, "list volumes on node %s", nodeName)
					if nodeName == volNodeName {
						Expect(len(currentVolumes.Entries)).To(Equal(len(node.volumes)+1), "one additional volume on node %s", nodeName)
						for _, e := range currentVolumes.Entries {
							if e.Volume.VolumeId == vol.VolumeId {
								Expect(e.Volume.CapacityBytes).To(Equal(sizeInBytes), "additional volume size on node #%s(%s)", nodeName, node.host)
								break
							}
						}
					} else {
						// ensure that no new volume on other nodes
						Expect(len(currentVolumes.Entries)).To(Equal(len(node.volumes)), "volume count mismatch on node %s", nodeName)
					}
				}

				v.remove(vol, volName)
			})

			It("persistent volume retains data", func() {
				sizeInBytes := int64(33 * 1024 * 1024)
				volName, vol := v.create(sizeInBytes, nodeID)

				Expect(len(vol.AccessibleTopology)).To(Equal(1), "accessible topology mismatch")
				Expect(vol.AccessibleTopology[0].Segments["pmem-csi.intel.com/node"]).To(Equal(nodeID), "unexpected node")

				// Node now should have one additional volume only one node,
				// and its size should match the requested one.
				node := nodes[nodeID]
				currentVolumes, err := node.cc.ListVolumes(context.Background(), &csi.ListVolumesRequest{})
				framework.ExpectNoError(err, "list volumes on node %s", nodeID)
				Expect(len(currentVolumes.Entries)).To(Equal(len(node.volumes)+1), "one additional volume on node %s", nodeID)
				for _, e := range currentVolumes.Entries {
					if e.Volume.VolumeId == vol.VolumeId {
						Expect(e.Volume.CapacityBytes).To(Equal(sizeInBytes), "additional volume size on node #%s(%s)", nodeID, node.host)
						break
					}
				}

				v.publish(volName, vol)

				i := strings.Split(nodeID, "worker")[1]

				sshcmd := fmt.Sprintf("%s/_work/%s/ssh.%s", os.Getenv("REPO_ROOT"), os.Getenv("CLUSTER"), i)
				// write some data to mounted volume
				cmd := "sudo sh -c 'echo -n hello > " + v.getTargetPath() + "/target/test-file'"
				ssh := exec.Command(sshcmd, cmd)
				out, err := ssh.CombinedOutput()
				framework.ExpectNoError(err, "write failure:\n%s", string(out))

				// unmount volume
				v.unpublish(vol, nodeID)

				// republish volume
				v.publish(volName, vol)

				// ensure the data retained
				cmd = "sudo cat " + v.getTargetPath() + "/target/test-file"
				ssh = exec.Command(sshcmd, cmd)
				out, err = ssh.CombinedOutput()
				framework.ExpectNoError(err, "read failure:\n%s", string(out))
				Expect(string(out)).To(Equal("hello"), "read failure")

				// end of test cleanup
				v.unpublish(vol, nodeID)
				v.remove(vol, volName)
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
				readyNodes, err := e2enode.GetReadySchedulableNodes(f.ClientSet)
				framework.ExpectNoError(err, "get schedulable nodes")
				for _, node := range readyNodes.Items {
					if node.Labels["storage"] != "pmem" {
						continue
					}
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
				for nodeName, node := range nodes {
					currentVolumes, err := node.cc.ListVolumes(context.Background(), &csi.ListVolumesRequest{})
					framework.ExpectNoError(err, "list volumes on node %s via %s", nodeName)
					Expect(len(currentVolumes.Entries)).To(Equal(len(node.volumes)+1), "one additional volume on node %s", nodeName)
					for _, e := range currentVolumes.Entries {
						if e.Volume.VolumeId == vol.VolumeId {
							Expect(e.Volume.CapacityBytes).To(Equal(sizeInBytes), "additional volume size on node %s(%s)", nodeName, node.host)
							break
						}
					}
				}

				v.remove(vol, volName)

				// Now those volumes are gone again.
				for nodeName, node := range nodes {
					currentVolumes, err := node.cc.ListVolumes(context.Background(), &csi.ListVolumesRequest{})
					framework.ExpectNoError(err, "list volumes on node %s", nodeName)
					Expect(len(currentVolumes.Entries)).To(Equal(len(node.volumes)), "same volumes as before on node %s", nodeName)
				}
			})

			Context("ephemeral volumes", func() {
				doit := func(withFlag bool, repeatCalls int) {
					targetPath := sc.TargetPath + "/ephemeral"
					params := map[string]string{
						"size": "1Mi",
					}
					if withFlag {
						params["csi.storage.k8s.io/ephemeral"] = "true"
					}
					req := csi.NodePublishVolumeRequest{
						VolumeId:      "fake-ephemeral-volume-id",
						VolumeContext: params,
						VolumeCapability: &csi.VolumeCapability{
							AccessType: &csi.VolumeCapability_Mount{
								Mount: &csi.VolumeCapability_MountVolume{},
							},
							AccessMode: &csi.VolumeCapability_AccessMode{
								Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
							},
						},
						TargetPath: targetPath,
					}
					published := false
					var failedPublish, failedUnpublish error
					for i := 0; i < repeatCalls; i++ {
						_, err := nc.NodePublishVolume(context.Background(), &req)
						if err == nil {
							published = true
							break
						} else if failedPublish == nil {
							failedPublish = fmt.Errorf("NodePublishVolume for ephemeral volume, attempt #%d: %v", i, err)
						}
					}
					if published {
						req := csi.NodeUnpublishVolumeRequest{
							VolumeId:   "fake-ephemeral-volume-id",
							TargetPath: targetPath,
						}
						for i := 0; i < repeatCalls; i++ {
							_, err := nc.NodeUnpublishVolume(context.Background(), &req)
							if err != nil && failedUnpublish == nil {
								failedUnpublish = fmt.Errorf("NodeUnpublishVolume for ephemeral volume, attempt #%d: %v", i, err)
							}
						}
					}
					framework.ExpectNoError(failedPublish)
					framework.ExpectNoError(failedUnpublish)
				}

				doall := func(withFlag bool) {
					It("work", func() {
						doit(withFlag, 1)
					})

					It("are idempotent", func() {
						doit(withFlag, 10)
					})
				}

				Context("with csi.storage.k8s.io/ephemeral", func() {
					doall(true)
				})

				Context("without csi.storage.k8s.io/ephemeral", func() {
					doall(false)
				})
			})
		})
	})
})

type volume struct {
	namePrefix  string
	ctx         context.Context
	sc          *sanity.TestContext
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

func (v volume) create(sizeInBytes int64, nodeID string, expectedStatus ...codes.Code) (string, *csi.Volume) {
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
	if len(expectedStatus) > 0 {
		// Expected to fail, no retries.
		vol, err = v.cc.CreateVolume(v.ctx, req)
	} else {
		// With retries.
		err = v.retry(func() error {
			vol, err = v.cc.CreateVolume(
				v.ctx, req,
			)
			return err
		}, "CreateVolume")
	}
	v.cl.MaybeRegisterVolume(name, vol, err)
	if len(expectedStatus) > 0 {
		framework.ExpectError(err, create)
		status, ok := status.FromError(err)
		Expect(ok).To(BeTrue(), "have gRPC status error")
		Expect(status.Code()).To(Equal(expectedStatus[0]), "expected gRPC status code")
		return name, nil
	}
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

// retry will execute the operation (rapidly initially, then with
// exponential backoff) until it succeeds or the context times
// out. Each failure gets logged.
func (v volume) retry(operation func() error, what string) error {
	if v.ctx.Err() != nil {
		return fmt.Errorf("%s: not calling %s, the deadline has been reached already", v.namePrefix, what)
	}

	// Something failed. Retry with exponential backoff.
	// TODO: use wait.NewExponentialBackoffManager once we use K8S v1.18.
	backoff := NewExponentialBackoffManager(
		time.Second,    // initial backoff
		10*time.Second, // maximum backoff
		30*time.Second, // reset duration
		2,              // backoff factor
		0,              // no jitter
		clock.RealClock{})
	for i := 0; ; i++ {
		err := operation()
		if err == nil {
			return nil
		}
		framework.Logf("%s: %s failed at attempt %#d: %v", v.namePrefix, what, i, err)
		select {
		case <-v.ctx.Done():
			framework.Logf("%s: %s failed %d times and deadline exceeded, giving up after error: %v", v.namePrefix, what, i+1, err)
			return err
		case <-backoff.Backoff().C():
		}
	}
}

func canRestartNode(nodeID string) {
	if !regexp.MustCompile(`worker\d+$`).MatchString(nodeID) {
		skipper.Skipf("node %q not one of the expected QEMU nodes (worker<number>))", nodeID)
	}
}

// restartNode works only for one of the nodes in the QEMU virtual cluster.
// It does a hard poweroff via SysRq and relies on Docker to restart the
// "failed" node.
func restartNode(cs clientset.Interface, nodeID string, sc *sanity.TestContext) {
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
			pods, err = e2epod.WaitForPodsWithLabel(c, ns, label)
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
