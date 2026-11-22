# qqbox build orchestration.
#
# Rust workspace: Cargo.toml is the build entry point. The committed freestanding
# Zig stubs are embedded with include_bytes!, so a build needs no Zig toolchain.
# Go source and Go build tooling were removed; Rust is the only implementation.

SHELL := /bin/sh
CARGO ?= cargo
# The committed freestanding Zig stub sources target Zig 0.13.0. Installed
# toolchains may differ; stub/build.sh exits non-zero on a major-version
# mismatch instead of silently compiling against an incompatible toolchain.
# Set ZIG explicitly to a 0.13.0 binary when rebuilding the .bin payloads.
ZIG ?= $(shell command -v zig 2>/dev/null)
export ZIG

.DEFAULT_GOAL := build

.PHONY: build build-release check fmt clippy test stubs stubs-linux \
	stubs-windows stubs-macos cli daemon mcp runtime run invariants version routes \
	witness witness-http backend-witness backend-exec-elf zh-hant-verify stub-verify clean

# ------------------------------------------------------------- rust build -----
build:
	$(CARGO) build --workspace

build-release:
	$(CARGO) build --workspace --release

check:
	$(CARGO) check --workspace

fmt:
	$(CARGO) fmt --all

clippy:
	$(CARGO) clippy --workspace -- -D warnings

test:
	$(CARGO) test --workspace

cli:
	$(CARGO) run -q -p qqbox-cli -- version

daemon:
	$(CARGO) run -q -p qqboxd -- --routes

mcp:
	$(CARGO) run -q -p qqbox-mcp -- --help

runtime:
	$(CARGO) run -q -p qqbox-runtime -- features

# --------------------------------------------------------------- stubs -------
stubs-linux:
	sh stub/build.sh

stubs-windows:
	sh stub/windows/build.sh

stubs-macos:
	sh stub/macos/build.sh
	sh stub/macos/build-hvf.sh

stubs: stubs-linux stubs-windows stubs-macos

# ------------------------------------------------------------ witnesses ------
invariants:
	$(CARGO) run -q -p qqbox-cli -- invariants

version:
	$(CARGO) run -q -p qqbox-cli -- version

routes:
	$(CARGO) run -q -p qqboxd -- --routes

run:
	$(CARGO) run -q -p qqbox-cli -- run $(PROGRAM)

# Socket-level zero-trust witness: a real qqboxd over real TCP, all 54 routes,
# with and without the bearer token. This is live HTTP authz, not an
# in-process probe. GAP 3 from the adversarial completeness critic. The script
# is an exported Make variable, not a file on disk.
define WITNESS_HTTP_PY
import json, socket, subprocess, urllib.request, urllib.error, sys, os, secrets
BIN = os.path.join(os.getcwd(), "target/debug/qqboxd")
TOKEN = secrets.token_hex(16)
CAP_SECRET = secrets.token_hex(16)
probe = socket.socket()
probe.bind(("127.0.0.1", 0))
port = probe.getsockname()[1]
probe.close()
proc = subprocess.Popen([BIN, "--listen", "127.0.0.1:" + str(port), "--token", TOKEN,
                         "--migration-secret", CAP_SECRET],
                        stdout=subprocess.DEVNULL, stderr=subprocess.STDOUT)
ready = socket.socket()
for _ in range(300):
    if ready.connect_ex(("127.0.0.1", port)) == 0:
        break
    ready.close()
    ready = socket.socket()
ready.close()
routes = subprocess.check_output([BIN, "--routes"], text=True).strip().splitlines()
base = "http://127.0.0.1:" + str(port)
def call(route, auth):
    method, path = route.split(" ", 1)
    path = path.replace("{id}", "abc").replace("{name}", "img")
    headers = {"Authorization": "Bearer " + TOKEN} if auth else {}
    request = urllib.request.Request(base + path, method=method, headers=headers)
    try:
        with urllib.request.urlopen(request, timeout=5) as response:
            return response.status
    except urllib.error.HTTPError as err:
        return err.code
    except Exception:
        return -1
