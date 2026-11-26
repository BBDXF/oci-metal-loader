# Local targets: bin, test, vet, iso (= iso-local), e2e-live-local.
# Remote targets (*-remote, fixture, disk, e2e*): optional KVM test harness over ssh. Set OML_REMOTE=user@host and
# OML_REMOTE_PASS (or use ssh keys; never stored in the repo). See build/README.md.
VERSION    ?= $(shell build/version.sh)
LDFLAGS    := -s -w -X main.version=$(VERSION)
OML_REMOTE ?=
OML_RDIR   ?= omloader-test
export SSHPASS := $(OML_REMOTE_PASS)
SSH  = $(if $(OML_REMOTE_PASS),sshpass -e )ssh -o StrictHostKeyChecking=no $(OML_REMOTE)
RSH  = $(if $(OML_REMOTE_PASS),sshpass -e )ssh -o StrictHostKeyChecking=no
CONTAINER_ENGINE ?= $(shell command -v podman >/dev/null 2>&1 && echo podman || echo docker)
export CONTAINER_ENGINE

.PHONY: update-base screenshots bin config-pkg test vet fmt version iso iso-local e2e-live-local sync iso-remote fixture disk e2e e2e-oci e2e-snap e2e-live e2e-ui e2e-pull e2e-ventoy e2e-ntfs e2e-storefile-guests e2e-hostdisks e2e-isoscan e2e-store e2e-tui e2e-real e2e-autoboot e2e-init e2e-vmroot e2e-quick e2e-full help licenses clean clean-remote check-remote

version:
	@echo $(VERSION)

# static binary (the loader ISO and the release tarball use this)
bin:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/omloader ./cmd/omloader

fmt:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
vet:
	go vet ./...
test: fmt vet
	go test ./...

# ---- local ISO build (podman or docker; no remote host): out/omloader-<version>.iso + out/SHA256SUMS ----
# Needs: podman|docker, mksquashfs (squashfs-tools). Override: CONTAINER_ENGINE=docker DEBIAN_MIRROR=http://mirrors.ustc.edu.cn/debian
iso: iso-local
iso-local: bin
	build/build-iso.sh

# small user-facing zip (boot.yaml, sources.yaml, examples, schema, ventoy.json.example) to unzip into the Ventoy data partition
config-pkg:
	build/mk-config-pkg.sh

# boots out/omloader.iso under QEMU/KVM (needs qemu-system-x86, ovmf, /dev/kvm) and waits for the shell-ready marker
e2e-live-local:
	test/smoke-local.sh

clean:
	rm -rf bin out work cache

# ---- optional remote KVM harness ----
check-remote:
	@test -n "$(OML_REMOTE)" || { echo "set OML_REMOTE=user@host (and OML_REMOTE_PASS or ssh keys)"; exit 1; }

sync: bin check-remote
	$(SSH) 'mkdir -p ~/$(OML_RDIR)/bin'
	tar -c build test configs VERSION | $(SSH) 'cd ~/$(OML_RDIR) && tar -x'
	cat bin/omloader | $(SSH) 'cat > ~/$(OML_RDIR)/bin/omloader.new && chmod +x ~/$(OML_RDIR)/bin/omloader.new && mv ~/$(OML_RDIR)/bin/omloader.new ~/$(OML_RDIR)/bin/omloader'

iso-remote: sync
	$(SSH) 'cd ~/$(OML_RDIR) && build/build-iso.sh'
fixture: sync
	$(SSH) 'cd ~/$(OML_RDIR) && test/make-fixture.sh'
disk: sync
	$(SSH) 'cd ~/$(OML_RDIR) && test/make-disk.sh'
# Every remote e2e target prints "DURATION <script>: N s (rc=..)". $(1)=script name in test/, $(2)=extra environment.
# `make help` lists the targets with their one-line descriptions (the "##" comments).
define run-one
	$(SSH) 'cd ~/$(OML_RDIR) && t0=$$(date +%s); $(2) test/$(1); rc=$$?; echo "DURATION $(1): $$(( $$(date +%s)-t0 )) s (rc=$$rc)"; exit $$rc'
endef
e2e: sync            ## VM fixture, 3 boots with kexec (covered by snap/store/pull: not in e2e-full)
	$(call run-one,e2e.sh,)
e2e-oci: sync        ## OCI fixture boots, experimental (covered by e2e-snap/store: not in e2e-full)
	$(call run-one,e2e-oci.sh,)
e2e-snap: sync       ## snapshots: create/revert/delete/ephemeral/prune/flatten, container + VM (~long)
	$(call run-one,e2e-snap.sh,)
