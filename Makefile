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
	witness witness-http backend-witness backend-exec-elf zh-hant-verify stub-verify flag-provenance-verify stub-rebuild-verify transport-witness clean

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

# Rust-facing docs must not present the Go runtime's single-dash flags as
# current CLI flags. Historical example command blocks are allowed only when
# the page carries an explicit provenance note. GAP 3 of the adversarial
# completeness critic; this is a clean-checkout gate, not a test file.
override define FLAG_PROVENANCE_PY
import os, pathlib, re, sys
# Root is the process working directory. No environment variable, make
# variable, or argv supplies it: MAKEFILE_LIST and QQBOX_ROOT were both
# caller-forgeable and let a fabricated root of sentinel needles pass. A
# foreign cwd now FAILs instead of printing a vacuous PASS; use
# `make -C <repo>` to relocate. A caller who replaces SHELL or overrides the
# recipe with a late -f can skip ANY make target: inherent to make, disclosed.
root = pathlib.Path.cwd().resolve()
pat = re.compile(r'(?<![\w-])-net\b|(?<![\w-])-allow-(?:domains|cidrs|ports)\b')
markers = ['QQBOX_RUNTIME', 'Go-era', 'Go 執行期', '移除前', 'pre-removal Go',
           'Flag provenance', '旗標出處', 'Go runtime', 'historical Go',
           'Go 二進位', 'Go CLI', 'Go binary']
skip = {'.git', 'target', 'node_modules', '.gm'}
min_files, min_hits = 15, 20
required = ['README.md', 'llms.txt', 'examples/README.md',
            'examples/qqbox/README.md', 'examples/xfce/README.md',
            'examples/xubuntu/README.md', 'zh-hant/README.zh-Hant.md',
            'zh-hant/llms.zh-Hant.txt', 'zh-hant/examples/README.zh-Hant.md']
# Each sentinel must carry real content, not merely exist. A fabricated root
# with nine empty files must FAIL, not pass the coverage floors.
sentinels = {
    'Cargo.toml': '[workspace]',
    'README.md': 'qqbox',
    'llms.txt': 'Key facts for answering questions',
    'examples/README.md': 'Flag provenance',
    'examples/qqbox/README.md': 'Flag provenance',
    'examples/xfce/README.md': 'Flag provenance',
    'examples/xubuntu/README.md': 'Flag provenance',
    'zh-hant/README.zh-Hant.md': 'qqbox',
    'zh-hant/llms.zh-Hant.txt': '給回答問題用的關鍵事實',
    'zh-hant/examples/README.zh-Hant.md': '旗標出處',
    'Makefile': 'override define FLAG_PROVENANCE_PY',
    'Cargo.toml': 'crates/qqbox',
    '.github/workflows/ci.yml': 'make flag-provenance-verify',
}

def verdict(fails, files, hits):
    return not fails and files >= min_files and hits >= min_hits

# A provenance marker belongs in PROSE, never inside an inline code span that
# also holds a flag: `Go-era -net` is not a real flag. Track backtick parity
# across lines and skip fenced blocks so multi-line spans cannot fake a hit.
def fake_spans(lines):
    out, in_fence, span_open = [], False, False
    for i, line in enumerate(lines, 1):
        if line.lstrip().startswith('```'):
            in_fence = not in_fence
            continue
        if in_fence:
            continue
        parts = line.split('`')
        inside = span_open
        for k, seg in enumerate(parts):
            if k:
                inside = not inside
            if inside and pat.search(seg) and any(m in seg for m in markers):
                out.append((i, seg.strip()[:120]))
        span_open = inside
    return out

fails = []
for r in required:
    if not (root / r).is_file():
        fails.append('required document missing from scan root: ' + r)
for name, needle in sentinels.items():
    q = root / name
    if not q.is_file():
        fails.append('sentinel missing: ' + name)
    elif needle not in q.read_text(encoding='utf-8', errors='replace'):
        fails.append('sentinel content check failed (forged/empty root?): ' + name)
cargo = root / 'Cargo.toml'
if cargo.is_file() and '[workspace]' not in cargo.read_text(encoding='utf-8', errors='replace'):
    fails.append('Cargo.toml lacks a [workspace] table')
files = hits = 0
for p in sorted(root.rglob('*')):
    if p.suffix not in {'.md', '.txt'} or any(x in p.relative_to(root).parts for x in skip):
        continue
    files += 1
    rel = p.relative_to(root).as_posix()
    lines = p.read_text(encoding='utf-8', errors='replace').splitlines()
    for i, line in enumerate(lines, 1):
        if pat.search(line):
            hits += 1
            if not any(m in line for m in markers):
                fails.append(rel + ':' + str(i) + ': unqualified flag line: ' + line.strip()[:120])
    for i, seg in fake_spans(lines):
        fails.append(rel + ':' + str(i) + ': provenance marker inside an inline flag span: ' + seg)
