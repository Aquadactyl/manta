GIT_HEAD = $(shell git rev-parse HEAD | head -c8)

build:
	GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -trimpath -o build/manta_linux_amd64 -v .
	GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -trimpath -o build/manta_linux_arm64 -v .

debug:
	go build -o build/manta -ldflags="-X github.com/Aquadactyl/manta/system.Version=$(GIT_HEAD)"
	sudo ./build/manta --debug --config config.yml --pprof --pprof-block-rate 1

# Runs a remotly debuggable session for Manta allowing an IDE to connect and target
# different breakpoints.
rmdebug:
	go build -o build/manta -gcflags "all=-N -l" -ldflags="-X github.com/Aquadactyl/manta/system.Version=$(GIT_HEAD)" -race
	sudo dlv --listen=:2345 --headless=true --api-version=2 --accept-multiclient exec ./build/manta -- --debug --config config.yml

cross-build: clean build

# Lint the Linux implementation on Linux to cover the _linux/_unix files.
lint:
	GOOS=linux golangci-lint run ./...

clean:
	rm -f build/manta_* build/manta

.PHONY: build debug rmdebug cross-build clean lint
