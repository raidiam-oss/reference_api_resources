# Makefile
#

ROOT ?= ../..
-include $(ROOT)/version.properties
VERSION ?= 1.0.0

ifdef CHANGE_ID
VER=PR-${CHANGE_ID}-SNAPSHOT
else ifdef BUILD_NUMBER
VER=${VERSION}.${BUILD_NUMBER}
else
VER=${VERSION}
endif

PKG=github.com/example/sample-api/mock-api
REPO_INFO=$(shell git config --get remote.origin.url)
DATE=$(shell date)
LDFLAGS=-X "$(PKG).Version=$(VER)" -X "$(PKG).BuildDate=$(DATE)" -X "$(PKG).RepoUrl=$(REPO_INFO)" -X "$(PKG).BuildUrl=$(BUILD_URL)"

.PHONY: checks clean build test test-and-report build-local

clean:
	@( rm -f main bootstrap sample-api-$(VER).mock-api-function.zip )

build:
	@rm -f bootstrap sample-api-$(VER).mock-api-function.zip
	@(GOARCH=arm64 GOOS=linux go build -ldflags '-s -w $(LDFLAGS)' -gcflags "all=-trimpath=$$GOPATH" -o bootstrap && zip sample-api-$(VER).mock-api-function.zip bootstrap)

build-local:
	@(rm -f bootstrap sample-api-$(VER).mock-api-function.zip && GOARCH=arm64 GOOS=linux go build -ldflags '-s -w $(LDFLAGS)' -gcflags "all=-trimpath=$$GOPATH" -o bootstrap && zip sample-api-$(VER).mock-api-function.zip bootstrap)

print-ver:
	@echo "VERSION='${VERSION}'"
	@echo "BUILD_NUMBER='${BUILD_NUMBER}'"
	@echo "CHANGE_ID='${CHANGE_ID}'"
	@echo "VER='${VER}'"