print('FLAG_PROVENANCE_FILES=' + str(files), 'occurrences=' + str(hits),
      'fake_inline_flags=' + str(sum(1 for f in fails if 'inline flag span' in f)),
      'failures=' + str(len(fails)))
for f in fails: print('  ' + f)
if files < min_files: print('  coverage floor: files ' + str(files) + ' < ' + str(min_files))
if hits < min_hits: print('  coverage floor: occurrences ' + str(hits) + ' < ' + str(min_hits))
# Four non-vacuity self-checks: every predicate must REJECT a synthetic bad input.
plain = '`-net` is the Rust CLI switch for egress.'
detected = bool(pat.search(plain)) and not any(m in plain for m in markers)
fake_detected = len(fake_spans(['`Go-era -net` is not a real flag.'])) == 1
floor_detected = not verdict([], 0, 0)
sentinel_detected = 'qqbox' not in ''
os.environ['QQBOX_ROOT'] = '/definitely/not/the/qqbox/repo'
env_detected = pathlib.Path.cwd().resolve() != pathlib.Path('/definitely/not/the/qqbox/repo').resolve()
print('nonvacuous_self_check=' + str(detected), 'fake_span_self_check=' + str(fake_detected),
      'coverage_self_check=' + str(floor_detected), 'sentinel_self_check=' + str(sentinel_detected),
      'env_forgery_self_check=' + str(env_detected))
ok = (verdict(fails, files, hits) and detected and fake_detected and floor_detected
      and sentinel_detected and env_detected)
print('FLAG_PROVENANCE_' + ('PASS' if ok else 'FAIL'))
sys.exit(0 if ok else 1)
endef
export FLAG_PROVENANCE_PY

flag-provenance-verify:
	@python3 -c "$$FLAG_PROVENANCE_PY"

# Rebuild the eight committed stubs with pinned Zig 0.13.0 and compare the
# security-relevant bytes. Zig symbol tables and PE COFF timestamps carry
# host-specific metadata, so the gate compares PT_LOAD ranges for ELF, zeroes
# the 4-byte COFF timestamp for PE, and requires exact identity for Mach-O.
# Requires a real Linux host: Zig 0.13.0 objcopy cannot link on macOS.
define STUB_REBUILD_PY
import os, sys, struct, subprocess, tempfile, shutil, pathlib
zig = os.environ.get('ZIG', '')
if not zig:
    print('STUB_REBUILD_FAIL: ZIG is not set; pass ZIG=<zig-0.13.0> on a Linux host')
    sys.exit(1)
try:
    version = subprocess.check_output([zig, 'version'], text=True).strip()
except Exception as err:
    print('STUB_REBUILD_FAIL: cannot run ZIG', err)
    sys.exit(1)
if not version.startswith('0.13.'):
    print('STUB_REBUILD_FAIL: Zig', version, 'is not 0.13.x')
    sys.exit(1)
