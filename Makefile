APP=apcupsd-exporter
REPO=carlosedp
VERSION=latest
PLATFORMS=linux/amd64 linux/arm64 linux/arm linux/ppc64le

temp = $(subst /, ,$@)
os = $(word 1, $(temp))
arch = $(word 2, $(temp))
noop=
space = $(noop) $(noop)
comma = ,

all: $(PLATFORMS)

$(PLATFORMS):
	GOOS=$(os) GOARCH=$(arch) CGO_ENABLED=0 go build -a -installsuffix cgo -ldflags '-s -w -extldflags "-static"' -o ${APP}-$(os)-$(arch)

docker: $(PLATFORMS)
	docker buildx build -t ${REPO}/${APP} --platform=$(subst $(space),$(comma),$(PLATFORMS)) --push -f Dockerfile .

docker-daemon: $(PLATFORMS)
	docker buildx build -t ${REPO}/${APP}-daemon --platform=$(subst $(space),$(comma),$(PLATFORMS)) --push -f Dockerfile-with-daemon .

# docker: $(PLATFORMS)
# 	DOCKER_BUILDKIT=1 docker build -t ${REPO}/${APP} .

docker-multi: $(PLATFORMS)
	$(foreach PLAT,$(PLATFORMS),$(shell DOCKER_BUILDKIT=1 docker build -t ${REPO}/${APP}:${}{VERSION} --platform=$(PLAT) .))

release: docker

clean:
	rm -rf ${APP}-*

.PHONY: all docker clean
