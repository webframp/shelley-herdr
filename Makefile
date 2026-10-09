# Core targets: build, clean, test.

BINARY := shelly-herdr
# Which install method `make test` exercises: all, github (herdr plugin install)
# or nix (Home Manager).
FLAVOR ?= all

.PHONY: build clean clean-all test

build:
	go build -o $(BINARY) .

# Removes build output only. The container tests keep a cached Nix store volume
# that makes later runs fast; `make clean-all` removes that too.
clean:
	rm -f $(BINARY)
	rm -f result

# Build output plus the cached Nix store volume used by the container tests.
# The next `make test` rebuilds Home Manager from scratch, which is slow.
clean-all: clean
	test/run.sh clean

# Vets the Go code, then runs the container tests (needs podman or docker).
#   make test                  both install methods
#   make test FLAVOR=github    herdr plugin install, no Nix
#   make test FLAVOR=nix       Home Manager module
test:
	go vet ./...
	test/run.sh $(FLAVOR)
