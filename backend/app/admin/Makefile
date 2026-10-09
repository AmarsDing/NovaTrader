# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
ENABLED_CGO=0
BINARY_NAME=admin
# VERSION=1.2.0
# 如果外部未传递VERSION变量，则使用默认值
VERSION?=1.1.0
BINARY_DIR=../../dist/admin/v$(VERSION)
SRC_DIR=./cmd/

prepare:
	mkdir -p bin
	mkdir -p $(BINARY_DIR)/linux-amd64
	mkdir -p $(BINARY_DIR)/linux-386
	mkdir -p $(BINARY_DIR)/linux-arm64
	mkdir -p $(BINARY_DIR)/linux-arm
	mkdir -p $(BINARY_DIR)/windows-amd64
	mkdir -p $(BINARY_DIR)/darwin-amd64

build: prepare
	CGO_ENABLED=$(ENABLED_CGO) $(GOBUILD) -o bin/v$(VERSION)/$(BINARY_NAME) -ldflags "-X github.com/cnotch/ipchub/config.Version=$(VERSION)" $(SRC_DIR)
	cp -r bin/v$(VERSION)/$(BINARY_NAME) $(BINARY_DIR)/

build-docker: prepare
	CGO_ENABLED=$(ENABLED_CGO) GOOS=linux GOARCH=amd64 $(GOBUILD) -o bin/docker/$(BINARY_NAME) -ldflags "-X github.com/cnotch/ipchub/config.Version=$(VERSION)" $(SRC_DIR)
	cp -r demos bin/docker/
	cp -r docs bin/docker/
	docker build -t ipchub .
	rm -rf bin/docker
	
build-linux-amd64: prepare
	CGO_ENABLED=$(ENABLED_CGO) GOOS=linux GOARCH=amd64 $(GOBUILD) -o bin/linux/amd64/v$(VERSION)/$(BINARY_NAME) -ldflags "-X github.com/cnotch/ipchub/config.Version=$(VERSION)" $(SRC_DIR)
	cp -r bin/linux/amd64/v$(VERSION)/$(BINARY_NAME) $(BINARY_DIR)/linux-amd64/

build-linux-386: prepare
	CGO_ENABLED=$(ENABLED_CGO) GOOS=linux GOARCH=386 $(GOBUILD) -o bin/linux/386/v$(VERSION)/$(BINARY_NAME) -ldflags "-X github.com/cnotch/ipchub/config.Version=$(VERSION)" $(SRC_DIR)
	cp -r bin/linux/386/v$(VERSION)/$(BINARY_NAME) $(BINARY_DIR)/linux-386/

build-linux-arm64: prepare
	CGO_ENABLED=$(ENABLED_CGO) GOOS=linux GOARCH=arm64 $(GOBUILD) -o bin/linux/arm/v$(VERSION)/$(BINARY_NAME) -ldflags "-X github.com/cnotch/ipchub/config.Version=$(VERSION)" $(SRC_DIR)
	cp -r bin/linux/arm/v$(VERSION)/$(BINARY_NAME) $(BINARY_DIR)/linux-arm64/

build-linux-arm: prepare
	CGO_ENABLED=$(ENABLED_CGO) GOOS=linux GOARCH=arm $(GOBUILD) -o bin/linux/arm/v$(VERSION)/$(BINARY_NAME) -ldflags "-X github.com/cnotch/ipchub/config.Version=$(VERSION)" $(SRC_DIR)
	cp -r bin/linux/arm/v$(VERSION)/$(BINARY_NAME) $(BINARY_DIR)/linux-arm/

# window compilation
build-windows-amd64: prepare
	CGO_ENABLED=$(ENABLED_CGO) GOOS=windows GOARCH=amd64 $(GOBUILD) -o bin/windows/amd64/v$(VERSION)/$(BINARY_NAME).exe -ldflags "-X github.com/cnotch/ipchub/config.Version=$(VERSION)" $(SRC_DIR)
	cp -r bin/windows/amd64/v$(VERSION)/$(BINARY_NAME).exe $(BINARY_DIR)/windows-amd64/

# darwin compilation
build-darwin-amd64: prepare
	CGO_ENABLED=$(ENABLED_CGO) GOOS=darwin GOARCH=amd64 $(GOBUILD) -o bin/darwin/amd64/v$(VERSION)/$(BINARY_NAME) -ldflags "-X github.com/cnotch/ipchub/config.Version=$(VERSION)" $(SRC_DIR)
	cp -r bin/darwin/amd64/v$(VERSION)/$(BINARY_NAME) $(BINARY_DIR)/darwin-amd64/

# amd64 all platform compilation
build-amd64: build-linux-amd64 build-windows-amd64 build-darwin-amd64

# all 
build-all: build-linux-amd64 build-windows-amd64 build-darwin-amd64 build-linux-386 build-linux-arm64 build-linux-arm

test:
	$(GOTEST) -v ./...
clean:
	$(GOCLEAN)
	rm -rf bin/
	