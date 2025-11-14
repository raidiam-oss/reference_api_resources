# Makefile
#

ROOT ?= ..
-include $(ROOT)/version.properties        # don't error if the file isn't there
VERSION ?= 1.0.0

ifdef CHANGE_ID
VER=PR-${CHANGE_ID}-SNAPSHOT
else ifdef BUILD_NUMBER
VER=${VERSION}.${BUILD_NUMBER}
else
VER=${VERSION}
endif

PKG=github.com/example/sample-api/authorizer
REPO_INFO=$(shell git config --get remote.origin.url)
DATE=$(shell date)
GOARCH=arm64
LDFLAGS=-X "$(PKG).Version=$(VER)" -X "$(PKG).BuildDate=$(DATE)" -X "$(PKG).RepoUrl=$(REPO_INFO)" -X "$(PKG).BuildUrl=$(BUILD_URL)"

.PHONY: build build-local build-debug-local clean

clean:
	@rm -f bootstrap app sample-api-$(VER).authorizer-function.zip

build:
	@rm -f bootstrap sample-api-$(VER).authorizer-function.zip
	@(GOARCH=$(GOARCH) GOOS=linux go build -ldflags '-s -w $(LDFLAGS)' -gcflags "all=-trimpath=$$GOPATH" -o bootstrap && zip sample-api-$(VER).authorizer-function.zip bootstrap)

build-local:
	@(rm -f bootstrap sample-api-$(VER).authorizer-function.zip && GOARCH=$(GOARCH) GOOS=linux go build -ldflags '-s -w $(LDFLAGS)' -gcflags "all=-trimpath=$$GOPATH" -o bootstrap && zip sample-api-$(VER).authorizer-function.zip bootstrap)

build-debug-local:
	@rm -f app sample-api-$(VER).authorizer-function.zip bootstrap
	@echo "#!/bin/bash" > bootstrap
	@echo "dlv --listen=:2345 --headless=true --api-version=2 --accept-multiclient exec ./app" >> bootstrap
	@chmod +x bootstrap
	@(go build -ldflags '$(LDFLAGS)' -gcflags "all=-N -l -trimpath=$$GOPATH" -o app && zip sample-api-$(VER).authorizer-function.zip bootstrap app)
print-ver:
	@echo "VERSION='${VERSION}'"
	@echo "BUILD_NUMBER='${BUILD_NUMBER}'"
	@echo "CHANGE_ID='${CHANGE_ID}'"
	@echo "VER='${VER}'"