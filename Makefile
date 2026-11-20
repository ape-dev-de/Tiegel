.PHONY: check fmt vet test build run catalog

# Apple's linker (Xcode 15+) warns "ignoring duplicate libraries: '-lobjc'"
# because several cgo packages (Gio, native, trash) compile Objective-C.
# The warning is harmless; silence it when the linker knows the flag.
ifeq ($(shell uname -s),Darwin)
ifeq ($(shell echo 'int main(){return 0;}' | cc -x c - -Wl,-no_warn_duplicate_libraries -o /dev/null >/dev/null 2>&1 && echo ok),ok)
export CGO_LDFLAGS += -Wl,-no_warn_duplicate_libraries
endif
endif

check: fmt vet test

fmt:
	@out=$$(gofmt -l $$(git ls-files '*.go' ; git ls-files -o --exclude-standard '*.go')); \
	if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

vet:
	go vet ./...

test:
	go test -race ./...

build:
	go build -o bin/tiegel ./cmd/tiegel
	go build -o bin/tiegel-catalog ./cmd/tiegel-catalog

run:
	go run ./cmd/tiegel

catalog:
	go run ./cmd/tiegel-catalog

# --- macOS packaging (M8) -------------------------------------------------
# Signs ad-hoc unless TIEGEL_SIGN_IDENTITY is set; see docs/release.md.
.PHONY: app dmg

app:
	scripts/macos/build-app.sh

dmg: app
	scripts/macos/dmg.sh
