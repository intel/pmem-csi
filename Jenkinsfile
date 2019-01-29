pipeline {
  agent {
    node {
      label 'bare-metal'
    }

  }
  stages {
    stage('Build') {
      steps {
        sh 'docker run -it --rm -v `pwd`:/go/src/github.com/intel/pmem-csi pmem-csi-ci:go-alpine  make'
      }
    }
    stage('Test') {
      steps {
        sh 'docker run --rm -v `pwd`:/go/src/github.com/intel/pmem-csi pmem-csi-ci:go-alpine  make test'
      }
    }
    stage('Build Docker Images') {
      steps {
        sh 'make build-images'
      }
    }
    stage('Clean') {
      steps {
        sh 'make clean'
      }
    }
  }
}