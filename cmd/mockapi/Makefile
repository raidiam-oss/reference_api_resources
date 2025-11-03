# Makefile
#

include ../../version.properties
VER=${VERSION}

ifdef CHANGE_ID
VER=PR-${CHANGE_ID}-SNAPSHOT
else
VER=${VERSION}.${BUILD_NUMBER}
endif

# Update to your module path
PKG=github.com/example/sample-api/cmd/mockapi
REPO_INFO=$(shell git config --get remote.origin.url)
DATE=$(shell date)

LDFLAGS=-X "$(PKG).Version=$(VER)" -X "$(PKG).BuildDate=$(DATE)" -X "$(PKG).RepoUrl=$(REPO_INFO)" -X "$(PKG).BuildUrl=$(BUILD_URL)"

.PHONY: checks clean build build-local test test-and-report

checks:
	@staticcheck ./...
	@gosec ./...
	@go fmt ./...

clean:
	@rm -f bootstrap sample-api-${VER}-mock-api-function.zip

build:
	@rm -f bootstrap sample-api-${VER}-mock-api-function.zip
	@(GOARCH=arm64 GOOS=linux go build -ldflags '-s -w $(LDFLAGS)' -gcflags "all=-trimpath=$$GOPATH" -o bootstrap && zip sample-api-${VER}-mock-api-function.zip bootstrap)

build-local:
	@(rm -f bootstrap sample-api-${VER}-mock-api-function.zip && GOARCH=arm64 GOOS=linux go build -ldflags '-s -w $(LDFLAGS)' -gcflags "all=-trimpath=$$GOPATH" -o bootstrap && zip sample-api-${VER}-mock-api-function.zip bootstrap)

test:
	@(go test -v -coverprofile=coverage.out && go tool cover -func=coverage.out)

test-and-report:
	@(go test -v -coverprofile=coverage.out -json > report.json && go tool cover -func=coverage.out)