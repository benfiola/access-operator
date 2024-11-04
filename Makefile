ASSETS ?= $(shell pwd)/.dev
DEV ?= $(shell pwd)/dev
CONTROLLER_GEN_VERSION ?= 0.16.4
CRANE_VERSION ?= 0.20.2
HELM_VERSION ?= 3.16.1
HELM_DOCS_VERSION ?= 1.14.2
KUBERNETES_VERSION ?= 1.30.5
MINIKUBE_VERSION ?= 1.34.0
ROUTEROS_VERSION ?= 7.16

OS = $(shell go env GOOS)
ARCH = $(shell go env GOARCH)

ALTARCH = $(ARCH)
ifeq ($(ALTARCH),amd64)
	ALTARCH = x86_64
endif

CILIUM_MANIFEST = $(ASSETS)/cilium.yaml
CILIUM_MANIFEST_SRC = $(DEV)/manifests/cilium
CONTROLLER_GEN = $(ASSETS)/controller-gen
CONTROLLER_GEN_URL = https://github.com/kubernetes-sigs/controller-tools/releases/download/v$(CONTROLLER_GEN_VERSION)/controller-gen-$(OS)-$(ARCH)
CRANE = $(ASSETS)/crane
CRANE_CMD = env $(CRANE)
CRANE_URL = https://github.com/google/go-containerregistry/releases/download/v$(CRANE_VERSION)/go-containerregistry_$(OS)_$(ALTARCH).tar.gz
CRDS_MANIFEST = $(ASSETS)/crds.yaml
CRDS_MANIFEST_SRC = $(DEV)/manifests/crds
HELM = $(ASSETS)/helm
HELM_CMD = env $(HELM)
HELM_URL = https://get.helm.sh/helm-v$(HELM_VERSION)-$(OS)-$(ARCH).tar.gz
HELM_DOCS = $(ASSETS)/helm-docs
HELM_DOCS_CMD = env $(HELM_DOCS)
HELM_DOCS_URL = https://github.com/norwoodj/helm-docs/releases/download/v$(HELM_DOCS_VERSION)/helm-docs_$(HELM_DOCS_VERSION)_$(OS)_$(ALTARCH).tar.gz
KUBECONFIG = $(ASSETS)/kube-config.yaml
KUBECTL = $(ASSETS)/kubectl
KUBECTL_CMD = env KUBECONFIG=$(KUBECONFIG) $(ASSETS)/kubectl
KUBECTL_URL = https://dl.k8s.io/release/v$(KUBERNETES_VERSION)/bin/$(OS)/$(ARCH)/kubectl
KUSTOMIZE_CMD = $(KUBECTL_CMD) kustomize --enable-helm --load-restrictor=LoadRestrictionsNone
MINIKUBE = $(ASSETS)/minikube
MINIKUBE_CMD = env KUBECONFIG=$(KUBECONFIG) MINIKUBE_HOME=$(ASSETS)/.minikube $(MINIKUBE)
MINIKUBE_TUNNEL_LOG = $(ASSETS)/.minikube/tunnel.log
MINIKUBE_URL = https://github.com/kubernetes/minikube/releases/download/v$(MINIKUBE_VERSION)/minikube-$(OS)-$(ARCH)
RESOURCES_MANIFEST = $(ASSETS)/resources.yaml
RESOURCES_MANIFEST_SRC = $(DEV)/manifests/resources

.PHONY: default
default: 

.PHONY: clean
clean: delete-minikube-cluster
	# delete asset directory
	rm -rf $(ASSETS)

.PHONY: dev-env
dev-env: create-minikube-cluster apply-manifests start-minikube-tunnel start-routeros wait-for-ready 

.PHONY: e2e-test
e2e-test:
	go test -count=1 -v ./internal/e2e

.PHONY: create-minikube-cluster
create-minikube-cluster: $(MINIKUBE)
	# create minikube cluster
	$(MINIKUBE_CMD) start --force --kubernetes-version=$(KUBERNETES_VERSION)
	# enable ingress addon
	$(MINIKUBE_CMD) addons enable ingress

.PHONY: delete-minikube-cluster
delete-minikube-cluster: $(MINIKUBE)
	# delete minikube cluster
	$(MINIKUBE_CMD) delete || true

.PHONY: start-minikube-tunnel
start-minikube-tunnel: $(LB_HOSTS_MANAGER)
	# send SIGTERM to existing minikube tunnels
	pkill -x -f '$(MINIKUBE) tunnel' || true
	# wait for existing minikube tunnels to exit
	while true; do pgrep -x -f '$(MINIKUBE) tunnel' || break; sleep 1; done
	# launch minikube tunnel
	nohup $(MINIKUBE_CMD) tunnel > $(MINIKUBE_TUNNEL_LOG) 2>&1 &

.PHONY: start-routeros
start-routeros:
	# stop existing routeros container
	docker stop routeros || true
	# remove existing routeros container
	docker rm routeros || true
	# start routeros container
	docker run --name=routeros --rm --detach --publish=80:80 --publish=8728:8728 --cap-add=NET_ADMIN --device=/dev/net/tun --platform=linux/amd64 evilfreelancer/docker-routeros:$(ROUTEROS_VERSION) -cpu qemu64	

