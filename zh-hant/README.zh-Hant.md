# Tencent qqbox（繁體中文）

本頁是 [`../README.md`](../README.md) 的繁體中文鏡像。英文版保留上游
laboon 的使用者體驗，但所有指令、型別與路徑皆為 Rust；本頁沿用相同的章節
順序。程式識別字、旗標、路徑與專有名詞保留拉丁原文。

**Tencent qqbox** 在 Linux、macOS 與 Windows 上，以**沒有虛擬機器、沒有第二個
核心**的方式，安全地執行不受信任的 Linux 程式與 OCI 容器。來賓機器碼直接在
宿主 CPU 上以原生速度執行；只有 `syscall`／`svc` 指令被重寫，使其陷入模擬
Linux ABI 的監督器。這屬於 gVisor 一類的系統呼叫攔截設計。原生執行代表任何
支援的宿主都不需要虛擬機器，但也要求 CPU 架構相符：amd64 或 arm64，兩者之間
不做轉譯。

隔離有**兩層彼此獨立的防線**：監督器的中介——封閉的檔案系統、沒有直接的宿主
路徑、固定的裝置白名單、預設關閉網路並提供可設定的輸出允許清單；以及宿主作業
系統的沙箱（seccomp／Seatbelt／AppContainer）作為逃逸時終止來賓的後盾。為每個
不受信任的程式或代理產生的指令，配置一次性沙箱，只揭露它需要的檔案與網路。

可以當作獨立執行檔、透過容器用戶端、經由 MCP，或嵌入自己的程式使用。各宿主
提供相同的 Linux 來賓行為，但仍受
[平台限制](../docs/permissions-and-limits.md) 約束。qqbox 仍屬新專案，宿主介面
刻意維持精簡；提供 daemon 與 OCI runtime 不應被解讀為已達生產成熟度或完整
相容引擎。