e2e-live: sync       ## live ISO: boot to TUI/shell, network, CA/TLS, tools, version info (about a minute)
	$(call run-one,e2e-live.sh,)
e2e-ui: sync         ## console login/users/sudo, busybox fallback, clean fs after poweroff (optional, not in e2e-full)
	$(call run-one,e2e-ui.sh,)
e2e-pull: sync       ## online pull against a local simplestreams server, mirrors, offline cache
	$(call run-one,e2e-pull.sh,)
e2e-ventoy: sync     ## boot through Ventoy (normal+GRUB2), boot.yaml auto-boot, cancel
	$(call run-one,e2e-ventoy.sh,)
e2e-ntfs: sync       ## ISO on an NTFS data partition labelled VHDX: stage 1 ntfs-3g, data-partition fallback, dirty NTFS read-only
	$(call run-one,e2e-ntfs.sh,)
e2e-hostdisks: sync   ## host disks in guests (default on, rw): container + VM see the data partition
	$(call run-one,e2e-hostdisks.sh,)

e2e-storefile-guests: sync ## store file on NTFS with real guests: Arch VM (.ko.zst modules) + Docker image via mirror (needs network)
	$(call run-one,e2e-storefile-guests.sh,SKIP_BUILD=1)
e2e-isoscan: sync    ## stage-1 ISO search order: Ventoy partitions first, VTOY_DEFAULT_SEARCH_ROOT, omloader/ dir, skipped-candidates log
	$(call run-one,e2e-isoscan.sh,SKIP_BUILD=1)
e2e-vmroot: sync   ## vm_root_size: VM root grown (3G, 20G default), persists over reboot, off = unchanged
	$(call run-one,e2e-vmroot.sh,)
e2e-init: sync   ## first-boot init dialog: confirm blocks the autoboot, accept-defaults applies timezone/hostname and persists
	$(call run-one,e2e-init.sh,)

e2e-autoboot: sync   ## TUI-owned auto-boot countdown: banner, cancel, expiry once, ui=shell, last, poweroff_on_fail
	$(call run-one,e2e-autoboot.sh,)
e2e-store: sync      ## store file on exFAT/NTFS: container + VM, store create/grow/check
	$(call run-one,e2e-store.sh,)
e2e-tui: sync        ## TUI driven over the serial console: images, tree, pull, snapshots, shell round trip
	$(call run-one,e2e-tui.sh,)
e2e-real: sync       ## REAL public images (network): defaults to 5 images; e2e-full uses only ubuntu ct+vm
	$(call run-one,e2e-real.sh,)

# ---- test tiers (docs/DEVELOPMENT.md "Test tiers") ----
E2E_FULL_SUITES := live snap tui ventoy autoboot store pull real
e2e-quick: sync      ## tier 2, minutes: live ISO suite only
	$(SSH) 'cd ~/$(OML_RDIR) && test/run-suites.sh live'
e2e-full: sync       ## tier 3, release gate (reduced): live snap tui ventoy autoboot store pull + real (ubuntu ct+vm)
	$(SSH) 'cd ~/$(OML_RDIR) && SUITE_ENV_real="REAL_IMAGES=\"ct:ubuntu/noble vm:ubuntu/noble\" REAL_SNAP=\"ct:ubuntu/noble vm:ubuntu/noble\"" test/run-suites.sh $(E2E_FULL_SUITES)'

help:                ## list the make targets that have a description
	@grep -hE '^[a-zA-Z0-9_-]+:.*## ' $(MAKEFILE_LIST) | sed -E 's/:[^#]*## /\t/' | sort | column -t -s "$$(printf '\t')"

licenses: sync
	$(SSH) 'cd ~/$(OML_RDIR) && build/licenses.sh' && mkdir -p out && $(SSH) 'cat ~/$(OML_RDIR)/out/licenses.tsv' > out/licenses.tsv
clean-remote:
	-$(SSH) 'pkill -u $$USER -f "[o]mloader-test/.*qemu" || true; pkill -u $$USER -f "[o]mloader-test/test/" || true; rm -rf ~/$(OML_RDIR)/out ~/$(OML_RDIR)/work; true'

# Simulated TUI screenshots (see test/screenshots/README.md)
screenshots:
	rm -rf out/screenshots-sim && mkdir -p out/screenshots-sim/ans
	OML_SHOTS_DIR=$(CURDIR)/out/screenshots-sim/ans go test ./internal/tui -count=1 -run TestSimShots
	python3 test/screenshots/ans2png.py out/screenshots-sim/ans out/screenshots-sim

# Is the last-built live base behind current Debian stable? (podman/docker + network; builds nothing)
update-base:
	build/update-base.sh
