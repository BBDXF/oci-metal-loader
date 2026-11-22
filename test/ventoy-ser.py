#!/usr/bin/env python3
"""Send text to the guest serial console: ventoy-ser.py SOCKET TEXT   (TEXT may contain \\n)."""
import socket, sys
s = socket.socket(socket.AF_UNIX); s.connect(sys.argv[1]); s.sendall(sys.argv[2].encode().decode("unicode_escape").encode()); s.close()
