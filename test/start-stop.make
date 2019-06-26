# Build a suitable https://github.com/govm-project/govm version.
GOVM_VERSION=1166148359ed9b4b83df555e528aad3cd1144ed3
_work/govm-$(GOVM_VERSION):
	mkdir -p _work/bin
	tmpdir=`mktemp -d` && \
	trap 'rm -r $$tmpdir' EXIT && \
	cd $$tmpdir && \
	echo "module govm" > go.mod && \
	go get -v github.com/govm-project/govm@$(GOVM_VERSION) && \
	go build -o $(abspath $@) github.com/govm-project/govm
	ln -sf ../$(@F) _work/bin/govm

# Brings up the emulator environment:
# - starts a Kubernetes cluster with NVDIMMs as described in https://github.com/qemu/qemu/blob/bd54b11062c4baa7d2e4efadcf71b8cfd55311fd/docs/nvdimm.txt
# - generate pmem secrets if necessary
start: _work/.setupcfssl-stamp _work/govm-$(GOVM_VERSION)
	PATH="$(PWD)/_work/bin:$$PATH" test/start-kubernetes.sh
	if ! [ -e _work/$(CLUSTER)/secretsdone ] || [ $$(_work/$(CLUSTER)/ssh-$(CLUSTER) kubectl get secrets | grep -e pmem-csi-node-secrets -e pmem-csi-registry-secrets | wc -l) -ne 2 ]; then \
		KUBECTL="$(PWD)/_work/$(CLUSTER)/ssh-$(CLUSTER) kubectl" PATH="$(PWD)/_work/bin:$$PATH" ./test/setup-ca-kubernetes.sh && \
		touch _work/$(CLUSTER)/secretsdone; \
	fi
	test/setup-deployment.sh

# Stops the VMs and removes all files.
stop: _work/govm-$(GOVM_VERSION)
	@ if [ -f _work/$(CLUSTER)/stop.sh ]; then \
		PATH="$(PWD)/_work/bin:$$PATH" _work/$(CLUSTER)/stop.sh; \
	else \
		echo "Cluster $(CLUSTER) was already removed."; \
	fi

# Clean shutdown of all VMs, then waits for reboot of cluster.
restart:
	@ if [ -f _work/$(CLUSTER)/stop.sh ]; then \
		_work/$(CLUSTER)/restart.sh; \
	else \
		echo "Cluster $(CLUSTER) is not running, it cannot be restarted."; \
		exit 1; \
	fi