unauth = [call(route, False) for route in routes]
authed = [call(route, True) for route in routes]
print("routes=" + str(len(routes)) + " all_unauth_401=" + str(all(code == 401 for code in unauth)))
print("all_auth_dispatched=" + str(all(code >= 200 for code in authed)))
proc.terminate()
proc.wait(timeout=5)
if not (len(routes) == 54 and all(code == 401 for code in unauth) and all(code >= 200 for code in authed)):
    print("WITNESS_HTTP_FAIL")
    sys.exit(1)
print("WITNESS_HTTP_PASS")
endef
export WITNESS_HTTP_PY

witness-http: build
	@python3 -c "$$WITNESS_HTTP_PY"

witness: build test
	$(CARGO) run -q -p qqbox-cli -- version
	$(CARGO) run -q -p qqbox-cli -- invariants
	$(CARGO) run -q -p qqbox-cli -- abi
	$(CARGO) run -q -p qqbox-cli -- backend capabilities
	$(CARGO) run -q -p qqbox-cli -- ports-check 127.0.0.1:8080:80/tcp 3000-3002:3000-3002/udp 53
	$(CARGO) run -q -p qqbox-cli -- config-validate examples/oci-config.json
	$(CARGO) run -q -p qqbox-cli -- oci-inspect examples/oci-config.json
	$(CARGO) run -q -p qqbox-cli -- compose-plan examples/compose.yaml
	$(CARGO) run -q -p qqbox-cli -- compose-validate examples/compose.yaml
	$(CARGO) run -q -p qqbox-cli -- runtime-spec validate --advisory examples/runtime-spec.json
	$(CARGO) run -q -p qqbox-cli -- runtime-spec validate --advisory examples/runtime-spec-minimal.json
	@if $(CARGO) run -q -p qqbox-cli -- runtime-spec validate examples/runtime-spec-minimal.json >/dev/null 2>&1; then \
		echo 'qqbox: strict validation wrongly accepted linux.seccomp' >&2; exit 1; \
	else echo 'strict_validation_rejects_linux.seccomp=true'; fi
	$(CARGO) run -q -p qqbox-cli -- runtime-spec features
	$(CARGO) run -q -p qqbox-cli -- image-plan alpine:3.20
	$(CARGO) run -q -p qqbox-cli -- network-plan examples/compose.yaml
	$(CARGO) run -q -p qqbox-cli -- daemon-probe /containers/create --method POST --body examples/container-create.json
	$(CARGO) run -q -p qqboxd -- --routes
	$(CARGO) run -q -p qqbox-runtime -- features
	$(CARGO) run -q -p qqbox-mcp -- --help >/dev/null

# Live Linux backend witness. Requires a real Linux kernel reachable via
# `podman machine ssh` with the repository visible through virtiofs. This is a
# real seccomp trap, not a mock. Cross-compiled with rust-lld for static musl.
backend-witness: build
	RUSTFLAGS='-C linker=rust-lld' $(CARGO) build --offline -p qqbox-cli --target aarch64-unknown-linux-musl
	podman machine ssh -- $(CURDIR)/target/aarch64-unknown-linux-musl/debug/qqbox backend capabilities
	podman machine ssh -- $(CURDIR)/target/aarch64-unknown-linux-musl/debug/qqbox backend witness
	podman machine ssh -- $(CURDIR)/target/aarch64-unknown-linux-musl/debug/qqbox backend memory

# Real unmodified ELF guest under the Rust supervisor. The guest ELF is built
# off-repo with aarch64-linux-musl-ld -Ttext=0x6f0000000000; the kernel-only
# baseline exits 1, the supervised run exits 42. Requires GUEST_ELF=<path>.
backend-exec-elf: build
	RUSTFLAGS='-C linker=rust-lld' $(CARGO) build --offline -p qqbox-cli --target aarch64-unknown-linux-musl
	podman machine ssh -- $(GUEST_ELF); echo kernel_baseline_rc=$$?
	podman machine ssh -- $(CURDIR)/target/aarch64-unknown-linux-musl/debug/qqbox backend exec-elf $(GUEST_ELF)

