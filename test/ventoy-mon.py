#!/usr/bin/env python3
"""Tiny QEMU monitor client: ventoy-mon.py SOCKET CMD...  (e.g. 'sendkey ret', 'screendump /path.ppm'); one command per argument."""
import socket, sys, time
s = socket.socket(socket.AF_UNIX); s.connect(sys.argv[1]); s.settimeout(2)
def rd():
    try: return s.recv(65536)
    except Exception: return b""
rd()
for c in sys.argv[2:]:
    s.sendall(c.encode() + b"\n"); time.sleep(0.3); rd()
