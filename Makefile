# Common commands for the monorepo. Everything builds inside Docker (see AGENTS.md).
# Each app also has its own Makefile: `make -C desktop help`, `make -C android help`.
# Any target of an app is reachable as desktop-<target> / android-<target>.

.DEFAULT_GOAL := help
.PHONY: help version check-version image check test build run release clean \
        desktop-check desktop-build desktop-run desktop-release android-check android-debug android-install android-release

help: ## List the main targets
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-18s %s\n", $$1, $$2}'
	@echo "  desktop-<target>   any target of desktop/Makefile (e.g. desktop-fmt, desktop-vet)"
	@echo "  android-<target>   any target of android/Makefile (e.g. android-lint, android-shell)"

version: ## Print the shared version
	@cat VERSION

check-version: ## Check VERSION and both changelogs
	@scripts/check-version.sh

image: ## Build both Docker dev images
	$(MAKE) -C desktop image
	$(MAKE) -C android image

check: check-version ## Full check of both apps (version, desktop checks, android tests + lint)
	$(MAKE) -C desktop check
	$(MAKE) -C android check

test: ## Unit tests of both apps
	$(MAKE) -C desktop test
	$(MAKE) -C android test

build: ## Dev builds: desktop binary and android debug APK
	$(MAKE) -C desktop build
	$(MAKE) -C android debug

release: check-version ## Release builds of both apps (android needs signing, see android/README.md)
	$(MAKE) -C desktop release
	$(MAKE) -C android release

clean: ## Remove android build outputs
	$(MAKE) -C android clean

desktop-check: ; $(MAKE) -C desktop check ## Desktop: gofmt, vet, tests, build, GUI smoke test
desktop-build: ; $(MAKE) -C desktop build ## Desktop: build the dev binary
desktop-run: ; $(MAKE) -C desktop run ## Desktop: run the app (needs X11/Wayland)
desktop-release: ; $(MAKE) -C desktop release ## Desktop: validated release tarball
android-check: ; $(MAKE) -C android check ## Android: unit tests and lint
android-debug: ; $(MAKE) -C android debug ## Android: debug APK
android-install: ; $(MAKE) -C android install ## Android: install on a device/emulator
android-release: ; $(MAKE) -C android release ## Android: signed APK and AAB

desktop-%: ; $(MAKE) -C desktop $*
android-%: ; $(MAKE) -C android $*
