#!/usr/bin/env python3
"""Serial-console driver for test/e2e-tui.sh: runs test/run-kvm.sh (INTERACTIVE=1: QEMU serial on a pty), feeds keys,
keeps a tiny VT emulator (80x24) to scrape what the TUI shows, and keeps the ANSI-stripped raw stream for plain markers.

usage: tui-drive.py SCENARIO     SCENARIO = run1 | run2 | run3   (environment: DISK, SRCPORT, W (work dir), EXPERIMENTAL_OCI ...)
Exit status 0 = all checks of the scenario passed. Prints PASS:/FAIL: lines.
"""
import os, re, select, signal, subprocess, sys, threading, time

HERE = os.path.dirname(os.path.abspath(__file__))
W = os.environ.get("W", os.path.join(HERE, "..", "work"))


class VT:
    def __init__(self, w=80, h=24):
        self.w, self.h = w, h
        self.rows = [[" "] * w for _ in range(h)]
        self.y = self.x = 0
        self.esc = None

    def _scroll(self):
        self.rows.pop(0)
        self.rows.append([" "] * self.w)
        self.y = self.h - 1

    def feed(self, s):
        for ch in s:
            if self.esc is not None:
                self.esc += ch
                if self.esc == "\x1b" + ch and ch != "[":
                    self.esc = None  # two-char escape, ignore
                    continue
                if len(self.esc) > 2 and "@" <= ch <= "~":
                    self._csi(self.esc[2:-1], ch)
                    self.esc = None
                continue
            if ch == "\x1b":
                self.esc = "\x1b"
            elif ch == "\r":
                self.x = 0
            elif ch == "\n":
                self.y += 1
                if self.y >= self.h:
                    self._scroll()
            elif ch == "\b":
                self.x = max(0, self.x - 1)
            elif ch == "\t":
                self.x = min(self.w - 1, (self.x // 8 + 1) * 8)
            elif ch >= " ":
                if self.x >= self.w:
                    self.x = 0
                    self.y += 1
                    if self.y >= self.h:
                        self._scroll()
                self.rows[self.y][self.x] = ch
                self.x += 1

    def _csi(self, params, fin):
        p = [int(x) if x.isdigit() else 0 for x in params.lstrip("?").split(";")] if params else []
        n = p[0] if p and p[0] else 1
        if fin in "Hf":
            self.y = min(self.h - 1, max(0, (p[0] if p and p[0] else 1) - 1))
            self.x = min(self.w - 1, max(0, (p[1] if len(p) > 1 and p[1] else 1) - 1))
        elif fin == "J" and (not p or p[0] in (0, 2)):
            if p and p[0] == 2:
                self.rows = [[" "] * self.w for _ in range(self.h)]
            else:
                for x in range(self.x, self.w):
                    self.rows[self.y][x] = " "
                for y in range(self.y + 1, self.h):
                    self.rows[y] = [" "] * self.w
        elif fin == "K":
            for x in range(self.x, self.w):
                self.rows[self.y][x] = " "
        elif fin == "A":
            self.y = max(0, self.y - n)
        elif fin == "B":
            self.y = min(self.h - 1, self.y + n)
        elif fin == "C":
            self.x = min(self.w - 1, self.x + n)
        elif fin == "D":
            self.x = max(0, self.x - n)

    def text(self):
        return "\n".join("".join(r).rstrip() for r in self.rows)


ANSI = re.compile(r"\x1b\[[0-9;?=]*[A-Za-z]|\x1b[()][A-Za-z0-9]")


class Session:
    def __init__(self, tag, timeout=420):
        self.tag = tag
        self.vt = VT()
        self.raw = ""
        self.lock = threading.Lock()
        self.fails = 0
        master, slave = os.openpty()
        env = dict(os.environ, INTERACTIVE="1", TIMEOUT=str(timeout), NAME="", SNAP="", EXTRA=os.environ.get("EXTRA", ""))
        self.log = open(os.path.join(W, f"tui-{tag}.log"), "w")
        self.p = subprocess.Popen(["bash", os.path.join(HERE, "run-kvm.sh")], stdin=slave, stdout=slave, stderr=slave,
                                  env=env, start_new_session=True)
        os.close(slave)
        self.master = master
        self.alive = True
        threading.Thread(target=self._pump, daemon=True).start()

    def _pump(self):
        import codecs
        dec = codecs.getincrementaldecoder("utf-8")("replace")
        while True:
            try:
                r, _, _ = select.select([self.master], [], [], 0.5)
                if not r:
                    if self.p.poll() is not None:
                        break
                    continue
                b = os.read(self.master, 4096)
            except OSError:
                break
            if not b:
                break
            s = dec.decode(b)
            with self.lock:
                self.log.write(s)
                self.log.flush()
                self.vt.feed(s)
                self.raw += s.replace("\r", "")
        self.alive = False

    # -- input
    def send(self, s, delay=0.15):
        os.write(self.master, s.encode())
        time.sleep(delay)

    def type(self, s):
        for ch in s:
            self.send(ch, 0.05)
        time.sleep(0.2)

    def esc(self):
        self.send("\x1b", 0.5)  # lone ESC: the TUI turns it into Esc after a short timeout

    # -- output
    def clean(self):
        with self.lock:
            return ANSI.sub("", self.raw)

    def screen(self):
        with self.lock:
            return self.vt.text()

    def wait(self, pred, timeout):
        end = time.time() + timeout
        while time.time() < end:
            if pred():
                return True
            if not self.alive and not pred():
                time.sleep(0.2)
                return pred()
            time.sleep(0.2)
        return pred()

    def raw_has(self, pat, timeout=60, since=0):
        rx = re.compile(pat)
        return self.wait(lambda: rx.search(self.clean()[since:]) is not None, timeout)

    def screen_has(self, subs, timeout=30):
        if isinstance(subs, str):
            subs = [subs]
        return self.wait(lambda: all(s in self.screen() for s in subs), timeout)

    # -- verdicts
    def check(self, ok, msg):
        print(("PASS: " if ok else "FAIL: ") + msg, flush=True)
        if not ok:
            self.fails += 1
            print("---- screen at failure:\n" + self.screen() + "\n---- (full log: %s)" % self.log.name, flush=True)
        return ok

    def stop(self):
        if self.p.poll() is None:
            try:
                os.killpg(self.p.pid, signal.SIGKILL)  # only the process group we started
            except ProcessLookupError:
                pass
        self.p.wait()

    def wait_exit(self, timeout):
        try:
            self.p.wait(timeout)
            return True
        except subprocess.TimeoutExpired:
            return False


def start_tui(s, n=1):
    ok = s.wait(lambda: s.clean().count("OMLOADER_TUI_READY") >= n, 300)
    s.check(ok, "loader booted into the TUI on the serial console (OMLOADER_TUI_READY #%d)" % n)
    return ok


def run1():
    s = Session("run1")
    try:
        if not start_tui(s):
            return s
        s.check(s.screen_has(["OCI Metal Loader", "1:Images", "omloci", "oci"]), "images list shows the fixture image omloci")
        s.send("\r")
        s.check(s.screen_has(["omloci", "snapshots=0", "base (original image)", "current (you are here)"]), "image detail (tree) opens: base root + live state")
        s.send("c")
        s.check(s.screen_has("Create snapshot"), "create-snapshot form")
        s.type("s1\r\r\r")
        s.check(s.screen_has("nothing to snapshot"), "a never-booted image has no live state to snapshot (refused)")
        s.send("\r")
        # ---- pull from the local fake simplestreams
        s.esc()
        s.check(s.screen_has(["NAME", "SNAPS", "omloci"]), "Esc returns to the images list")
        s.send("2")
        s.check(s.screen_has(["Sources", "lxc-images"]), "Pull tab lists the configured source")
        s.send("j")  # sources are sorted: dockerhub, lxc-images
        s.send("t")
        ok = s.screen_has(["[finished]"], 90) and s.screen_has("OK ")
        s.check(ok and ("10.0.2.2:%s" % os.environ["SRCPORT"]) in s.screen(), "source test reports the local mirror OK")
        s.send("\r")
        s.send("/")
        s.type("omltest\r\rcontainer\r")
        s.check(s.screen_has("lxc:omltest/ct/default", 90), "catalog filtered to the omltest container image")
        s.send("\r")
        s.check(s.screen_has("Pull image"), "pull form")
        s.send("\r")  # accept default name
        s.check(s.screen_has("Download and import"), "pull confirm")
        s.send("\r")
        ok = s.screen_has("[finished]", 300)
        s.check(ok and "sha256 verified" in s.screen(), "pull finished with digest verification (streamed output)")
        s.send("\r")
        s.check(s.screen_has("pulled lxc:omltest/ct/default as omltest-ct"), "pulled image registered")
        # ---- boot the pulled container image: continue current (with dry-run preview first)
        s.send("1")
        s.send("j")
        s.check(s.screen_has("omltest-ct"), "images list shows the pulled image")
        s.send("\r")
        s.check(s.screen_has("type=lxd-container"), "detail of the pulled lxd-container image")
        s.send("b")
        s.check(s.screen_has(["Boot omltest-ct", "Continue current", "base (original image), ephemeral"]), "boot menu")
        s.send("\r")
        s.check(s.screen_has(["omloader boot --image omltest-ct --snapshot current", "switch-root"]), "boot confirm shows the omloader boot command")
        s.send("d")
        ok = s.screen_has("[finished]", 60)
        s.check(ok and "[FAILED]" not in s.screen(), "dry-run preview streamed")
        s.send("\r")
        s.check(s.screen_has("Boot now?"), "back at the boot confirmation")
        s.send("\r")
        ok = s.raw_has(r"OMLOADER_LOADER: switch-root", 120) and s.raw_has(r"OMLOADER_COUNTER=1\b", 300)
        s.check(ok, "container booted from the TUI via switch-root (counter == 1)")
        s.check(s.raw_has("OMLOADER_GUEST_OK", 30), "guest reports OMLOADER_GUEST_OK")
        s.wait_exit(120)
    finally:
        s.stop()
    return s


def run2():
    s = Session("run2")
    try:
        if not start_tui(s):
            return s
        ok = s.screen_has(["omloci", "omltest-ct"])
        s.check(ok, "second boot: TUI lists both images (pulled image persisted on VTOYDATA)")
        s.send("j")
        s.send("\r")
        s.check(s.screen_has(["omltest-ct", "current (you are here)"]), "pulled image: the live state (created by the first boot) persisted")
        s.send("c")
        s.type("s1\rfirst\rkeepme\r")
        s.check(s.screen_has(["created snapshot s1", "#keepme"]), "snapshot s1 created from the TUI (note + tag) after the boot")
        s.send("j")
        s.send("r")
        s.check(s.screen_has("Revert"), "revert menu")
        s.send("1")
        s.check(s.screen_has(["WARNING", "discarded permanently"]), "revert asks for confirmation")
        s.send("\r")
        s.check(s.screen_has(["revert to s1: ok", "s1"]), "revert to s1 (no live changes yet: live state rebased on s1)")
        s.esc()
        s.send("P")
        s.check(s.screen_has(["Reboot", "Power off"]), "power menu")
        s.send("\r")
        s.check(s.screen_has("WARNING"), "reboot asks for confirmation")
        s.send("\r")
        s.check(s.wait_exit(120), "Reboot from the TUI ended the VM (qemu -no-reboot)")
    finally:
        s.stop()
    return s


def run3():
    s = Session("run3")
    try:
        if not start_tui(s):
            return s
        s.esc()
        s.check(s.screen_has("open the shell"), "Esc on the first screen asks to open the shell")
        s.send("\r")
        s.check(s.raw_has("OMLOADER_SHELL_READY", 60), "TUI -> shell (banner)")
        s.send("exit\r")
        s.check(start_tui(s, 2), "leaving the shell shows the TUI again")
        s.send("j")
        s.send("\r")
        s.check(s.screen_has(["omltest-ct", "s1", "[frozen]", "current (you are here)"]), "detail: s1 is frozen, the live state sits on it")
        s.send("b")
        s.send("\r")  # continue current
        s.check(s.screen_has("--snapshot current"), "boot confirm for the current snapshot")
        s.send("\r")
        ok = s.raw_has(r"OMLOADER_COUNTER=2\b", 360)
        s.check(ok, "current booted again from the TUI continues from s1 (counter 1 -> 2)")
        s.wait_exit(120)
    finally:
        s.stop()
    return s


if __name__ == "__main__":
    s = {"run1": run1, "run2": run2, "run3": run3}[sys.argv[1]]()
    sys.exit(1 if s.fails else 0)
