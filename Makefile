.PHONY: all binary binary-in-docker build tag release test test-in-docker link-to-gopath proto

repo=go-api-skeleton
repopath=github.com/mc0/$(repo)
shorthash=`git rev-parse --short HEAD`
image=$(repo):$(shorthash)
GOPATH ?= $(HOME)/go

all: proto test binary build

link-to-gopath:
	[ "$$PWD" = "$(GOPATH)/src/$(repopath)" ] \
		|| ( \
			mkdir -p $(GOPATH)/src/$(repopath) \
			&& rm -rf $(GOPATH)/src/$(repopath) \
			&& cp -R . $(GOPATH)/src/$(repopath) \
		)

binary: link-to-gopath
	cd $(GOPATH)/src/$(repopath) \
		&& GOPATH=$(GOPATH) go get \
		&& GOPATH=$(GOPATH) CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -installsuffix cgo --ldflags '-extldflags "-static"' -v -o ${CURDIR}/${repo}

test: link-to-gopath
	cd $(GOPATH)/src/$(repopath) \
		&& GOPATH=$(GOPATH) go get \
		&& GOPATH=$(GOPATH) go test -v

build:
	docker build -t $(image) .

template:
	rm -rf tmp-k8s
	mkdir -p tmp-k8s
	for file in k8s/*.yaml; do \
		cat $$file | sed -e "s/{IMAGE}/$(image)/g" > tmp-k8s/$$(basename $$file); \
	done

proto: $(addsuffix .pb.go, $(basename $(wildcard proto/*.proto)))

proto/%.pb.go: proto/%.proto
	protoc --go_out=plugins=grpc:proto/ -I proto/ $<
