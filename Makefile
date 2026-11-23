# Remote build/test harness. Password via env OML_REMOTE_PASS (never stored).
OML_REMOTE ?= philips@192.168.8.26
OML_RDIR   ?= omloader-test
export SSHPASS := $(OML_REMOTE_PASS)
SSH  = sshpass -e ssh -o StrictHostKeyChecking=no $(OML_REMOTE)
RSH  = sshpass -e ssh -o StrictHostKeyChecking=no

.PHONY: bin sync iso fixture disk e2e e2e-oci e2e-snap e2e-live e2e-ui e2e-pull e2e-ventoy e2e-store e2e-tui e2e-real licenses clean-remote
bin:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o build/omloader.bin ./cmd/omloader

sync: bin
	$(SSH) 'mkdir -p ~/$(OML_RDIR)/bin'
	tar -c --exclude=omloader.bin build test | $(SSH) 'cd ~/$(OML_RDIR) && tar -x'
	cat build/omloader.bin | $(SSH) 'cat > ~/$(OML_RDIR)/bin/omloader.new && chmod +x ~/$(OML_RDIR)/bin/omloader.new && mv ~/$(OML_RDIR)/bin/omloader.new ~/$(OML_RDIR)/bin/omloader'

iso: sync
	$(SSH) 'cd ~/$(OML_RDIR) && build/build-iso.sh'
fixture: sync
	$(SSH) 'cd ~/$(OML_RDIR) && test/make-fixture.sh'
disk: sync
	$(SSH) 'cd ~/$(OML_RDIR) && test/make-disk.sh'
e2e: sync
	$(SSH) 'cd ~/$(OML_RDIR) && test/e2e.sh'
e2e-oci: sync
	$(SSH) 'cd ~/$(OML_RDIR) && test/e2e-oci.sh'
e2e-snap: sync
	$(SSH) 'cd ~/$(OML_RDIR) && test/e2e-snap.sh'
e2e-live: sync
	$(SSH) 'cd ~/$(OML_RDIR) && test/e2e-live.sh'
e2e-ui: sync
	$(SSH) 'cd ~/$(OML_RDIR) && test/e2e-ui.sh'
e2e-pull: sync
	$(SSH) 'cd ~/$(OML_RDIR) && test/e2e-pull.sh'
e2e-ventoy: sync
	$(SSH) 'cd ~/$(OML_RDIR) && test/e2e-ventoy.sh'
e2e-store: sync
	$(SSH) 'cd ~/$(OML_RDIR) && test/e2e-store.sh'
e2e-tui: sync
	$(SSH) 'cd ~/$(OML_RDIR) && test/e2e-tui.sh'
licenses: sync
	$(SSH) 'cd ~/$(OML_RDIR) && build/licenses.sh' && mkdir -p out && $(SSH) 'cat ~/$(OML_RDIR)/out/licenses.tsv' > out/licenses.tsv
clean-remote:
	-$(SSH) 'pkill -u $$USER -f "[o]mloader-test/.*qemu" || true; pkill -u $$USER -f "[o]mloader-test/test/" || true; rm -rf ~/$(OML_RDIR)/out ~/$(OML_RDIR)/work; true'
e2e-real: sync
	$(SSH) 'cd ~/$(OML_RDIR) && test/e2e-real.sh'
