# Builder container
ARG IMAGE_ARCH="arm64"
ARG BASE_BUILD_IMAGE="golang:1.11-alpine"
ARG BASE_IMAGE="alpine:3.9"
ARG GO_ARCH="arm64"

FROM ${IMAGE_ARCH}/${BASE_BUILD_IMAGE} as builder

RUN apk add --no-cache git build-base && \
    rm -rf /var/cache/apk/*

WORKDIR $GOPATH/src/app

ADD . $GOPATH/src/app/

RUN go get ./...
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${GO_ARCH} go build -a -installsuffix cgo -ldflags \
    '-extldflags "-static"' -o ups-exporter .
RUN mv $GOPATH/src/app/ups-exporter /

# Application Container
#-------------------------------------------------------------------------------
FROM busybox

COPY --from=builder /ups-exporter /bin/ups-exporter

EXPOSE 9099

ENTRYPOINT ["/bin/ups-exporter"]
CMD ["-listen-address=:9099"]
