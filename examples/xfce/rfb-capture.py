"""Minimal RFB 3.8 client: attach to the x11vnc Unix socket that XFCE-in-laboon
exposes through the host bind mount, and save the live framebuffer as a PNG.

No VNC library is used; this is the wire protocol, so the artifact is direct
evidence that a real X desktop was being served from inside the sandbox.
"""
import json
import os
import socket
import struct
import sys
import time

from PIL import Image

OUT = "/Users/dylanwong/.gm-work/literbox/xfce/out"
SOCK = os.path.join(OUT, "vnc.sock")
READY = os.path.join(OUT, "vnc-ready.txt")
PNG = os.path.join(OUT, "xfce-vnc.png")
SUMMARY = os.path.join(OUT, "vnc-capture.json")


def log(msg):
    print(f"[rfb] {msg}", flush=True)


def wait_ready(deadline_s=240):
    start = time.time()
    while time.time() - start < deadline_s:
        if os.path.exists(READY) and os.path.exists(SOCK):
            age = time.time() - os.path.getmtime(READY)
            if age < 600:
                log(f"ready marker fresh ({age:.1f}s old)")
                return True
        time.sleep(2)
    return False


def recv_exact(s, n):
    buf = b""
    while len(buf) < n:
        chunk = s.recv(n - len(buf))
        if not chunk:
            raise EOFError(f"connection closed after {len(buf)}/{n} bytes")
        buf += chunk
    return buf


def main():
    if not wait_ready():
        log("TIMEOUT: vnc-ready.txt never appeared")
        return 2

    tcp = os.environ.get("RFB_TCP", "").strip()
    if tcp:
        host, port = tcp.rsplit(":", 1)
        s = socket.create_connection((host, int(port)), timeout=15)
        log(f"connected over TCP to {tcp}")
    else:
        s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        s.connect(SOCK)
        log(f"connected to {SOCK}")
    s.settimeout(30)

    server_version = recv_exact(s, 12)
    log(f"server version: {server_version!r}")
    s.sendall(b"RFB 003.008\n")

    count = recv_exact(s, 1)[0]
    if count == 0:
        reason_len = struct.unpack(">I", recv_exact(s, 4))[0]
        raise RuntimeError(f"no security types: {recv_exact(s, reason_len)!r}")
    types = list(recv_exact(s, count))
    log(f"security types offered: {types}")
    if 1 not in types:
        raise RuntimeError(f"server did not offer None security: {types}")
    s.sendall(bytes([1]))  # None
    result = struct.unpack(">I", recv_exact(s, 4))[0]
    if result != 0:
        reason_len = struct.unpack(">I", recv_exact(s, 4))[0]
        raise RuntimeError(f"security failed: {recv_exact(s, reason_len)!r}")
    log("security: None accepted")

    s.sendall(bytes([1]))  # ClientInit: shared
    header = recv_exact(s, 24)
    width, height = struct.unpack(">HH", header[:4])
    pf = header[4:20]
    bpp, depth, big_endian, true_color = struct.unpack(">BBBB", pf[:4])
    name_len = struct.unpack(">I", header[20:24])[0]
    name = recv_exact(s, name_len).decode("utf-8", "replace")
    log(f"ServerInit: {width}x{height} bpp={bpp} depth={depth} name={name!r}")

    # Ask for a simple 32bpp little-endian true-colour RGB format.
    pixel_format = struct.pack(
        ">BBBBHHHBBB3x",
        32, 24, 0, 1,       # bpp, depth, big-endian, true-colour
        255, 255, 255,      # r/g/b max
        16, 8, 0,           # r/g/b shift
    )
    s.sendall(b"\x00\x00\x00\x00" + pixel_format)  # SetPixelFormat
    s.sendall(b"\x02\x00" + struct.pack(">H", 2) + struct.pack(">ii", 0, -223))
    # FramebufferUpdateRequest, full, non-incremental
    s.sendall(b"\x03\x00" + struct.pack(">HHHH", 0, 0, width, height))
    log("requested full framebuffer update (Raw)")

    bpx = 4
    frame = bytearray(width * height * bpx)
    total_raw = 0
    rects = 0
    deadline = time.time() + 45
    while time.time() < deadline:
        msg_type = recv_exact(s, 1)[0]
        if msg_type == 1:  # SetColourMapEntries
            recv_exact(s, 1)
            first, n = struct.unpack(">HH", recv_exact(s, 4))
            recv_exact(s, n * 6)
            continue
        if msg_type == 2:  # Bell
            continue
        if msg_type == 3:  # ServerCutText
            recv_exact(s, 3)
            ln = struct.unpack(">I", recv_exact(s, 4))[0]
            recv_exact(s, ln)
            continue
        if msg_type != 0:
            log(f"unexpected server message {msg_type}; stopping")
            break
        recv_exact(s, 1)  # padding
        n_rects = struct.unpack(">H", recv_exact(s, 2))[0]
        for _ in range(n_rects):
            x, y, w, h = struct.unpack(">HHHH", recv_exact(s, 8))
            enc = struct.unpack(">i", recv_exact(s, 4))[0]
            if enc != 0:
                log(f"skipping rect encoding {enc} at {x},{y} {w}x{h}")
                continue
            data = recv_exact(s, w * h * bpx)
            total_raw += len(data)
            rects += 1
            for row in range(h):
                dst = ((y + row) * width + x) * bpx
                src = row * w * bpx
                frame[dst : dst + w * bpx] = data[src : src + w * bpx]
        if total_raw:
            break
    s.close()

    if not total_raw:
        log("NO_RAWDATA: no Raw framebuffer rect received")
        return 3

    img = Image.frombytes("RGB", (width, height), bytes(frame), "raw", "BGRX", 0, 1)
    img.save(PNG)
    colors = img.getcolors(maxcolors=1 << 20) or []
    summary = {
        "socket": SOCK,
        "server_name": name,
        "server_pixel_format": {"bpp": bpp, "depth": depth, "big_endian": big_endian, "true_color": true_color},
        "requested_pixel_format": "32bpp/24depth little-endian RGB888",
        "framebuffer": [width, height],
        "raw_rects": rects,
        "raw_bytes": total_raw,
        "distinct_colors": len(colors),
        "png": PNG,
        "png_bytes": os.path.getsize(PNG),
    }
    with open(SUMMARY, "w") as f:
        json.dump(summary, f, indent=2)
    log(f"saved {PNG}: {width}x{height}, {rects} rects, {total_raw} bytes, {len(colors)} distinct colors")
    print(json.dumps(summary, indent=2))
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as exc:
        log(f"ERROR: {type(exc).__name__}: {exc}")
        sys.exit(1)
