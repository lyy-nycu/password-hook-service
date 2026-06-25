GO_IMAGE ?= golang:1.26.4
DOCKER_RUN = docker run --rm -v "$(PWD):/src" -w /src $(GO_IMAGE)

.PHONY: fmt test vet verify docker-build

fmt:
	$(DOCKER_RUN) gofmt -w .

test:
	$(DOCKER_RUN) go test ./...

vet:
	$(DOCKER_RUN) go vet ./...

verify:
	$(DOCKER_RUN) sh -c "gofmt -w . && go test ./... && go vet ./..."

docker-build:
	docker build -f deploy/Dockerfile -t password-hook-service .
