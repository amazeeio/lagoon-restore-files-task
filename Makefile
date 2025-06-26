PKG=github.com/amazeeio/lagoon-restore-files-task
GO_VER ?= 1.24
TAG     := $(shell git describe --abbrev=0 --tags 2>/dev/null || git rev-parse --abbrev-ref HEAD)
COMMIT  := $(shell git rev-parse --short=8 HEAD)
VERSION ?= $(TAG)+$(COMMIT)
BUILD_DATE ?= $(shell date +%FT%T%z)
LDFLAGS ?= -w -s -X "${PKG}/internal/task.TaskVersion=${VERSION}" -X "${PKG}/internal/task.BuildDate=${BUILD_DATE}"

.PHONY: test
test: fmt vet
	go clean -testcache && go test -v ./...

.PHONY: fmt
fmt:
	go fmt ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: run
run: fmt vet
	go run ./main.go

.PHONY: build
build: fmt vet build-dist

.PHONY: build-dist
build-dist:
	go build -ldflags '${LDFLAGS}' -v -o ./bin/restore-files-task .

.PHONY: build-docker
build-docker:
	DOCKER_BUILDKIT=1 docker build --pull --build-arg VERSION=${VERSION} --build-arg BUILD_DATE=${BUILD_DATE} --build-arg GO_VER=${GO_VER} --rm -f build/Dockerfile -t lagoon/restore-files-task .

.PHONY: k3d/push-images
k3d/push-images:
	@read -p "Push images to $$(echo $$KUBECONFIG)? (y/N) " ans; \
	if [ "$$ans" = "y" ]; then \
		export IMAGE_REGISTRY="registry.$$(kubectl -n ingress-nginx get services ingress-nginx-controller -o jsonpath='{.status.loadBalancer.ingress[0].ip}').nip.io/library" \
		&& docker login -u admin -p Harbor12345 $$IMAGE_REGISTRY \
		&& docker tag lagoon/restore-files-task $$IMAGE_REGISTRY/restore-files-task \
		&& docker push $$IMAGE_REGISTRY/restore-files-task; \
	else \
		echo "Aborted."; \
		exit 1; \
	fi
