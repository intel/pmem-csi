pipeline {
    agent {
        label "pmem-csi"
    }

    environment {
        /*
          For each major Kubernetes release we need one version of Clear Linux
          which had that release. Installing different Kubernetes releases
          on the latest Clear Linux is not supported because we always
          use the Clear Linux kubelet, and a more recent kubelet than
          the control plane is unsupported.
        */

        /* 29890 broke networking
        (https://github.com/clearlinux/distribution/issues/904). In
        29880, Docker forgets containers after a system restart
        (https://github.com/clearlinux/distribution/issues/891). We
        need to stay on the latest known-good version. The version
        between *20 and *80 have not been tested. */
        CLEAR_LINUX_VERSION_1_14 = "29820"

        /* last version before the 1.14 update in 28630 */
        CLEAR_LINUX_VERSION_1_13 = "28620"

        PMEM_PATH = "/go/src/github.com/intel/pmem-csi"
        REGISTRY_NAME = "cloud-native-image-registry.westus.cloudapp.azure.com"
        // Per-branch build environment, marked as "do not promote to public registry".
        // Set below via a script, must *not* be set here as it can't be overwritten.
        // BUILD_IMAGE = ""
        // This image is pulled at the beginning and used as cache.
        // TODO: Here we use "canary" which is correct for the "devel" branch, but other
        // branches may need something else to get better caching.
        PMEM_CSI_IMAGE = "${env.REGISTRY_NAME}/pmem-csi-driver:canary"
    }

    stages {
        stage('Create build environment') {
            options {
                timeout(time: 60, unit: "MINUTES")
            }

            steps {
                sh 'docker version'
                withDockerRegistry([ credentialsId: "e16bd38a-76cb-4900-a5cb-7f6aa3aeb22d", url: "https://${REGISTRY_NAME}" ]) {
                    script {
                        if (env.CHANGE_ID != null) {
                            env.BUILD_IMAGE = "${env.REGISTRY_NAME}/pmem-clearlinux-builder:${env.CHANGE_TARGET}-rejected"

                            // Pull previous image and use it as cache (https://andrewlock.net/caching-docker-layers-on-serverless-build-hosts-with-multi-stage-builds---target,-and---cache-from/).
                            sh ( script: "docker image pull ${env.BUILD_IMAGE} || true")
                            sh ( script: "docker image pull ${env.PMEM_CSI_IMAGE} || true")

                            // PR jobs need to use the same CACHEBUST value as the latest build for their
                            // target branch, otherwise they cannot reuse the cached layers. Another advantage
                            // is that they use a version of Clear Linux that is known to work, because "swupd update"
                            // will be cached.
                            env.CACHEBUST = sh ( script: "docker inspect -f '{{ .Config.Labels.cachebust }}' ${env.BUILD_IMAGE} 2>/dev/null || true", returnStdout: true).trim()
                        } else {
                            env.BUILD_IMAGE = "${env.REGISTRY_NAME}/pmem-clearlinux-builder:${env.BRANCH_NAME}-rejected"
                        }

                        if (env.CACHEBUST == null || env.CACHEBUST == "") {
                            env.CACHEBUST = env.BUILD_ID
                        }
                    }
                    sh "docker build --cache-from ${env.BUILD_IMAGE} --label cachebust=${env.CACHEBUST} --target build --build-arg CACHEBUST=${env.CACHEBUST} -t ${env.BUILD_IMAGE} ."
                }
            }
        }

        stage('make test') {
            options {
                timeout(time: 20, unit: "MINUTES")
            }

            steps {
                sh "docker run --rm ${DockerBuildArgs()} ${env.BUILD_IMAGE} make test"
            }
        }

        stage('Build test image') {
            options {
                timeout(time: 60, unit: "MINUTES")
                retry(2)
            }

            steps {
                sh "docker run --rm ${DockerBuildArgs()} ${env.BUILD_IMAGE} make build-images"
            }
        }

        stage('testing 1.14 LVM') {
            options {
                timeout(time: 90, unit: "MINUTES")
                retry(2)
            }
            steps {
                TestInVM("lvm", "testing", "${env.CLEAR_LINUX_VERSION_1_14}")
            }
        }

        stage('testing 1.14 direct') {
            options {
                timeout(time: 180, unit: "MINUTES")
                retry(2)
            }
            steps {
                TestInVM("direct", "testing", "${env.CLEAR_LINUX_VERSION_1_14}")
            }
        }

        stage('testing 1.13 LVM') {
            when { not { changeRequest() } }
            options {
                timeout(time: 90, unit: "MINUTES")
                retry(2)
            }
            steps {
                TestInVM("lvm", "testing", "${env.CLEAR_LINUX_VERSION_1_13}")
            }
        }

        stage('testing 1.13 direct') {
            when { not { changeRequest() } }
            options {
                timeout(time: 180, unit: "MINUTES")
                retry(2)
            }
            steps {
                TestInVM("direct", "testing", "${env.CLEAR_LINUX_VERSION_1_13}")
            }
        }

        /*
          In production we can only run E2E testing, no sanity testing.
          Therefore it is faster.
        */

        stage('production 1.14 LVM') {
            when { not { changeRequest() } }
            options {
                timeout(time: 30, unit: "MINUTES")
                retry(2)
            }
            steps {
                TestInVM("lvm", "production", "${env.CLEAR_LINUX_VERSION_1_14}")
            }
        }

        stage('production 1.14 direct') {
            when { not { changeRequest() } }
            options {
                timeout(time: 30, unit: "MINUTES")
                retry(2)
            }
            steps {
                TestInVM("direct", "production", "${env.CLEAR_LINUX_VERSION_1_14}")
            }
        }

        stage('production 1.13 LVM') {
            when { not { changeRequest() } }
            options {
                timeout(time: 30, unit: "MINUTES")
                retry(2)
            }
            steps {
                TestInVM("lvm", "production", "${env.CLEAR_LINUX_VERSION_1_13}")
            }
        }

        stage('production 1.13 direct') {
            when { not { changeRequest() } }
            options {
                timeout(time: 30, unit: "MINUTES")
                retry(2)
            }
            steps {
                TestInVM("direct", "production", "${env.CLEAR_LINUX_VERSION_1_13}")
            }
        }

        stage('Push images') {
            when { not { changeRequest() } }
            steps {
                withDockerRegistry([ credentialsId: "e16bd38a-76cb-4900-a5cb-7f6aa3aeb22d", url: "https://${REGISTRY_NAME}" ]) {
                    // Push PMEM-CSI images without rebuilding them.
                    // When building a tag, we expect the code to contain that version as image version.
                    // When building a branch, we expect "canary" for the "devel" branch and (currently) don't publish
                    // canary images for other branches.
                    // This relies on GIT_LOCAL_BRANCH, which despite its name contains the tag name respectively the branch name.
                    sh "imageversion=\$(docker run --rm ${DockerBuildArgs()} ${env.BUILD_IMAGE} make print-image-version) && \
                        expectedversion=\$(echo '${GIT_LOCAL_BRANCH}' | sed -e 's/devel/canary/') && \
                        if [ \"\$imageversion\" = \"\$expectedversion\" ] ; then \
                            docker run --rm ${DockerBuildArgs()} -e DOCKER_CONFIG=$DOCKER_CONFIG -v $DOCKER_CONFIG:$DOCKER_CONFIG ${env.BUILD_IMAGE} make push-images PUSH_IMAGE_DEP=; \
                        else \
                            echo \"Skipping the pushing of PMEM-CSI driver images with version \$imageversion because this build is for ${GIT_LOCAL_BRANCH}.\"; \
                        fi"
                    // Also push the build image, for later reuse in PR jobs.
                    sh "docker image push ${env.BUILD_IMAGE}"
                }
            }
        }
    }
}

