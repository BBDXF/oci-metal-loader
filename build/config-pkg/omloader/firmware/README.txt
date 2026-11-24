Optional extra firmware (e.g. Wi-Fi). Files here are overlaid on /lib/firmware at boot, same relative paths:
  iwlwifi-cc-a0-77.ucode, rtw88/*.bin, ath10k/..., mediatek/...
Get them from Debian packages (apt download firmware-iwlwifi; dpkg -x *.deb x; copy x/lib/firmware/*) or from
https://git.kernel.org/pub/scm/linux/kernel/git/firmware/linux-firmware.git
Drivers that failed for missing firmware are reloaded once. Does NOT work for GPU firmware (amdgpu/i915 need it at early boot; bundled).
Nothing configures Wi-Fi: use iwctl by hand in the shell.
