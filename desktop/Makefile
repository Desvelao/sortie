export UID := $(shell id -u)
export GID := $(shell id -g)
RUN = docker compose run --rm dev

.PHONY: image tidy fmt vet test test-race build smoke check run shell release verify-release
image:  ; docker compose build dev
tidy:   ; $(RUN) go mod tidy
fmt:    ; $(RUN) gofmt -w .
vet:    ; $(RUN) go vet ./...
test:   ; $(RUN) go test ./...
# the race-detector run that release.sh also does (UI package without -race, see docs/RELEASE.md)
test-race: ; $(RUN) ./scripts/test_race.sh
build:  ; $(RUN) ./scripts/build_linux.sh build
smoke:  ; $(RUN) ./scripts/build_linux.sh smoke
check:  ; $(RUN) ./scripts/build_linux.sh all
run:    ; xhost +local:docker >/dev/null 2>&1 || true; docker compose run --rm gui go run ./cmd/sortie
shell:  ; $(RUN) bash
release:  ; $(RUN) ./scripts/release.sh
# runs the packaged release in clean distribution containers (only `docker` is used on the host)
verify-release: ; ./scripts/verify_release.sh