.PHONY: wait-for-ready
wait-for-ready:
	# wait for routeros to be connectable
	while true; do curl --max-time 2 -I http://localhost:80 && break; sleep 1; done;

.PHONY: generate
generate: $(CONTROLLER_GEN)
	# generate deepcopy
	$(CONTROLLER_GEN) object paths=./pkg/api/bfiola.dev/v1
	# clean generated crds files
	rm -rf ./charts/crds/generated
	mkdir -p ./charts/crds/generated
	# generate crds
	$(CONTROLLER_GEN) crd paths=./... output:stdout > ./charts/crds/generated/crds.yaml
	# clean generated operator files
	rm -rf ./charts/operator/generated
	mkdir -p ./charts/operator/generated
	# generate operator rbac
	$(CONTROLLER_GEN) rbac:roleName=__rolename__ paths=./internal/operator/... output:stdout > ./charts/operator/generated/operator-rbac.yaml
	# generate server rbac
	$(CONTROLLER_GEN) rbac:roleName=__rolename__ paths=./internal/server/... output:stdout > ./charts/operator/generated/server-rbac.yaml
	# clean generated static files
	rm -rf ./internal/embed/static
	mkdir -p ./internal/embed/static
	# generate frontend
	cd frontend && yarn install && yarn exec vite build --outDir ../internal/embed/static --emptyOutDir

$(ASSETS):
	# create .dev directory
	mkdir -p $(ASSETS)

.PHONY: install-tools
install-tools: $(CONTROLLER_GEN) $(CRANE) $(HELM) $(HELM_DOCS) $(KUBECTL) $(MINIKUBE)

$(CONTROLLER_GEN): | $(ASSETS)
	# install controller-gen
	# download
	curl -o $(CONTROLLER_GEN) -fsSL $(CONTROLLER_GEN_URL)
	# make executable
	chmod +x $(CONTROLLER_GEN)

$(CRANE): | $(ASSETS)
	# install crane
	# create extract directory
	mkdir -p $(ASSETS)/.tmp
	# download archive
	curl -o $(ASSETS)/.tmp/archive.tar.gz -fsSL $(CRANE_URL)
	# extract archive
	tar xzf $(ASSETS)/.tmp/archive.tar.gz -C $(ASSETS)/.tmp
	# copy executable
	cp $(ASSETS)/.tmp/crane $(CRANE)
	# delete extract directory
	rm -rf $(ASSETS)/.tmp

$(HELM): | $(ASSETS)
	# install helm
	# create extract directory
	mkdir -p $(ASSETS)/.tmp
	# download archive
	curl -o $(ASSETS)/.tmp/archive.tar.gz -fsSL $(HELM_URL)
	# extract archive
	tar xzf $(ASSETS)/.tmp/archive.tar.gz -C $(ASSETS)/.tmp --strip-components 1
	# copy executable
	cp $(ASSETS)/.tmp/helm $(ASSETS)/helm
	# delete extract directory
	rm -rf $(ASSETS)/.tmp

$(HELM_DOCS): | $(ASSETS)
	# install helm-docs
	# create extract directory
	mkdir -p $(ASSETS)/.tmp
	# download archive
	curl -o $(ASSETS)/.tmp/archive.tar.gz -fsSL $(HELM_DOCS_URL)
	# extract archive
	tar xzf $(ASSETS)/.tmp/archive.tar.gz -C $(ASSETS)/.tmp
	# copy executable
	cp $(ASSETS)/.tmp/helm-docs $(HELM_DOCS)
	# delete extract directory
	rm -rf $(ASSETS)/.tmp

$(KUBECTL): | $(ASSETS)
	# install kubectl
	# download
	curl -o $(KUBECTL) -fsSL $(KUBECTL_URL)
	# make kubectl executable
	chmod +x $(KUBECTL)

$(MINIKUBE): | $(ASSETS)
	# install minikube
	# download
	curl -o $(MINIKUBE) -fsSL $(MINIKUBE_URL)
	# make executable
	chmod +x $(MINIKUBE)

.PHONY: apply-manifests
apply-manifests: $(CILIUM_MANIFEST) $(CRDS_MANIFEST) $(KUBECTL) $(RESOURCES_MANIFEST)
	# deploy crds
	$(KUBECTL_CMD) apply -f $(CRDS_MANIFEST)
	# deploy cilium
	$(KUBECTL_CMD) apply -f $(CILIUM_MANIFEST)
	# deploy resources
	$(KUBECTL_CMD) apply -f $(RESOURCES_MANIFEST)

$(CILIUM_MANIFEST): $(KUBECTL) | $(ASSETS)
	# generate cilium manifest
	$(KUSTOMIZE_CMD) $(CILIUM_MANIFEST_SRC) > $(CILIUM_MANIFEST)

$(CRDS_MANIFEST): generate $(KUBECTL) $(HELM) | $(ASSETS)
	# generate crds manifest
	$(KUSTOMIZE_CMD) $(CRDS_MANIFEST_SRC) > $(CRDS_MANIFEST)

$(RESOURCES_MANIFEST): $(KUBECTL) | $(ASSETS)
	# generate resources manifest
	$(KUSTOMIZE_CMD) $(RESOURCES_MANIFEST_SRC) > $(RESOURCES_MANIFEST)
