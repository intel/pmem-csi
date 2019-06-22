pipeline {

    agent {

        label "pmem-csi"
    }

    options {

        timeout(time: 2, unit: "HOURS")

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
        BUILD_IMAGE = "clearlinux-builder"
    }

    stages {

        stage('Create build environment') {

            options {

                timeout(time: 60, unit: "MINUTES")

            }

            steps {

                sh 'docker version'
                sh "docker build --target build --build-arg CACHEBUST=${env.BUILD_ID} -t ${env.BUILD_IMAGE} ."

             }

        }

        stage('make test') {

            options {

                timeout(time: 20, unit: "MINUTES")
                retry(3)

            }

            steps {

                sh "docker run --rm \
                -v `pwd`:${env.PMEM_PATH} \
                -w $PMEM_PATH \
                ${env.BUILD_IMAGE} \
                make test"
            }

        }

        stage('Build test image') {

            options {

                timeout(time: 60, unit: "MINUTES")
                retry(2)

            }

            steps {

                sh "docker run --rm \
                    -e BUILD_IMAGE_ID=${env.BUILD_ID} \
                    -v /var/run/docker.sock:/var/run/docker.sock \
                    -v /usr/bin/docker:/usr/bin/docker \
                    -v `pwd`:${env.PMEM_PATH} \
                    -w ${env.PMEM_PATH} \
                    ${env.BUILD_IMAGE} \
                    make build-images"

            }

        }

            /*
             We have to run "make start" in the current directory
             because the QEMU instances that it starts under Docker
             run outside of the container. For "make test_e2e" we
             then have to switch into the GOPATH. Once we can
             build outside of the GOPATH, we can simplify that to
             build inside one directory.

             TODO: test in parallel (on different nodes? single node didn't work,
             https://github.com/intel/pmem-CSI/pull/309#issuecomment-504659383)

             TODO: avoid cut-and-paste - all steps are identical except for the environment
            */
                stage('1.14 LVM') {
                    options {
                        timeout(time: 90, unit: "MINUTES")
                    }
                    environment {
                        CLUSTER = "clear-1-14-lvm"
                        TEST_DEVICE_MODE = "lvm"
                        CLEAR_LINUX_VERSION = "${env.CLEAR_LINUX_VERSION_1_14}"
                    }
                    steps {
                        sh "docker run --rm \
                            -e GOVM_YAML=`pwd`/_work/$CLUSTER/deployment.yaml \
                            -e CLUSTER=${env.CLUSTER} \
                            -e TEST_DEVICEMODE=${env.TEST_DEVICE_MODE} \
                            -e TEST_CREATE_REGISTRY=true \
                            -e TEST_CHECK_SIGNED_FILES=false \
                            -e TEST_CLEAR_LINUX_VERSION=${env.CLEAR_LINUX_VERSION} \
                            -v /var/run/docker.sock:/var/run/docker.sock \
                            -v `pwd`:$PMEM_PATH \
                            -v /usr/bin/docker:/usr/bin/docker \
                            -v `pwd`:`pwd` \
                            -w `pwd` \
                            ${env.BUILD_IMAGE} \
                            bash -c 'swupd bundle-add openssh-server &&  \
                                make start && cd ${env.PMEM_PATH} && \
                                make test_e2e'"
                    }
                }

                stage('1.14 direct') {
                    options {
                        timeout(time: 90, unit: "MINUTES")
                    }
                    environment {
                        CLUSTER = "clear-1-14-direct"
                        TEST_DEVICE_MODE = "direct"
                        CLEAR_LINUX_VERSION = "${env.CLEAR_LINUX_VERSION_1_14}"
                    }
                    steps {
                        sh "docker run --rm \
                            -e GOVM_YAML=`pwd`/_work/$CLUSTER/deployment.yaml \
                            -e CLUSTER=${env.CLUSTER} \
                            -e TEST_DEVICEMODE=${env.TEST_DEVICE_MODE} \
                            -e TEST_CREATE_REGISTRY=true \
                            -e TEST_CHECK_SIGNED_FILES=false \
                            -e TEST_CLEAR_LINUX_VERSION=${env.CLEAR_LINUX_VERSION} \
                            -v /var/run/docker.sock:/var/run/docker.sock \
                            -v `pwd`:$PMEM_PATH \
                            -v /usr/bin/docker:/usr/bin/docker \
                            -v `pwd`:`pwd` \
                            -w `pwd` \
                            ${env.BUILD_IMAGE} \
                            bash -c 'swupd bundle-add openssh-server &&  \
                                make start && cd ${env.PMEM_PATH} && \
                                make test_e2e'"

                    }
                }

                stage('1.13 LVM') {
                    options {
                        timeout(time: 90, unit: "MINUTES")
                    }
                    environment {
                        CLUSTER = "clear-1-13-lvm"
                        TEST_DEVICE_MODE = "lvm"
                        CLEAR_LINUX_VERSION = "${env.CLEAR_LINUX_VERSION_1_13}"
                    }
                    steps {
                        sh "docker run --rm \
                            -e GOVM_YAML=`pwd`/_work/$CLUSTER/deployment.yaml \
                            -e CLUSTER=${env.CLUSTER} \
                            -e TEST_DEVICEMODE=${env.TEST_DEVICE_MODE} \
                            -e TEST_CREATE_REGISTRY=true \
                            -e TEST_CHECK_SIGNED_FILES=false \
                            -e TEST_CLEAR_LINUX_VERSION=${env.CLEAR_LINUX_VERSION} \
                            -v /var/run/docker.sock:/var/run/docker.sock \
                            -v `pwd`:$PMEM_PATH \
                            -v /usr/bin/docker:/usr/bin/docker \
                            -v `pwd`:`pwd` \
                            -w `pwd` \
                            ${env.BUILD_IMAGE} \
                            bash -c 'swupd bundle-add openssh-server &&  \
                                make start && cd ${env.PMEM_PATH} && \
                                make test_e2e'"
                    }
                }

                stage('1.13 direct') {
                    options {
                        timeout(time: 90, unit: "MINUTES")
                    }
                    environment {
                        CLUSTER = "clear-1-13-direct"
                        TEST_DEVICE_MODE = "direct"
                        CLEAR_LINUX_VERSION = "${env.CLEAR_LINUX_VERSION_1_13}"
                    }
                    steps {
                        sh "docker run --rm \
                            -e GOVM_YAML=`pwd`/_work/$CLUSTER/deployment.yaml \
                            -e CLUSTER=${env.CLUSTER} \
                            -e TEST_DEVICEMODE=${env.TEST_DEVICE_MODE} \
                            -e TEST_CREATE_REGISTRY=true \
                            -e TEST_CHECK_SIGNED_FILES=false \
                            -e TEST_CLEAR_LINUX_VERSION=${env.CLEAR_LINUX_VERSION} \
                            -v /var/run/docker.sock:/var/run/docker.sock \
                            -v `pwd`:$PMEM_PATH \
                            -v /usr/bin/docker:/usr/bin/docker \
                            -v `pwd`:`pwd` \
                            -w `pwd` \
                            ${env.BUILD_IMAGE} \
                            bash -c 'swupd bundle-add openssh-server &&  \
                                make start && cd ${env.PMEM_PATH} && \
                                make test_e2e'"

                    }
                }
    }
}