/*
 "docker run" parameters which:
 - make the Docker instance on the host available inside a container (socket and command)
 - set common Makefile values (cachebust, cache populated from images if available)
 - source in $GOPATH as current directory

 A function is used because a variable, even one which uses a closure with lazy evaluation,
 didn't actually result in a string with all variables replaced by the current values.
 Do not use lazy evaluation inside the function, that caused steps which use
 this function to get skipped silently?!
*/
String DockerBuildArgs() {
    "\
    -e BUILD_IMAGE_ID=${env.CACHEBUST} \
    -e 'BUILD_ARGS=--cache-from ${env.BUILD_IMAGE} --cache-from ${env.PMEM_CSI_IMAGE}' \
    -e REGISTRY_NAME=${env.REGISTRY_NAME} \
    -v /var/run/docker.sock:/var/run/docker.sock \
    -v /usr/bin/docker:/usr/bin/docker \
    -v `pwd`:${env.PMEM_PATH} \
    -w ${env.PMEM_PATH} \
    "
}

void TestInVM(deviceMode, deploymentMode, clearVersion) {
    try {
        /*
        We have to run "make start" in the current directory
        because the QEMU instances that it starts under Docker
        run outside of the container and thus paths used inside
        the container have to be the same as outside.

        For "make test_e2e" we then have to switch into the
        GOPATH. Once we can build outside of the GOPATH, we can
        simplify that to build inside one directory.

        TODO: test in parallel (on different nodes? single node didn't work,
        https://github.com/intel/pmem-CSI/pull/309#issuecomment-504659383)
        */
        sh " \
           docker run --rm \
                  -e CLUSTER=clear \
                  -e GOVM_YAML=`pwd`/_work/clear/deployment.yaml \
                  -e TEST_BUILD_PMEM_REGISTRY=${env.REGISTRY_NAME} \
                  -e TEST_DEVICEMODE=${deviceMode} \
                  -e TEST_DEPLOYMENTMODE=${deploymentMode} \
                  -e TEST_CREATE_REGISTRY=true \
                  -e TEST_CHECK_SIGNED_FILES=false \
                  -e TEST_CLEAR_LINUX_VERSION=${clearVersion} \
                  ${DockerBuildArgs()} \
                  -v `pwd`:`pwd` \
                  -w `pwd` \
                  ${env.BUILD_IMAGE} \
                  bash -c 'swupd bundle-add openssh-server && \
                           make start && cd ${env.PMEM_PATH} && \
                           make test_e2e' \
           "
    } finally {
        // Always shut down the cluster to free up resources. As in "make start", we have to expose
        // the path as used on the host also inside the containner, but we don't need to be in it.
        sh "docker run --rm -e CLUSTER=clear ${DockerBuildArgs()} -v `pwd`:`pwd` ${env.BUILD_IMAGE} make stop"
    }
}
