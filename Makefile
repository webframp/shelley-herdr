# Core targets: build, clean, test.

BINARY := shelly-herdr
# Which install method `make test` exercises: all, github (herdr plugin install)
# or nix (Home Manager).
FLAVOR ?= all

.PHONY: build clean test

build:
	go build -o $(BINARY) .

# Removes build output only. The container tests cache a Nix store volume that
# makes later runs fast; remove it separately with `test/run.sh clean`.
clean:
	rm -f $(BINARY)
	rm -f result

# Vets the Go code, then runs the container tests (needs podman or docker).
#   make test                  both install methods
#   make test FLAVOR=github    herdr plugin install, no Nix
#   make test FLAVOR=nix       Home Manager module
test:
	go vet ./...
	test/run.sh $(FLAVOR)
