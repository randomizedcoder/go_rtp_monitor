#
# Makefile
#

# ldflags variables to update --version
# short commit hash
COMMIT :=$(shell /usr/bin/git describe --always)
DATE :=$(shell /bin/date -u +"%Y-%m-%d-%H:%M")
BINARY :=go_rtp_monitor

all: clean build version

test:
	go test

clean:
	[ -f ${BINARY} ] && /bin/rm -rf ./${BINARY} || true

build:
	CGO_ENABLED=0 go build -ldflags "-X main.commit=${COMMIT} -X main.date=${DATE}" -o ./${BINARY} ./${BINARY}.go

# https://words.filippo.io/shrink-your-go-binaries-with-this-one-weird-trick/
buildsmall:
	CGO_ENABLED=0 go build -ldflags "-s -w -X main.commit=${COMMIT} -X main.date=${DATE}" -o ./${BINARY} ./${BINARY}.go

shrink:
	upx --brute ./${BINARY}

version:
	./${BINARY} --version

curl:
	curl --silent http://localhost:9901/metrics

lab:
	./go_rtp_monitor -group 232.0.0.1 -port 6666 -intf br-siden -source 10.99.0.1
#