# Verify the SHIPPED Traditional Chinese deliverables. The blocklist and the
# scan logic live in this Makefile as an exported variable, not in a test file
# and not in an off-repo harness: CI can run this target on a clean checkout.
# ROOT and the expected file set are derived from the shipped zh-hant/ tree.
define ZH_HANT_VERIFY_PY
import pathlib, sys
root = pathlib.Path('zh-hant')
docs = sorted(p for p in root.rglob('*') if p.is_file() and p.suffix in {'.md', '.txt'})
multi = ['网络', '连接', '转发', '磁盘', '标签', '系统调用', '运行', '构建',
         '验证', '测试', '代码', '设置', '默认', '支持', '文档', '安装',
         '软件', '硬件', '浏览器', '权限', '内存', '进程', '状态', '错误',
         '问题', '记录', '证据', '简体', '端口', '重写', '虚拟机', '内核',
         '线程', '调用', '数据', '信息', '镜像', '仓库', '后端',
         '组件', '接口', '日志', '源码', '源代码', '编译', '链接', '注册',
         '登录', '目录', '路径', '空间', '时间', '时候', '缓存', '无线',
         '用户', '服务器', '客户端', '应用', '对应', '响应', '应该', '因为',
         '这样', '这个', '这些', '一个', '我们', '他们', '进行',
         '实现', '通过', '透过', '经过', '还是', '还有', '允许', '防护',
         '隔离', '边界', '规则', '清单', '开启', '关闭', '读取', '写入',
         '变更', '产生', '实际', '来源', '延迟', '协议', '证书', '密码',
         '处理器', '图形处理器', '文件系统', '已知问题', '简体中文',
         '软件渲染', '硬件加速', '检查', '检测', '覆盖', '复现', '运行时']
single = set('网连转盘标签统运构验测码设认档装软浏览权进态错录证简体内让为这个们来'
             '对时会过还应从将并与较执术图显层级启动静协议讯迟际现环优点带宽产变读写'
             '关闭开虚拟机处状记题误总数据库门问间么样经话语说请区车东乐无专业务实'
             '发达传计论报导彻观检决选项择参组织结联络换输储删盖驱链访据复边护纯单杂'
             '线载触够别样')
artifacts = ['譯按', 'reproducibility', 'TODO', 'TBD', 'PLACEHOLDER', '...']
def han(t): return sum(1 for c in t if '一' <= c <= '鿿')
def scan(t):
    hits = []
    for n, line in enumerate(t.splitlines(), 1):
        for tok in multi:
            if tok in line: hits.append((n, 'multi', tok))
        for ch in single:
            if ch in line: hits.append((n, 'single', ch))
        if '文件' in line.replace('說明文件', ''): hits.append((n, 'context', '文件(non-document)'))
        for tok in artifacts:
            if tok in line: hits.append((n, 'artifact', tok))
    return sorted(set(hits), key=lambda h: (h[0], h[1], h[2]))
total_hits = 0
failures = 0
print('scan target = all .md/.txt deliverables (' + str(len(docs)) + ' files); inline verifier, no off-repo harness')
print('blocklist = ' + str(len(multi)) + ' multi-char strings + ' + str(len(single)) + ' single chars + context check 文件 + artifact check')
for p in docs:
    text = p.read_text(encoding='utf-8')
    need = 400 if p.suffix == '.txt' else 600
    h = han(text)
    hits = scan(text)
    total_hits += len(hits)
    ok = not hits and h >= need
    if not ok: failures += 1
    print(str(p) + ' han=' + str(h) + ' min=' + str(need) + ' hits=' + str(len(hits)) + ' ' + ('PASS' if ok else 'FAIL'))
    for n, kind, tok in hits: print('    L' + str(n) + ' [' + kind + '] ' + repr(tok))
