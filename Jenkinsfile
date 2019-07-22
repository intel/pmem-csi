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
                    -e BUILD_IMAGE_ID=${env.CACHEBUST} \
                    -e 'BUILD_ARGS=--cache-from ${env.BUILD_IMAGE} --cache-from ${env.PMEM_CSI_IMAGE}' \
                    -e REGISTRY_NAME=${env.REGISTRY_NAME} \
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
                stage('testing 1.14 LVM') {
                    options {
                        timeout(time: 90, unit: "MINUTES")
                        retry(2)
                    }
                    environment {
                        CLUSTER = "clear-1-14-lvm"
                        TEST_DEVICE_MODE = "lvm"
                        TEST_DEPLOYMENT_MODE = "testing"
                        CLEAR_LINUX_VERSION = "${env.CLEAR_LINUX_VERSION_1_14}"
                    }
                    steps {
                        sh "docker run --rm \
                            -e GOVM_YAML=`pwd`/_work/$CLUSTER/deployment.yaml \
                            -e CLUSTER=${env.CLUSTER} \
                            -e TEST_BUILD_PMEM_REGISTRY=${env.REGISTRY_NAME} \
                            -e TEST_DEVICEMODE=${env.TEST_DEVICE_MODE} \
                            -e TEST_DEPLOYMENTMODE=${env.TEST_DEPLOYMENT_MODE} \
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
                    post {
                        always {
                            sh "docker run --rm \
                            -e GOVM_YAML=`pwd`/_work/$CLUSTER/deployment.yaml \
                            -e CLUSTER=${env.CLUSTER} \
                            -e TEST_BUILD_PMEM_REGISTRY=${env.REGISTRY_NAME} \
                            -e TEST_DEVICEMODE=${env.TEST_DEVICE_MODE} \
                            -e TEST_DEPLOYMENTMODE=${env.TEST_DEPLOYMENT_MODE} \
                            -e TEST_CREATE_REGISTRY=true \
                            -e TEST_CHECK_SIGNED_FILES=false \
                            -e TEST_CLEAR_LINUX_VERSION=${env.CLEAR_LINUX_VERSION} \
                            -v /var/run/docker.sock:/var/run/docker.sock \
                            -v `pwd`:$PMEM_PATH \
                            -v /usr/bin/docker:/usr/bin/docker \
                            -v `pwd`:`pwd` \
                            -w `pwd` \
                            ${env.BUILD_IMAGE} \
                            bash -c 'make stop'"
                        }
                    }
                }

                stage('testing 1.14 direct') {
                    options {
                        timeout(time: 180, unit: "MINUTES")
                        retry(2)
                    }
                    environment {
                        CLUSTER = "clear-1-14-direct"
                        TEST_DEVICE_MODE = "direct"
                        TEST_DEPLOYMENT_MODE = "testing"
                        CLEAR_LINUX_VERSION = "${env.CLEAR_LINUX_VERSION_1_14}"
                    }
                    steps {
                        sh "docker run --rm \
                            -e GOVM_YAML=`pwd`/_work/$CLUSTER/deployment.yaml \
                            -e CLUSTER=${env.CLUSTER} \
                            -e TEST_BUILD_PMEM_REGISTRY=${env.REGISTRY_NAME} \
                            -e TEST_DEVICEMODE=${env.TEST_DEVICE_MODE} \
                            -e TEST_DEPLOYMENTMODE=${env.TEST_DEPLOYMENT_MODE} \
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
                    post {
                        always {
                            sh "docker run --rm \
                            -e GOVM_YAML=`pwd`/_work/$CLUSTER/deployment.yaml \
                            -e CLUSTER=${env.CLUSTER} \
                            -e TEST_BUILD_PMEM_REGISTRY=${env.REGISTRY_NAME} \
                            -e TEST_DEVICEMODE=${env.TEST_DEVICE_MODE} \
                            -e TEST_DEPLOYMENTMODE=${env.TEST_DEPLOYMENT_MODE} \
                            -e TEST_CREATE_REGISTRY=true \
                            -e TEST_CHECK_SIGNED_FILES=false \
                            -e TEST_CLEAR_LINUX_VERSION=${env.CLEAR_LINUX_VERSION} \
                            -v /var/run/docker.sock:/var/run/docker.sock \
                            -v `pwd`:$PMEM_PATH \
                            -v /usr/bin/docker:/usr/bin/docker \
                            -v `pwd`:`pwd` \
                            -w `pwd` \
                            ${env.BUILD_IMAGE} \
                            bash -c 'make stop'"
                        }
                    }
                }

                stage('testing 1.13 LVM') {
                    when { not { changeRequest() } }
                    options {
                        timeout(time: 90, unit: "MINUTES")
                        retry(2)
                    }
                    environment {
                        CLUSTER = "clear-1-13-lvm"
                        TEST_DEVICE_MODE = "lvm"
                        TEST_DEPLOYMENT_MODE = "testing"
                        CLEAR_LINUX_VERSION = "${env.CLEAR_LINUX_VERSION_1_13}"
                    }
                    steps {
                        sh "docker run --rm \
                            -e GOVM_YAML=`pwd`/_work/$CLUSTER/deployment.yaml \
                            -e CLUSTER=${env.CLUSTER} \
                            -e TEST_BUILD_PMEM_REGISTRY=${env.REGISTRY_NAME} \
                            -e TEST_DEVICEMODE=${env.TEST_DEVICE_MODE} \
                            -e TEST_DEPLOYMENTMODE=${env.TEST_DEPLOYMENT_MODE} \
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
                    post {
                        always {
                            sh "docker run --rm \
                            -e GOVM_YAML=`pwd`/_work/$CLUSTER/deployment.yaml \
                            -e CLUSTER=${env.CLUSTER} \
                            -e TEST_BUILD_PMEM_REGISTRY=${env.REGISTRY_NAME} \
                            -e TEST_DEVICEMODE=${env.TEST_DEVICE_MODE} \
                            -e TEST_DEPLOYMENTMODE=${env.TEST_DEPLOYMENT_MODE} \
                            -e TEST_CREATE_REGISTRY=true \
                            -e TEST_CHECK_SIGNED_FILES=false \
                            -e TEST_CLEAR_LINUX_VERSION=${env.CLEAR_LINUX_VERSION} \
                            -v /var/run/docker.sock:/var/run/docker.sock \
                            -v `pwd`:$PMEM_PATH \
                            -v /usr/bin/docker:/usr/bin/docker \
                            -v `pwd`:`pwd` \
                            -w `pwd` \
                            ${env.BUILD_IMAGE} \
                            bash -c 'make stop'"
                        }
                    }
                }

                stage('testing 1.13 direct') {
                    when { not { changeRequest() } }
                    options {
                        timeout(time: 180, unit: "MINUTES")
                        retry(2)
                    }
                    environment {
                        CLUSTER = "clear-1-13-direct"
                        TEST_DEVICE_MODE = "direct"
                        TEST_DEPLOYMENT_MODE = "testing"
                        CLEAR_LINUX_VERSION = "${env.CLEAR_LINUX_VERSION_1_13}"
                    }
                    steps {
                        sh "docker run --rm \
                            -e GOVM_YAML=`pwd`/_work/$CLUSTER/deployment.yaml \
                            -e CLUSTER=${env.CLUSTER} \
                            -e TEST_BUILD_PMEM_REGISTRY=${env.REGISTRY_NAME} \
                            -e TEST_DEVICEMODE=${env.TEST_DEVICE_MODE} \
                            -e TEST_DEPLOYMENTMODE=${env.TEST_DEPLOYMENT_MODE} \
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
                    post {
                        always {
                            sh "docker run --rm \
                            -e GOVM_YAML=`pwd`/_work/$CLUSTER/deployment.yaml \
                            -e CLUSTER=${env.CLUSTER} \
                            -e TEST_BUILD_PMEM_REGISTRY=${env.REGISTRY_NAME} \
                            -e TEST_DEVICEMODE=${env.TEST_DEVICE_MODE} \
                            -e TEST_DEPLOYMENTMODE=${env.TEST_DEPLOYMENT_MODE} \
                            -e TEST_CREATE_REGISTRY=true \
                            -e TEST_CHECK_SIGNED_FILES=false \
                            -e TEST_CLEAR_LINUX_VERSION=${env.CLEAR_LINUX_VERSION} \
                            -v /var/run/docker.sock:/var/run/docker.sock \
                            -v `pwd`:$PMEM_PATH \
                            -v /usr/bin/docker:/usr/bin/docker \
                            -v `pwd`:`pwd` \
                            -w `pwd` \
                            ${env.BUILD_IMAGE} \
                            bash -c 'make stop'"
                        }
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
                    environment {
                        CLUSTER = "clear-1-14-lvm"
                        TEST_DEVICE_MODE = "lvm"
                        TEST_DEPLOYMENT_MODE = "production"
                        CLEAR_LINUX_VERSION = "${env.CLEAR_LINUX_VERSION_1_14}"
                    }
                    steps {
                        sh "docker run --rm \
                            -e GOVM_YAML=`pwd`/_work/$CLUSTER/deployment.yaml \
                            -e CLUSTER=${env.CLUSTER} \
                            -e TEST_BUILD_PMEM_REGISTRY=${env.REGISTRY_NAME} \
                            -e TEST_DEVICEMODE=${env.TEST_DEVICE_MODE} \
                            -e TEST_DEPLOYMENTMODE=${env.TEST_DEPLOYMENT_MODE} \
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
                    post {
                        always {
                            sh "docker run --rm \
                            -e GOVM_YAML=`pwd`/_work/$CLUSTER/deployment.yaml \
                            -e CLUSTER=${env.CLUSTER} \
                            -e TEST_BUILD_PMEM_REGISTRY=${env.REGISTRY_NAME} \
                            -e TEST_DEVICEMODE=${env.TEST_DEVICE_MODE} \
                            -e TEST_DEPLOYMENTMODE=${env.TEST_DEPLOYMENT_MODE} \
                            -e TEST_CREATE_REGISTRY=true \
                            -e TEST_CHECK_SIGNED_FILES=false \
                            -e TEST_CLEAR_LINUX_VERSION=${env.CLEAR_LINUX_VERSION} \
                            -v /var/run/docker.sock:/var/run/docker.sock \
                            -v `pwd`:$PMEM_PATH \
                            -v /usr/bin/docker:/usr/bin/docker \
                            -v `pwd`:`pwd` \
                            -w `pwd` \
                            ${env.BUILD_IMAGE} \
                            bash -c 'make stop'"
                        }
                    }
                }

                stage('production 1.14 direct') {
                    when { not { changeRequest() } }
                    options {
                        timeout(time: 30, unit: "MINUTES")
                        retry(2)
                    }
                    environment {
                        CLUSTER = "clear-1-14-direct"
                        TEST_DEVICE_MODE = "direct"
                        TEST_DEPLOYMENT_MODE = "production"
                        CLEAR_LINUX_VERSION = "${env.CLEAR_LINUX_VERSION_1_14}"
                    }
                    steps {
                        sh "docker run --rm \
                            -e GOVM_YAML=`pwd`/_work/$CLUSTER/deployment.yaml \
                            -e CLUSTER=${env.CLUSTER} \
                            -e TEST_BUILD_PMEM_REGISTRY=${env.REGISTRY_NAME} \
                            -e TEST_DEVICEMODE=${env.TEST_DEVICE_MODE} \
                            -e TEST_DEPLOYMENTMODE=${env.TEST_DEPLOYMENT_MODE} \
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
                    post {
                        always {
                            sh "docker run --rm \
                            -e GOVM_YAML=`pwd`/_work/$CLUSTER/deployment.yaml \
                            -e CLUSTER=${env.CLUSTER} \
                            -e TEST_BUILD_PMEM_REGISTRY=${env.REGISTRY_NAME} \
                            -e TEST_DEVICEMODE=${env.TEST_DEVICE_MODE} \
                            -e TEST_DEPLOYMENTMODE=${env.TEST_DEPLOYMENT_MODE} \
                            -e TEST_CREATE_REGISTRY=true \
                            -e TEST_CHECK_SIGNED_FILES=false \
                            -e TEST_CLEAR_LINUX_VERSION=${env.CLEAR_LINUX_VERSION} \
                            -v /var/run/docker.sock:/var/run/docker.sock \
                            -v `pwd`:$PMEM_PATH \
                            -v /usr/bin/docker:/usr/bin/docker \
                            -v `pwd`:`pwd` \
                            -w `pwd` \
                            ${env.BUILD_IMAGE} \
                            bash -c 'make stop'"
                        }
                    }
                }

                stage('production 1.13 LVM') {
                    when { not { changeRequest() } }
                    options {
                        timeout(time: 30, unit: "MINUTES")
                        retry(2)
                    }
                    environment {
                        CLUSTER = "clear-1-13-lvm"
                        TEST_DEVICE_MODE = "lvm"
                        TEST_DEPLOYMENT_MODE = "production"
                        CLEAR_LINUX_VERSION = "${env.CLEAR_LINUX_VERSION_1_13}"
                    }
                    steps {
                        sh "docker run --rm \
                            -e GOVM_YAML=`pwd`/_work/$CLUSTER/deployment.yaml \
                            -e CLUSTER=${env.CLUSTER} \
                            -e TEST_BUILD_PMEM_REGISTRY=${env.REGISTRY_NAME} \
                            -e TEST_DEVICEMODE=${env.TEST_DEVICE_MODE} \
                            -e TEST_DEPLOYMENTMODE=${env.TEST_DEPLOYMENT_MODE} \
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
                    post {
                        always {
                            sh "docker run --rm \
                            -e GOVM_YAML=`pwd`/_work/$CLUSTER/deployment.yaml \
                            -e CLUSTER=${env.CLUSTER} \
                            -e TEST_BUILD_PMEM_REGISTRY=${env.REGISTRY_NAME} \
                            -e TEST_DEVICEMODE=${env.TEST_DEVICE_MODE} \
                            -e TEST_DEPLOYMENTMODE=${env.TEST_DEPLOYMENT_MODE} \
                            -e TEST_CREATE_REGISTRY=true \
                            -e TEST_CHECK_SIGNED_FILES=false \
                            -e TEST_CLEAR_LINUX_VERSION=${env.CLEAR_LINUX_VERSION} \
                            -v /var/run/docker.sock:/var/run/docker.sock \
                            -v `pwd`:$PMEM_PATH \
                            -v /usr/bin/docker:/usr/bin/docker \
                            -v `pwd`:`pwd` \
                            -w `pwd` \
                            ${env.BUILD_IMAGE} \
                            bash -c 'make stop'"
                        }
                    }
                }

                stage('production 1.13 direct') {
                    when { not { changeRequest() } }
                    options {
                        timeout(time: 30, unit: "MINUTES")
                        retry(2)
                    }
                    environment {
                        CLUSTER = "clear-1-13-direct"
                        TEST_DEVICE_MODE = "direct"
                        TEST_DEPLOYMENT_MODE = "production"
                        CLEAR_LINUX_VERSION = "${env.CLEAR_LINUX_VERSION_1_13}"
                    }
                    steps {
                        sh "docker run --rm \
                            -e GOVM_YAML=`pwd`/_work/$CLUSTER/deployment.yaml \
                            -e CLUSTER=${env.CLUSTER} \
                            -e TEST_BUILD_PMEM_REGISTRY=${env.REGISTRY_NAME} \
                            -e TEST_DEVICEMODE=${env.TEST_DEVICE_MODE} \
                            -e TEST_DEPLOYMENTMODE=${env.TEST_DEPLOYMENT_MODE} \
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
                    post {
                        always {
                            sh "docker run --rm \
                            -e GOVM_YAML=`pwd`/_work/$CLUSTER/deployment.yaml \
                            -e CLUSTER=${env.CLUSTER} \
                            -e TEST_BUILD_PMEM_REGISTRY=${env.REGISTRY_NAME} \
                            -e TEST_DEVICEMODE=${env.TEST_DEVICE_MODE} \
                            -e TEST_DEPLOYMENTMODE=${env.TEST_DEPLOYMENT_MODE} \
                            -e TEST_CREATE_REGISTRY=true \
                            -e TEST_CHECK_SIGNED_FILES=false \
                            -e TEST_CLEAR_LINUX_VERSION=${env.CLEAR_LINUX_VERSION} \
                            -v /var/run/docker.sock:/var/run/docker.sock \
                            -v `pwd`:$PMEM_PATH \
                            -v /usr/bin/docker:/usr/bin/docker \
                            -v `pwd`:`pwd` \
                            -w `pwd` \
                            ${env.BUILD_IMAGE} \
                            bash -c 'make stop'"
                        }
                    }
                }

    stage('Push images') {
        when { not { changeRequest() } }
        steps {
                withDockerRegistry([ credentialsId: "e16bd38a-76cb-4900-a5cb-7f6aa3aeb22d", url: "https://${REGISTRY_NAME}" ]) {
                    sh "docker run --rm \
                        -e REGISTRY_NAME=${env.REGISTRY_NAME} \
                        -e DOCKER_CONFIG=$DOCKER_CONFIG \
                        -v /var/run/docker.sock:/var/run/docker.sock \
                        -v /usr/bin/docker:/usr/bin/docker \
                        -v `pwd`:${env.PMEM_PATH} \
                        -v $DOCKER_CONFIG:$DOCKER_CONFIG \
                        -w ${env.PMEM_PATH} \
                        ${env.BUILD_IMAGE} \
                        make push-images PUSH_IMAGE_DEP="
                    sh "docker image push ${env.BUILD_IMAGE}"
                }
            }
        }
    }
}