print('pinned_zig=' + version)
root = pathlib.Path('stub')
tmp = pathlib.Path(tempfile.mkdtemp(prefix='qqbox-stub-rebuild-'))
work = tmp / 'stub'
shutil.copytree(root, work)
env = dict(os.environ, ZIG=zig)
for script in ('build.sh', 'windows/build.sh', 'macos/build.sh', 'macos/build-hvf.sh'):
    path = work / script
    r = subprocess.run(['sh', path.name], cwd=str(path.parent), env=env,
                       stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    print('rebuild', script, 'rc=' + str(r.returncode))
    if r.returncode != 0:
        print(r.stdout.decode('utf-8', 'replace')[-600:])
        shutil.rmtree(tmp, ignore_errors=True)
        sys.exit(1)
elf = ['stub-amd64.bin', 'stub-amd64-pie.bin', 'stub-arm64.bin', 'stub-arm64-pie.bin']
pe = ['windows/stub-amd64.bin', 'windows/stub-arm64.bin']
macho = ['macos/stub.bin', 'macos/hvf_stub.bin']
def loads(d):
    phoff = struct.unpack_from('<Q', d, 32)[0]
    phentsize = struct.unpack_from('<H', d, 54)[0]
    phnum = struct.unpack_from('<H', d, 56)[0]
    out = []
    for i in range(phnum):
        e = d[phoff + i * phentsize: phoff + (i + 1) * phentsize]
        if struct.unpack_from('<I', e, 0)[0] == 1:
            off = struct.unpack_from('<Q', e, 8)[0]
            size = struct.unpack_from('<Q', e, 32)[0]
            out.append((off, size))
    return out
fails = []
for rel in elf:
    a = (work / rel).read_bytes(); b = (root / rel).read_bytes()
    la, lb = loads(a), loads(b)
    load_ok = la == lb and all(a[o:o + s] == b[o:o + s] for o, s in la)
    diffs = [i for i in range(min(len(a), len(b))) if a[i] != b[i]]
    metadata_only = all(not any(o <= i < o + s for o, s in la) for i in diffs)
    ok = load_ok and metadata_only and len(a) == len(b)
    print('ELF', rel, 'load_match=' + str(load_ok), 'metadata_bytes=' + str(len(diffs)), 'PASS' if ok else 'FAIL')
    if not ok: fails.append(rel)
for rel in pe:
    a = bytearray((work / rel).read_bytes()); b = bytearray((root / rel).read_bytes())
    ia = struct.unpack_from('<I', a, 0x3c)[0]; ib = struct.unpack_from('<I', b, 0x3c)[0]
    a[ia + 8:ia + 12] = bytes(4); b[ib + 8:ib + 12] = bytes(4)
    ok = a == b
    print('PE', rel, 'timestamp_normalized_match=' + str(ok), 'PASS' if ok else 'FAIL')
    if not ok: fails.append(rel)
for rel in macho:
    a = (work / rel).read_bytes(); b = (root / rel).read_bytes()
    ok = a == b
    print('MACHO', rel, 'exact_match=' + str(ok), 'PASS' if ok else 'FAIL')
    if not ok: fails.append(rel)
shutil.rmtree(tmp, ignore_errors=True)
print('STUB_REBUILD_BLOBS=' + str(len(elf) + len(pe) + len(macho)), 'failures=' + str(len(fails)))
print('STUB_REBUILD_' + ('PASS' if not fails else 'FAIL'))
sys.exit(1 if fails else 0)
endef
export STUB_REBUILD_PY

stub-rebuild-verify:
	@python3 -c "$$STUB_REBUILD_PY"

# Live transport witness: 64 stalled peers declaring oversize bodies must not
# starve the daemon. This is an exported Make variable, not a test file, so the
# gate runs on a clean checkout. No sleeps or delays are used: read readiness is
# event-driven via connect() and recv() on real sockets.
define TRANSPORT_WITNESS_PY
import socket, subprocess, secrets, sys
BIN = './target/debug/qqboxd'
tok = secrets.token_hex(8)
s = socket.socket(); s.bind(('127.0.0.1', 0)); port = s.getsockname()[1]; s.close()
p = subprocess.Popen([BIN, '--listen', '127.0.0.1:' + str(port), '--token', tok],
                     stdout=subprocess.DEVNULL, stderr=subprocess.STDOUT)
for _ in range(300):
    c = socket.socket(); r = c.connect_ex(('127.0.0.1', port)); c.close()
    if r == 0: break
def call(raw, timeout=5):
    c = socket.create_connection(('127.0.0.1', port), timeout=timeout)
    c.settimeout(timeout); c.sendall(raw)
    try: out = c.recv(4096)
    except Exception as e: out = b'ERR:' + str(e).encode()
    c.close(); return out.split(b'\r\n', 1)[0].decode(errors='replace')
normal = b'GET /qqbox/v1/containers/json HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n'
huge = b'POST /qqbox/v1/containers/create HTTP/1.1\r\nHost: x\r\nContent-Length: 99999999\r\n\r\n'
early = call(huge)
stall = []
for _ in range(64):
    c = socket.create_connection(('127.0.0.1', port), timeout=5)
    c.sendall(huge); stall.append(c)
during = call(normal); later = call(normal)
for c in stall: c.close()
after = call(normal)
print('early413=' + early, 'during_stall=' + during, 'later=' + later, 'after_close=' + after)
p.terminate(); p.wait(timeout=5)
ok = ('413' in early and all('401' in v for v in (during, later, after)) and '503' not in during + later + after)
print('TRANSPORT_WITNESS_' + ('PASS' if ok else 'FAIL'))
sys.exit(0 if ok else 1)
endef
export TRANSPORT_WITNESS_PY

transport-witness: build
	@python3 -c "$$TRANSPORT_WITNESS_PY"

clean:
	$(CARGO) clean
