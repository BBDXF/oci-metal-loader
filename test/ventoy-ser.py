#!/usr/bin/env python3
"""Send text to the guest serial console: ventoy-ser.py SOCKET TEXT   (TEXT may contain \\n). Sent in small chunks: a 16550 UART drops
characters when a long line arrives at once."""
import socket, sys, time
s = socket.socket(socket.AF_UNIX); s.connect(sys.argv[1])
data = sys.argv[2].encode().decode("unicode_escape").encode()
for i in range(0, len(data), 8):
    s.sendall(data[i:i + 8]); time.sleep(0.03)
s.close()