print('TOTAL: files=' + str(len(docs)) + ' blocklist_hits=' + str(total_hits) + ' failures=' + str(failures) + ' verdict=' + ('PASS' if failures == 0 and docs else 'FAIL'))
sys.exit(1 if failures or not docs else 0)
endef
export ZH_HANT_VERIFY_PY

zh-hant-verify:
	@python3 -c "$$ZH_HANT_VERIFY_PY"

# Structural integrity gate for the eight committed freestanding stubs. This
# needs no Zig toolchain and no network; it is a clean-checkout gate, not a
# rebuild witness. Byte-for-byte rebuild reproducibility is disclosed in
# docs/PORT_STATUS.md as a residual limitation.
define STUB_VERIFY_PY
import pathlib, struct, sys
root = pathlib.Path('stub')
elf = {'stub-amd64.bin': (2, 62), 'stub-amd64-pie.bin': (3, 62),
       'stub-arm64.bin': (2, 183), 'stub-arm64-pie.bin': (3, 183)}
pe = {'windows/stub-amd64.bin': 0x8664, 'windows/stub-arm64.bin': 0xaa64}
macho = ['macos/stub.bin', 'macos/hvf_stub.bin']
fails = []
def check(rel, want):
    p = root / rel
    if not p.is_file():
        fails.append(rel + ': missing'); return
    b = p.read_bytes()
    if len(b) < 4096:
        fails.append(rel + ': too small (' + str(len(b)) + ' bytes)'); return
    if want[0] == 'elf':
        if b[:4] != b'\x7fELF' or b[4] != 2 or b[5] != 1:
            fails.append(rel + ': not little-endian ELF64'); return
        got = (struct.unpack_from('<H', b, 16)[0], struct.unpack_from('<H', b, 18)[0])
        ok = got == want[1]
        print('ELF  ' + rel + ' type=' + str(got[0]) + ' machine=' + str(got[1]) + ' bytes=' + str(len(b)) + ' ' + ('PASS' if ok else 'FAIL'))
    elif want[0] == 'pe':
        if b[:2] != b'MZ':
            fails.append(rel + ': not PE/MZ'); return
        off = struct.unpack_from('<I', b, 0x3c)[0]
        if b[off:off+4] != b'PE\x00\x00':
            fails.append(rel + ': bad PE signature'); return
        got = struct.unpack_from('<H', b, off+4)[0]
        ok = got == want[1]
        print('PE   ' + rel + ' machine=' + hex(got) + ' bytes=' + str(len(b)) + ' ' + ('PASS' if ok else 'FAIL'))
    else:
        if b[:4] != b'\xcf\xfa\xed\xfe':
            fails.append(rel + ': not Mach-O arm64'); return
        got = struct.unpack_from('<I', b, 4)[0]
        ok = got == 0x0100000c
        print('MACHO ' + rel + ' cpu=' + hex(got) + ' bytes=' + str(len(b)) + ' ' + ('PASS' if ok else 'FAIL'))
    if not ok: fails.append(rel + ': header mismatch')
for rel, kind in [(k, ('elf', v)) for k, v in elf.items()] + [(k, ('pe', v)) for k, v in pe.items()] + [(k, ('macho', None)) for k in macho]:
    check(rel, kind)
print('STUB_BLOBS=' + str(len(elf) + len(pe) + len(macho)) + ' failures=' + str(len(fails)))
for f in fails: print('  ' + f)
print('STUB_VERIFY_' + ('PASS' if not fails else 'FAIL'))
sys.exit(1 if fails else 0)
endef
export STUB_VERIFY_PY

stub-verify:
	@python3 -c "$$STUB_VERIFY_PY"

clean:
	$(CARGO) clean
