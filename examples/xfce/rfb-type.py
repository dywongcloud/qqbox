"""Inject keyboard/pointer events over RFB into the live 5905 console, then
capture the resulting framebuffer. This proves the terminal works end-to-end
through the exact path the user uses (TurboVNC -> x11vnc -> Xvfb -> Chromium).
"""
import json
import os
import socket
import struct
import sys
import time
from PIL import Image

HOST = os.environ.get("RFB_HOST", "127.0.0.1")
PORT = int(os.environ.get("RFB_PORT", "5905"))
OUT = os.environ.get("RFB_OUT", "/Users/dylanwong/.gm-work/qqbox/xfce/out/vnc-typed.png")
TEXT = os.environ.get("RFB_TEXT", "echo VNC_TYPED_OK; uname -a")
CLICK = (int(os.environ.get("RFB_X", "400")), int(os.environ.get("RFB_Y", "778")))


def exact(s, n):
    b = b""
    while len(b) < n:
        c = s.recv(n - len(b))
        if not c:
            raise EOFError("closed")
        b += c
    return b


def main():
    s = socket.create_connection((HOST, PORT), timeout=15)
    s.settimeout(30)
    print("version:", exact(s, 12))
    s.sendall(b"RFB 003.008\n")
    n = exact(s, 1)[0]
    types = list(exact(s, n))
    print("security:", types)
    assert 1 in types
    s.sendall(bytes([1]))
    assert struct.unpack(">I", exact(s, 4))[0] == 0
    s.sendall(bytes([1]))  # ClientInit shared
    h = exact(s, 24)
    w, hh = struct.unpack(">HH", h[:4])
    name_len = struct.unpack(">I", h[20:24])[0]
    name = exact(s, name_len).decode("utf-8", "replace")
    print("ServerInit:", w, hh, name)
    pf = struct.pack(">BBBBHHHBBB3x", 32, 24, 0, 1, 255, 255, 255, 16, 8, 0)
    s.sendall(b"\x00\x00\x00\x00" + pf)
    s.sendall(b"\x02\x00" + struct.pack(">H", 1) + struct.pack(">i", 0))
    s.sendall(b"\x03\x00" + struct.pack(">HHHH", 0, 0, w, hh))
    # Drain the initial framebuffer update.
    def drain():
        t = exact(s, 1)[0]
        if t != 0:
            return None
        exact(s, 1)
        nr = struct.unpack(">H", exact(s, 2))[0]
        for _ in range(nr):
            x, y, rw, rh = struct.unpack(">HHHH", exact(s, 8))
            enc = struct.unpack(">i", exact(s, 4))[0]
            if enc == 0:
                exact(s, rw * rh * 4)
        return True
    drain()

    # Click the console input box.
    x, y = CLICK
    s.sendall(b"\x05" + bytes([1]) + struct.pack(">HH", x, y))
    s.sendall(b"\x05" + bytes([0]) + struct.pack(">HH", x, y))
    time.sleep(0.3)

    def key(keysym, down):
        s.sendall(b"\x04" + bytes([1 if down else 0]) + b"\x00\x00" + struct.pack(">I", keysym))

    for ch in TEXT:
        ks = ord(ch)
        if ch == "\n":
            ks = 0xFF0D
        key(ks, True)
        key(ks, False)
        time.sleep(0.02)
    key(0xFF0D, True)
    key(0xFF0D, False)
    time.sleep(2.5)

    # Request a fresh full framebuffer update.
    s.sendall(b"\x03\x00" + struct.pack(">HHHH", 0, 0, w, hh))
    frame = bytearray(w * hh * 4)
    got = 0
    deadline = time.time() + 20
    while time.time() < deadline and not got:
        t = exact(s, 1)[0]
        if t == 1:
            exact(s, 1)
            _, cnt = struct.unpack(">HH", exact(s, 4))
            exact(s, cnt * 6)
            continue
        if t == 2:
            continue
        if t == 3:
            exact(s, 3)
            ln = struct.unpack(">I", exact(s, 4))[0]
            exact(s, ln)
            continue
        if t != 0:
            continue
        exact(s, 1)
        nr = struct.unpack(">H", exact(s, 2))[0]
        for _ in range(nr):
            rx, ry, rw, rh = struct.unpack(">HHHH", exact(s, 8))
            enc = struct.unpack(">i", exact(s, 4))[0]
            if enc != 0:
                continue
            data = exact(s, rw * rh * 4)
            got += len(data)
            for row in range(rh):
                dst = ((ry + row) * w + rx) * 4
                src = row * rw * 4
                frame[dst:dst + rw * 4] = data[src:src + rw * 4]
    s.close()
    if not got:
        print("NO_FRAMEBUFFER")
        return 2
    Image.frombytes("RGB", (w, hh), bytes(frame), "raw", "BGRX", 0, 1).save(OUT)
    print(json.dumps({"typed": TEXT, "click": [x, y], "png": OUT, "bytes": got,
                      "png_bytes": os.path.getsize(OUT)}))
    return 0


if __name__ == "__main__":
    sys.exit(main())
