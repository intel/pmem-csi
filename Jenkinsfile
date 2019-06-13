pipeline {

    agent {

        label "pmem-csi"
    }

    options {

        timeout(time: 2, unit: "HOURS")

    }

    environment {

        PMEM_PATH = "/go/src/github.com/intel/pmem-csi"
        IMAGE_VERSION = "ci-${env.BUILD_ID}"
        BUILD_IMAGE = "clearlinux-builder"
        CLUSTER= "ci-$IMAGE_VERSION"
        TEST_BOOTSTRAP_IMAGES="localhost:5000/pmem-csi-driver:$IMAGE_VERSION  localhost:5000/pmem-csi-driver-test:$IMAGE_VERSION"

    }

    stages {

        stage('Create build environment') {

            options {

                timeout(time: 60, unit: "MINUTES")

            }

            steps {

                sh 'docker version'
                sh 'docker build --target build --build-arg CACHEBUST=$IMAGE_VERSION -t $BUILD_IMAGE .'

             }

        }

        stage('make test') {

            options {

                timeout(time: 20, unit: "MINUTES")
                retry(3)

            }

            steps {

                sh 'docker run --rm \
                -v `pwd`:$PMEM_PATH \
                -w $PMEM_PATH \
                $BUILD_IMAGE \
                bash -c "make test"'

            }

        }

        stage('Build test image') {

            options {

                timeout(time: 60, unit: "MINUTES")
                retry(2)

            }

            steps {

                sh 'docker run --rm \
                    -e BUILD_IMAGE_ID=$IMAGE_VERSION \
                    -e IMAGE_VERSION=$IMAGE_VERSION \
                    -v /var/run/docker.sock:/var/run/docker.sock \
                    -v /usr/bin/docker:/usr/bin/docker \
                    -v `pwd`:$PMEM_PATH \
                    -w $PMEM_PATH \
                    $BUILD_IMAGE \
                    bash -c "make build-images"'

            }

        }

        stage('E2E') {

            options {

                timeout(time: 90, unit: "MINUTES")
                retry(2)

            }

            steps {

                    sh 'cd deploy; grep -rl ":canary"  | xargs -L1 sed -i "s|canary|$IMAGE_VERSION|" | echo "Change image tag in deployment files"'

                    /*
                     We have to run "make start" in the current directory
                     because the QEMU instances that it starts under Docker
                     run outside of the container. For "make test_e2e" we
                     then have to switch into the GOPATH. Once we can
                     build outside of the GOPATH, we can simplify that to
                     build inside one directory.
                    */
                    sh 'docker run --rm \
                        -e TEST_BOOTSTRAP_IMAGES="$TEST_BOOTSTRAP_IMAGES" \
                        -e GOVM_YAML=`pwd`/_work/$CLUSTER/deployment.yaml \
                        -e CLUSTER="$CLUSTER" \
                        -e TEST_CREATE_REGISTRY=true \
                        -e TEST_CHECK_SIGNED_FILES=false \
                        -v /var/run/docker.sock:/var/run/docker.sock \
                        -v `pwd`:$PMEM_PATH \
                        -v /usr/bin/docker:/usr/bin/docker \
                        -v `pwd`:`pwd` \
                        -w `pwd` \
                        $BUILD_IMAGE \
                        bash -c "swupd bundle-add openssh-server &&  \
                            make start && cd $PMEM_PATH && \
                            make test_e2e"'

            }

        }

    }

    post {

        always {

            sh 'docker run --rm \
                -v /var/run/docker.sock:/var/run/docker.sock \
                -v /usr/bin/docker:/usr/bin/docker \
                -v `pwd`:$PMEM_PATH \
                -e IMAGE_VERSION=$IMAGE_VERSION \
                -e CLUSTER=$CLUSTER \
                -w $PMEM_PATH \
		$BUILD_IMAGE \
                bash -c "make stop"'

            sh 'docker run --rm \
                -v `pwd`:$PMEM_PATH \
                -e CLUSTER=$CLUSTER \
                -e IMAGE_VERSION=$IMAGE_VERSION \
                -w $PMEM_PATH \
                $BUILD_IMAGE \
                bash -c "rm -rf _work _output; make clean"'

            deleteDir()

            sh 'docker rmi $TEST_BOOTSTRAP_IMAGES'

        }

    }

}
