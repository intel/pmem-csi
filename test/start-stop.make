# Brings up the emulator environment:
# - creates pmem NVDIMM files if necessary
# - starts QEMU virtual machines with pmem NVDIMMs as described in https://github.com/qemu/qemu/blob/bd54b11062c4baa7d2e4efadcf71b8cfd55311fd/docs/nvdimm.txt
# - starts a Kubernetes cluster
# - generate pmem secrets if necessary
# - installs pmem-csi driver
#
# It operates on the cluster in _work/$(CLUSTER).
start: _work/$(CLUSTER)/created _work/$(CLUSTER)/start-kubernetes _work/$(CLUSTER)/run-qemu _work/$(CLUSTER)/ssh test/setup-ca-kubernetes.sh _work/.setupcfssl-stamp
	. test/test-config.sh && \
	for i in $$(seq 0 $$(($(NUM_NODES) - 1))); do \
		if ! [ -e _work/$(CLUSTER)/qemu.$$i.pid ] || ! kill -0 $$(cat _work/$(CLUSTER)/qemu.$$i.pid) 2>/dev/null; then \
			memargs="-m $${TEST_NORMAL_MEM_SIZE}M"; \
			if [ $$i -ne 0 ]; then \
				pmemfile=_work/$(CLUSTER)/qemu.$$i.pmem.raw; \
				truncate -s $${TEST_PMEM_MEM_SIZE}M $$pmemfile; \
				memargs="$$memargs,slots=$${TEST_MEM_SLOTS},maxmem=$$(($${TEST_NORMAL_MEM_SIZE} + $${TEST_PMEM_MEM_SIZE}))M"; \
				memargs="$$memargs -object memory-backend-file,id=mem1,share=$${TEST_PMEM_SHARE},mem-path=$$pmemfile,size=$${TEST_PMEM_MEM_SIZE}M"; \
				memargs="$$memargs -device nvdimm,id=nvdimm1,memdev=mem1,label-size=$${TEST_PMEM_LABEL_SIZE}"; \
				memargs="$$memargs -machine pc,nvdimm"; \
			fi; \
			_work/$(CLUSTER)/run-qemu _work/$(CLUSTER)/qemu.$$i.img $$memargs -monitor none -serial file:_work/$(CLUSTER)/qemu.$$i.log $$opts & \
			echo $$! >_work/$(CLUSTER)/qemu.$$i.pid; \
		fi; \
	done
	while ! _work/$(CLUSTER)/ssh true 2>/dev/null; do \
		sleep 1; \
	done
	_work/$(CLUSTER)/start-kubernetes
	_work/$(CLUSTER)/ssh kubectl label node host-0 storage-
	for i in $$(seq 1 $$(($(NUM_NODES) - 1))); do \
		if ! [ -e _work/$(CLUSTER)/qemu.$$i.labelsdone ]; then \
			: "The first OS startup triggers the creation of one device-size pmem region and `ndctl` shows zero remaining available space." ; \
			: "To make emulated NVDIMMs usable by `ndctl`, we use labels initialization, which must be performed once after the first bootup" ; \
			: "with new device(s)." ; \
			_work/$(CLUSTER)/ssh.$$i "/usr/bin/ndctl disable-region region0"; \
			_work/$(CLUSTER)/ssh.$$i "/usr/bin/ndctl init-labels nmem0"; \
			_work/$(CLUSTER)/ssh.$$i "/usr/bin/ndctl enable-region region0"; \
			touch _work/$(CLUSTER)/qemu.$$i.labelsdone; \
		fi; \
		_work/$(CLUSTER)/ssh kubectl label --overwrite node host-$$i storage=pmem; \
	done
	if ! [ -e _work/$(CLUSTER)/secretsdone ] || [ $$(_work/$(CLUSTER)/ssh kubectl get secrets | grep -e pmem-csi-node-secrets -e pmem-csi-registry-secrets | wc -l) -ne 2 ]; then \
		KUBECTL="$(PWD)/_work/$(CLUSTER)/ssh kubectl" PATH='$(PWD)/_work/bin/:$(PATH)' ./test/setup-ca-kubernetes.sh && \
		touch _work/$(CLUSTER)/secretsdone; \
	fi
	_work/$(CLUSTER)/ssh kubectl version --short | grep 'Server Version' | sed -e 's/.*: v\([0-9]*\)\.\([0-9]*\)\..*/\1.\2/' >_work/$(CLUSTER)/kubernetes.version
	( . test/test-config.sh && _work/$(CLUSTER)/ssh kubectl apply -f - <deploy/kubernetes-$$(cat _work/$(CLUSTER)/kubernetes.version)/pmem-csi-$${TEST_DEVICEMODE}-testing.yaml )
	_work/$(CLUSTER)/ssh kubectl apply -f - <deploy/kubernetes-$$(cat _work/$(CLUSTER)/kubernetes.version)/pmem-storageclass-ext4.yaml
	_work/$(CLUSTER)/ssh kubectl apply -f - <deploy/kubernetes-$$(cat _work/$(CLUSTER)/kubernetes.version)/pmem-storageclass-xfs.yaml
	_work/$(CLUSTER)/ssh kubectl apply -f - <deploy/kubernetes-$$(cat _work/$(CLUSTER)/kubernetes.version)/pmem-storageclass-cache.yaml
	@ echo
	@ echo "The test cluster is ready. Log in with _work/$(CLUSTER)/ssh, run kubectl once logged in."
	@ echo "Alternatively, KUBECONFIG=$$(pwd)/_work/$(CLUSTER)/kube.config can also be used directly."
	@ echo "To try out the pmem-csi driver persistent volumes:"
	@ echo "   cat deploy/kubernetes-$$(cat _work/$(CLUSTER)/kubernetes.version)/pmem-pvc.yaml | _work/$(CLUSTER)/ssh kubectl create -f -"
	@ echo "   cat deploy/kubernetes-$$(cat _work/$(CLUSTER)/kubernetes.version)/pmem-app.yaml | _work/$(CLUSTER)/ssh kubectl create -f -"
	@ echo "To try out the pmem-csi driver cache volumes:"
	@ echo "   cat deploy/kubernetes-$$(cat _work/$(CLUSTER)/kubernetes.version)/pmem-pvc-cache.yaml | _work/$(CLUSTER)/ssh kubectl create -f -"
	@ echo "   cat deploy/kubernetes-$$(cat _work/$(CLUSTER)/kubernetes.version)/pmem-app-cache.yaml | _work/$(CLUSTER)/ssh kubectl create -f -"

stop:
	for i in $$(seq 0 $$(($(NUM_NODES) - 1))); do \
		if [ -e _work/$(CLUSTER)/qemu.$$i.pid ]; then \
			kill -9 $$(cat _work/$(CLUSTER)/qemu.$$i.pid) 2>/dev/null; \
			rm -f _work/$(CLUSTER)/qemu.$$i.pid; \
		fi; \
	done