[安裝](#安裝) · [開始使用](#開始使用) · [使用方式](#使用方式) ·
[運作原理](#運作原理) · [效能](#效能) ·
[AI 代理與桌面沙箱](#ai-代理與桌面沙箱)

> **移植邊界（請先閱讀）**：依使用者要求，所有 Go 原始碼已移除；本專案僅含
> Rust 與 Zig。Rust 控制平面、OCI／Compose 型別、48 條 Docker API 路由、
> runtime-spec 驗證、MCP 伺服器與程式庫皆已實作並有實測見證。但三個**原生
> 來賓執行後端**（Linux memfd／seccomp／hop、macOS Mach／Seatbelt／HVF、
> Windows LPAC）**尚未**移植到 Rust，因此 `qqbox run` 與 MCP 的 `run` 工具在備妥
> 執行計畫之後，仍會回傳明確的 `UnsupportedPlatform` 診斷訊息。目前從原始碼
> 建置尚不能啟動來賓。
> [examples/qqbox/](../examples/qqbox/README.md) 中實測運作的 Xubuntu 26 桌面，
> 是以保存在版本庫之外的移除前 Go 執行期二進位檔啟動。完整說明見
> [docs/PORT_STATUS.md](../docs/PORT_STATUS.md)。

## 安裝

**從原始碼建置**（Linux、macOS、Windows；amd64 或 arm64）：

```sh
cargo install --path crates/qqbox-cli
cargo install --path crates/qqboxd
cargo install --path crates/qqbox-mcp
cargo install --path crates/qqbox-runtime
```

各平台的預先建置執行檔置於
[最新發行版](https://github.com/dywongcloud/qqbox/releases/latest)。若要把
qqbox 當作 Rust 程式庫嵌入，在 `Cargo.toml` 加入：

```toml
[dependencies]
qqbox = { path = "crates/qqbox" }   # 發佈後亦可取自 crates.io 或 git
```

從原始碼建置需要 Rust 1.98 或更新版本。各平台的 Zig stub 已隨專案提交
（`stub/*.bin`、`stub/macos/*.bin`、`stub/windows/*.bin`），並以
`include_bytes!` 嵌入，因此建置或引用 qqbox 不需要 Zig，也不需要 C 工具鏈。
首次執行映像需要宿主能存取該 OCI registry；之後使用快取映像，除非
`Options::pull` 要求更新。擷取映像與來賓的網路權限是兩件獨立的事。

## 使用方式

| 介面 | 適用情境 | 進入點 |
|---|---|---|
| **獨立 CLI／執行檔** | 直接執行程式或映像，不需要 daemon。 | [`crates/qqbox-cli`](../crates/qqbox-cli/src/main.rs)：`qqbox run-image alpine:3.20 cat /etc/os-release` |
| **相容 Docker API 的 daemon** | 讓 Docker 或 Moby 用戶端驅動沙箱。它提供 Docker Engine API；每個容器都是一個 qqbox 沙箱，沒有第二個核心。 | [`crates/qqboxd`](../crates/qqboxd/src/main.rs)：`qqboxd --listen 127.0.0.1:2375 --token <secret>` |
| **OCI runtime** | 作為 Docker、containerd、Podman 或 CRI-O 的 runc／crun 替代品。向引擎註冊後，以 `docker run --runtime=qqbox …` 選用。 | [`crates/qqbox-runtime`](../crates/qqbox-runtime/src/main.rs)：`qqbox-runtime create <id> <bundle>` |
| **MCP 伺服器** | 讓 AI 代理呼叫 `run` 工具，取得結束狀態、標準輸出與標準錯誤。支援 stdio 與 streamable HTTP。 | [`crates/qqbox-mcp`](../crates/qqbox-mcp/src/main.rs)：`qqbox-mcp`／`qqbox-mcp --http 127.0.0.1:8080` |
| **Rust 程式庫** | 在自己的程式中嵌入沙箱，不需要 daemon。 | `qqbox` crate：`qqbox::run_image(&ctx, "alpine:3.20", &opts)` |

daemon 與 OCI runtime 是兩種獨立整合：前者服務用戶端，後者執行既有引擎交付的
bundle。選用 qqbox 作為 OCI runtime，並不會移除引擎本身對宿主平台的要求。

## 開始使用

### 以 CLI 執行映像

下載並解開
[版本庫原始碼](https://github.com/dywongcloud/qqbox/archive/refs/heads/feat/rust-port.zip)，
在解開後的目錄開啟終端機：

```sh
cargo build --workspace
./target/debug/qqbox version
./target/debug/qqbox run-image alpine:3.20 cat /etc/os-release
```

Windows 請改用 `qqbox.exe`。原生後端備妥之後，指令會印出 Alpine 的發行資訊
（包含 `ID=alpine`）並成功結束；目前會印出前述 `UnsupportedPlatform` 診斷
訊息。來賓只看到映像的檔案系統，沒有外部網路。加入 PATH 之後，指令即為
`qqbox run-image alpine:3.20 cat /etc/os-release`。

CLI 也揭露已通過型別檢查的控制平面，以下皆以
`cargo run -p qqbox-cli -- <command>` 實測：

```
qqbox invariants                    已強制執行的安全不變量
qqbox abi                           驗證 128 位元組的 Zig 通道 ABI
qqbox address-space                 單一位址空間的決策
qqbox run <program> [args...]       剖析、重寫並規劃來賓 ELF
qqbox config-validate <file>        印出每一項 OCI image-config 構造
qqbox oci-inspect <file>            完整映像設定與構造計數
qqbox ports-check <spec>...         剖析 publish 並印出轉送規劃
qqbox compose-plan <compose.yaml>   把 Compose 檔降為執行個體規劃
qqbox compose-validate <file>       驗證服務、連接埠、磁碟區、depends_on
qqbox runtime-spec features         宣告的 OCI runtime-spec 功能
qqbox runtime-spec validate <file>  套用 config.json，未知項目一律失敗
qqbox image-plan <ref>              registry／manifest／blob URL 與平台
qqbox network-plan <compose.yaml>   網路、別名、轉送與 /etc 檔案
qqbox daemon-probe <path>           在行程內演練一條 Docker API 路由
qqbox daemon --listen <addr>        啟動相容 Docker 的 daemon
```

### 以 Moby／Docker 用戶端驅動沙箱

`qqboxd` 提供 Docker Engine API（v1.43），每條路由都要求零信任的 bearer
驗證。啟動後，把任何 Docker 或 Moby 用戶端指向它：

```sh
cargo run -p qqboxd -- --listen 127.0.0.1:2375 --token my-secret
export DOCKER_HOST=tcp://127.0.0.1:2375
```

對於能送出任意 `Authorization` 標頭的用戶端（透過自訂 `http.Client` 的 Moby
Go 用戶端，或單純的 `curl`），每個請求都帶
`Authorization: Bearer my-secret`：

```sh
curl -H 'Authorization: Bearer my-secret' http://127.0.0.1:2375/_ping
# OK
curl -H 'Authorization: Bearer my-secret' http://127.0.0.1:2375/version
# {"ApiVersion":"1.43","Arch":"arm64","KernelVersion":"qqbox","MinAPIVersion":"1.24","Os":"linux","Version":"0.1.0"}
curl -X POST -H 'Authorization: Bearer my-secret' -H 'Content-Type: application/json' \
  -d '{"Image":"alpine:3.20","Cmd":["echo","hello from qqbox"],"Labels":{"qqbox.test":"1"}}' \
  http://127.0.0.1:2375/containers/create
# {"Id":"39aed3f4…","Warnings":[]}
```

未經驗證的請求回傳 HTTP 401，沒有未驗證的預設值。省略 `--token` 時，啟動會
產生並印出權杖。48 條路由列於 `qqboxd --routes` 與 `GET /qqbox/v1/routes`；
已強制執行的不變量的位於 `GET /qqbox/v1/invariants`。

### 註冊為 OCI runtime

`qqbox-runtime` 針對 bundle 的 `config.json` 實作 runc／crun 指令介面。例如在
`/etc/docker/daemon.json` 向引擎註冊：

```json
{
  "runtimes": {
    "qqbox": { "path": "/usr/local/bin/qqbox-runtime" }
  }
}
```

之後使用 `docker run --runtime=qqbox …`。也可以直接驅動：

```sh
qqbox-runtime --root /run/qqbox-runtime features
qqbox-runtime --root /run/qqbox-runtime create demo-1 /path/to/bundle
qqbox-runtime --root /run/qqbox-runtime state demo-1
qqbox-runtime --root /run/qqbox-runtime start demo-1
qqbox-runtime --root /run/qqbox-runtime kill demo-1 SIGTERM
qqbox-runtime --root /run/qqbox-runtime delete demo-1
```

`create` 以 `qqbox::oci::runtime::parse`、`validate` 與 `apply` 剖析並驗證
`config.json`；不支援的安全構造會**拒絕**，不會靜略捨棄。狀態是 `--root` 之下
真實的 JSON。`start` 目前回傳前述原生後端診斷訊息。

### 在 Rust 中嵌入沙箱

在原始碼目錄之外建立新目錄：

```sh
cargo new qqbox-hello
cd qqbox-hello
```

`Cargo.toml`：

```toml
[package]
name = "qqbox-hello"
version = "0.1.0"
edition = "2021"

[dependencies]
qqbox = { path = "../literbox/crates/qqbox" }
```

`src/main.rs`：

```rust
use qqbox::{run_image, Context, Options, Stdio};

fn main() {
    let ctx = Context::default();
    let mut opts = Options::default();
    opts.args = Some(vec!["echo".into(), "hello from qqbox".into()]);
    opts.stdout = Stdio::Inherit;
    opts.stderr = Stdio::Inherit;
    match run_image(&ctx, "alpine:3.20", &opts) {
        Ok(code) => std::process::exit(code),
        Err(err) => {
            eprintln!("qqbox: {err}");
            std::process::exit(1);
        }
    }
}
```

```sh
cargo run
```

原生後端備妥之後，來賓會印出 `hello from qqbox` 並成功結束；目前則由錯誤分支
印出 `UnsupportedPlatform` 診斷訊息。來賓只看到映像的檔案系統，沒有外部網路。
輸出必須明確連接，因為程式庫預設丟棄標準輸出與標準錯誤（`Stdio::Null`）。

若要執行 Linux ELF 執行檔而非映像，改用 `qqbox::run`。未設定
`Options::root` 時，執行檔由宿主路徑載入，來賓沒有檔案系統；`root` 提供根目錄。
引數、掛載、環境、映像快取與輸出選項見
[crates/qqbox/src/options.rs](../crates/qqbox/src/options.rs)；API 見
`cargo doc -p qqbox --open`。

## 什麼能在其上執行

上游 README 記錄的工作負載屬於 **Go 執行期**的實測集合，既不保證每個 Linux
程式或系統呼叫都受支援，也**不是**對這份僅含 Rust 的程式庫的宣稱。本 Rust
移植已見證的範圍是具型別的控制平面：OCI image config、runtime spec、
Compose、連接埠、網路、磁碟區、標籤、`vfs` Namespace、48 條 Docker API 路由
與安全不變量，詳見
[docs/OCI_MATRIX.md](../docs/OCI_MATRIX.md)。來賓執行工作負載仍取決於原生
後端的移植進度，見 [docs/PORT_STATUS.md](../docs/PORT_STATUS.md)。

| 工作負載 | 上游 Go 執行期 | Rust 移植 |
|---|---|---|
| 語言與執行期 | Go、C/C++、Rust、Java、Python、PHP、Ruby、Perl、Node.js（V8 JIT）、.NET | 後端待移植 |
| 資料庫與伺服器 | PostgreSQL、MariaDB、Redis | 後端待移植 |
| 瀏覽器 | 無頭 Chromium：DOM 與截圖 | 後端待移植；桌面已以版本庫外執行期實測 |
| 巢狀容器 | podman 與 Docker 各自執行自己的容器 | 後端待移植 |
| 工具與巢狀 | `ptrace` 工具如 `strace`；laboon 內執行 laboon | 後端待移植 |
| 桌面 | 完整 XFCE 桌面 | 已實測：[examples/qqbox/](../examples/qqbox/README.md) |

## 運作原理

載入的 ELF 映像在宿主行程中與一個小型 Zig stub 一同執行。重寫器以各架構的
分類器找出系統呼叫位置，不需要反組譯器，再把這些指令改成陷入監督器的跳板。
其餘指令原封不動在 CPU 上執行。

stub 透過共享記憶體通道把請求轉交 Rust 監督器，由它模擬 Linux ABI 並擁有
檔案與網路的資料平面。stub 只處理必須留在來賓位址空間的工作：執行緒控制、
訊號跳板，以及持有描述項的標準輸出快速路徑。128 位元組的 `chanPage` 配置
固定於 [crates/qqbox/src/zig/abi.rs](../crates/qqbox/src/zig/abi.rs)。

### 兩層彼此獨立的隔離

**監督器中介。** 每個來賓路徑都在單一受限的 `vfs` Namespace 中解析。映像
rootfs、bind 掛載、overlay，以及合成的 `/proc`、`/sys`、`/dev` 都共用同一個
檔案系統邊界；裝置節點來自固定白名單。來賓無法直接解析宿主路徑；宿主檔案
只能經由明確設定的 root 或掛載取得。這讓一次執行能使用映像的檔案，同時只
共享呼叫者選定的宿主目錄。

**宿主作業系統後盾。** 若有系統呼叫躲過重寫，宿主沙箱會終止來賓
（`Error::Killed`）：Linux 為 seccomp、macOS 為 Seatbelt、Windows 為
AppContainer。在 Linux 上，映射記憶體、持有描述項與 fork 屬於另一條永不執行
來賓程式碼的 host-op 執行緒。來賓執行緒的 seccomp 過濾器，不會因為 stub 需要
這些操作而放行。[AGENTS.md](../AGENTS.md) 定義這些邊界。

### 安全預設

qqbox 預設即受限制：來賓只有 loopback 網路、看不到宿主檔案系統、沒有共享
記憶體；每項授權（`Options::net`、`Options::mounts`、
`Options::allow_domains` 等）都必須明確給予。完整控制項與目前限制見
[docs/permissions-and-limits.md](../docs/permissions-and-limits.md)。程式庫
接收選項而非環境變數；只有執行檔讀取環境。

### 定位

| 執行期 | 來賓執行方式 | Linux 來賓需要 VM | 整合方式 |
|---|---|---|---|
| qqbox | 原生程式碼；模擬系統呼叫 | 否 | CLI、Docker API、OCI runtime、MCP、Rust 程式庫 |
| Docker／Podman | Linux namespace 與 cgroup | macOS／Windows 需要 | 容器執行期 |
| gVisor | 攔截系統呼叫；使用者空間核心 | 否 | 沙箱執行期 |
| Firecracker | microVM 中的來賓核心 | 是 | VMM |
| Kata Containers | 每容器一個 VM 的來賓核心 | 是 | 容器執行期 |
| Windows Sandbox | Windows 來賓，不是 Linux | 是（Hyper-V） | 作業系統功能 |
| Microsoft LiteBox | Library OS；TEE／機密運算後端 | 通常需要（TEE／VM） | 程式庫 |

差別在於執行邊界，不是安全排名。qqbox 結合監督器中介與宿主作業系統後盾。
Docker 在 Linux 上共享宿主核心；在 macOS 與 Windows 上，其 Linux VM 提供另一
層邊界。請依工作負載與所需邊界選擇。

## 效能

上游 README 記錄的是 **Go 執行期**的量測（Apple M4、macOS 26.5：Alpine
`echo` 12 ms 對 Docker Desktop 187 ms、Node 啟動 129 ms 對 200 ms、CPU 迴圈
約略持平；Linux CPU 密集工作約為原生的 1.01 倍、單純攔截的系統呼叫約
224 ns）。這些是**已記錄的量測，不是對這份 Rust 程式庫的重新驗證**，而且
不能沿用：目前沒有原生 Rust 來賓後端，因此無法在此產生端對端量測。方法與
如實狀態見 [docs/PERF.md](../docs/PERF.md)。原生 CPU 執行不代表每種工作負載
都有原生速度：系統呼叫處理、資料複製與行程啟動仍須付出成本。

## AI 代理與桌面沙箱

對於會產生程式碼的代理，MCP 伺服器提供直接的執行邊界：每次 `run` 呼叫都從
OCI 映像建立一次性沙箱，並回傳結束碼與擷取的輸出。從版本庫根目錄建置並
啟動：

```sh
cargo build --workspace
./target/debug/qqbox-mcp                        # stdio 傳輸
./target/debug/qqbox-mcp --http 127.0.0.1:8080 # streamable HTTP
```

讓 MCP 用戶端以絕對路徑啟動 `qqbox-mcp` 執行檔以使用 stdio 傳輸，或執行
`qqbox-mcp --http 127.0.0.1:8080` 並連線到 `http://127.0.0.1:8080/` 使用
HTTP 模式。Windows 請用 `qqbox-mcp.exe`。例如呼叫 `run` 工具：

```json
{"image":"python:3.12-alpine","command":["python3","-c","print(2+2)"]}
```

上游結果包含結束碼 `0` 與標準輸出 `4\n`。在這份 Rust 程式庫中，呼叫會回傳
`isError: true`，並在 `stderr` 帶上原生後端診斷訊息，因為後端尚未移植；它
不會捏造一次成功的執行。只有在伺服器以 `--allow-net` 啟動且呼叫要求
`"network": true` 時，才可使用外部網路。預設每次執行逾時為 120 秒，每條
輸出串流上限為 1 MiB；伺服器提供 `--timeout` 與 `--max-output` 調整。

伺服器另提供不需要來賓後端、且已完整實測的工具：
`qqbox_invariants`、`qqbox_routes`、`qqbox_runtime_spec_validate`、
`qqbox_config_validate`、`qqbox_ports_check`、`qqbox_compose_plan` 與
`qqbox_version`。

[Xubuntu 26 桌面沙箱](../examples/qqbox/README.md) 在 Xvfb 上以 `x11vnc`
啟動 XFCE，具備底部工作列、可運作的桌布、PulseAudio、真正的 Chromium、
`root@qbox` 以及完整的 GNU 工具鏈。它是這個分支實測運作的桌面，以保存在
版本庫之外的移除前 Go 執行期二進位檔啟動；可重現的 Dockerfile 與啟動指令
置於 `examples/qqbox/`。

```sh
sh examples/qqbox/fetch-chromium.sh
sh examples/qqbox/fetch-extras.sh
qqbox build -net -t qqbox-xubuntu:26 -f examples/qqbox/Dockerfile -o qqbox-xubuntu26.tar examples/qqbox
qqbox -image qqbox-xubuntu:26 -net -e VNC_PORT=5905 -mount /tmp/qqbox-out:/out
vncviewer 127.0.0.1:5905
```

## 授權

Apache-2.0。上游歸屬與品牌說明見 [LICENSE](../LICENSE)、
[COPYRIGHT](../COPYRIGHT) 與 [NOTICE.md](../NOTICE.md)。
