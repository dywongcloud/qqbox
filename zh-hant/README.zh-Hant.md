# qqbox

> qqbox 是 Tencent 的來賓執行沙箱：未經修改的 Linux ELF 執行檔與 OCI 容器，直接在宿主 CPU 上以原生機器碼執行，沒有虛擬機器，也沒有第二個核心。只有系統呼叫與同類指令被重寫，使其陷入監督器；宿主端後盾為 Linux `seccomp` 的 `RET_TRAP`（`SIGSYS`）、macOS `Seatbelt`，以及 Windows instrumentation callback 搭配 `LPAC`。

本頁是專案說明文件的繁體中文版。專案正由 laboon 更名為 Tencent qqbox；程式識別字、旗標、路徑與摘要一律保留拉丁原文，舊名稱只在指向英文版說明文件時出現。本目錄是繁體中文說明文件鏡像，英文版說明文件置於專案根目錄，故連結以 `../` 指向英文版。此項安排為本系列的預設，並非額外假設。

## 目錄

- [1. 什麼是 qqbox](#1-什麼是-qqbox)
- [2. 架構與執行模型](#2-架構與執行模型)
- [3. 建置](#3-建置)
- [4. 執行與網路](#4-執行與網路)
- [5. OCI 與 Compose 支援範圍](#5-oci-與-compose-支援範圍)
- [6. 安全模型](#6-安全模型)
- [7. 已知限制與已知問題](#7-已知限制與已知問題)
- [8. 說明文件連結](#8-說明文件連結)

## 1. 什麼是 qqbox

qqbox 讓不受信任的 Linux 程式以原生的速度在宿主處理器上執行，同時把程式與宿主之間的邊界收斂到單一處：系統呼叫。來賓的機器碼不解譯、不反組譯後轉譯、也不逐指令插樁；重寫器只找出 `syscall`／`svc` 一類指令所在的位置，把它們改成陷入監督器的跳板，其餘位元組保持原樣。效能損耗因此跟隨系統呼叫密度，而不是一個固定的倍率。隔離不是靠第二個核心或虛擬機器，而是靠「所有跨越邊界的動作都必須經過監督器」這件事；漏網的系統呼叫則由上述宿主端後盾攔下。

監督器模擬 Linux ABI，並以自己的資料平面處理檔案與網路；來賓看到的檔案系統只有 `vfs` Namespace，來賓看到的網路則是監督器配對的通訊端點，而不是封包。這樣的取捨換來極小的啟動延遲與接近原生的運算，但也畫出清楚的界線：沒有封包層，就沒有無線網卡、原始通訊端與來賓核心內的 ICMP。少了這些能力不是待補的缺口，而是設計契約的一部分，說明文件必須如實陳述。

## 2. 架構與執行模型

程式碼分兩層。來賓位址空間內的 stub 與原生輔助程式是 Zig；監督器與控制平面是 Rust。Zig 端只處理必須留在來賓位址空間的事：執行緒控制、訊號跳板與標準輸入輸出的快速路徑；檔案與網路的資料平面由 Rust 監督器持有，來賓端只負責轉送。兩者之間以版面固定的共享通道通訊，設定變更必須同時維持兩端位元組一致，否則 ABI 偏移會在最難察覺的地方冒出來。

Rust 工作區有三個 crate：`crates/qqbox` 是核心，`crates/qqbox-cli` 產生指令列程式 `qqbox`，`crates/qqboxd` 產生與 Docker 相容的常駐程式 `qqboxd`。常駐程式提供 48 條路由，並採零信任設計：每一條路由都要求 `Authorization: Bearer <secret>`，不存在未經驗證的預設開放路徑。控制平面把 OCI 設定、Compose 計畫、映像檔快取與重寫快取集中管理，讓同一個監督器可以規劃多個互不共享位址空間的執行個體；共享的是控制平面，不是頁表。

`vfs` Namespace 是唯一的檔案系統：所有路徑都經由此單一咽喉解析，`mount`、bind、`pivot_root` 與 overlay 都只是 Namespace 內的操作，`/proc`、`/sys`、`/dev` 與 cgroup 樹也都是同一套物件。監督器不直接拿宿主路徑當作來賓路徑；裝置節點來自固定允許清單。程式庫的行為由 `Options`／`Config` 之類的選項決定，不讀環境變數；只有可執行檔、範例與測試工具才讀環境。六條不可讓步的規則（只重寫系統呼叫、`vfs` 為唯一檔案系統、監督器擁有資料平面、Zig 來賓／Rust 監督器、程式庫吃選項不吃環境、以及絕不以 `sleep`／timeout／週期性重查當作正確性修正）詳見 [AGENTS.zh-Hant.md](AGENTS.zh-Hant.md)。

## 3. 建置

```sh
cargo build --workspace
make witness
```

`cargo build --workspace` 會建置三個 crate；`make witness` 則跑過控制平面的見證清單，包含版本、安全不變量、ABI、連接埠、映像檔設定、Compose 計畫、runtime-spec、網路計畫、常駐程式路由等，並把結果印出來供核對。已提交的 Zig stub 以 `include_bytes!` 嵌入，純 Rust 建置不需要另行取得 Zig 工具鏈；若要重新產生 stub，請依英文版 README 與 Makefile 的指示處理。

Go 原始碼已經移除，儲存庫只剩 Rust 與 Zig。請特別注意一項誠實的邊界：原生來賓執行後端尚未移植到 Rust，因此從原始碼建置整個 Rust 工作區，只會準備好控制平面，目前仍無法真正執行來賓。`make witness` 見證的是控制平面的接受／拒絕與規劃結果，不是來賓執行的端對端證明。細節請見 [docs/PORT_STATUS.zh-Hant.md](docs/PORT_STATUS.zh-Hant.md)。

## 4. 執行與網路

```sh
qqbox version
qqbox invariants
qqbox run <program> -net
qqboxd --listen 127.0.0.1:2375 --token <secret>
```

`qqbox` 的子指令以 `qqbox --help` 與[英文版 README](../README.md)為準；`-net` 是啟用來賓對外連線的開關。啟用後，監督器攔截每一個送往 `:53` 的 DNS 查詢，在宿主端完成網域名稱解析，再把來賓的 TCP `connect()` 代理到真實的宿主通訊端。少了 `-net`，來賓既沒有 DNS，也碰不到宿主可達的網路，瀏覽器只會顯示它通用的離線頁面；這不是故障，而是預設的封閉狀態。常駐程式啟動時請自行帶入憑證，所有路由都要求 `Authorization: Bearer <secret>`。

`-allow-domains`、`-allow-cidrs`、`-allow-ports` 只用來「收緊」，不會放寬任何邊界：把它們加上去只會讓原本可達的目的地被擋下，不能拿來開啟沒有 `-net` 的連線，也不能授予新的權限。套件登錄同理，`apt` 與 `apk` 的安裝在 `-net` 之下可正常運作；映像檔建置會使會崩潰的 `ldconfig` dpkg trigger 失效，並修復 `/var/lib/dpkg/status`，讓後續安裝不會被前面的失敗卡住。

ICMP echo（`ping`）有明確支援的路徑：由使用者空間的 ICMP echo 客戶端，經宿主未特權的 `SOCK_DGRAM`／`IPPROTO_ICMP` 通訊端送出；未特權環境拿不到 `SOCK_RAW`。qqbox 配對的是通訊端點而不是封包，因此沒有 `AF_PACKET`、沒有 `TUN`、沒有原始通訊端、也沒有來賓核心內的 ICMP；802.11 無線網路、`nl80211`、`wpa_supplicant` 與 `NetworkManager` 都無法模擬。在支援路徑之外看到 `ping` 失敗，是預期結果，請改用 DNS、TCP 與 HTTPS 的實際連線作為「網路可用」的證據，並保留記錄與證據。

## 5. OCI 與 Compose 支援範圍

已支援的 OCI 表面包含：映像檔設定、連接埠與連接埠轉送、`/etc/hosts`／`/etc/resolv.conf`／`/etc/hostname` 的產生、磁碟區、標籤、健康檢查、重新啟動政策、Compose（JSON 與 YAML，含 `depends_on` 條件）、多架構 index、capabilities、`seccomp`／`AppArmor`，以及資源限制。`runtime-spec` 中無法實作的安全欄位不是被靜默丟棄，而是帶診斷訊息明確拒絕；逐項對照請見 [docs/OCI_MATRIX.zh-Hant.md](docs/OCI_MATRIX.zh-Hant.md)。

桌面範例見 [examples/README.zh-Hant.md](examples/README.zh-Hant.md)：`examples/xfce` 是 Alpine 桌面，`examples/xubuntu` 是原廠的 `linuxserver/webtop` ubuntu-xfce，兩者都以 `x11vnc` 在 VNC TCP 5905 上提供畫面，並以 RFB 通訊協定傳送桌面。原生的 `xfce4-terminal`（VTE）之所以能正常畫出文字，是因為 `TIOCPKT` 以指標形式的 ioctl 正確轉送；終端機配色來自 `Xfconf`。`x11vnc -nopw` 在區域網路內可被連上，部署時應改用 `-rfbauth` 加上密碼保護。

瀏覽器是實際的 `linux/arm64` Chrome-for-Testing，不是 NetSurf。它的 setuid 沙箱需要 qqbox 不授予的特權，因此沙箱狀態與軟體渲染（`SwiftShader`）狀態都如實回報，不以「已啟用硬體加速」之類的說法掩飾；macOS 的圖形處理器也不會透傳給 Linux ELF 來賓。讀取、寫入、開啟、關閉都經由監督器，沒有第二條通往宿主的捷徑。

## 6. 安全模型

隔離的前提是：來賓不可讀寫宿主記憶體、不可直接讀寫宿主檔案、不可超出設定好的對外政策連線，也不可破壞監督器本身。只有系統呼叫指令被重寫，且每一個漏改寫的呼叫都由宿主端後盾接住；`vfs` Namespace 讓所有路徑解析只有一條路；監督器持有全部檔案與網路資料平面；來賓 stub 以 Zig 撰寫並維持最小職責；程式庫以選項而非環境變數決定行為。常駐程式的零信任 bearer 權杖則確保控制平面不會因為「本機預設」而敞開。完整的威脅模型、權杖處理、對外允許清單、VNC 暴露與通報方式，請見 [SECURITY.zh-Hant.md](SECURITY.zh-Hant.md)。

## 7. 已知限制與已知問題

- 最重要的是：Linux memfd／`seccomp`／hop、macOS Mach／`Seatbelt`／HVF、Windows `LPAC` 這三組原生來賓執行後端尚未移植到 Rust。Go 原始碼已移除，儲存庫是 Rust 與 Zig；因此從原始碼建置只能準備控制平面，不能執行來賓。這句話不應該被「建置成功」或「見證通過」稀釋。
- 無線網路層不在契約內：802.11、`nl80211`、`wpa_supplicant`、`NetworkManager`、`AF_PACKET`、`TUN`、`SOCK_RAW` 與來賓核心內的 ICMP 皆不提供；`ping` 只在上述使用者空間 echo 路徑上有效，路徑外失敗是預期結果。
- Chrome-for-Testing 的 setuid 沙箱特權不授予，圖形處理器不透傳，`SwiftShader` 與沙箱狀態如實呈現。
- `-net` 未啟用時沒有 DNS 與宿主可達性；`-allow-*` 只用來收緊。`-nopw` 的 VNC 會暴露在區域網路，須以 `-rfbauth` 保護。
- 不得以 `sleep`、timeout 或週期性重查當作正確性的修正；遇到競態請修正同步本身。若某個限制被宣稱已解除，請要求對抗式驗證的記錄與證據，而不是接受口頭結論。

## 8. 說明文件連結

- [英文版 README](../README.md)、[英文版 llms.txt](../llms.txt)、[英文版 SECURITY.md](../SECURITY.md)、[英文版 AGENTS.md](../AGENTS.md)
- 繁體中文：[AGENTS.zh-Hant.md](AGENTS.zh-Hant.md)、[llms.zh-Hant.txt](llms.zh-Hant.txt)、[SECURITY.zh-Hant.md](SECURITY.zh-Hant.md)
- 繁體中文技術說明：[docs/PORT_STATUS.zh-Hant.md](docs/PORT_STATUS.zh-Hant.md)、[docs/OCI_MATRIX.zh-Hant.md](docs/OCI_MATRIX.zh-Hant.md)、[docs/SINGLE_ADDRESS_SPACE.zh-Hant.md](docs/SINGLE_ADDRESS_SPACE.zh-Hant.md)
- 繁體中文範例：[examples/README.zh-Hant.md](examples/README.zh-Hant.md)、[examples/xfce/README.zh-Hant.md](examples/xfce/README.zh-Hant.md)、[examples/xubuntu/README.zh-Hant.md](examples/xubuntu/README.zh-Hant.md)
- 授權與著作權資訊請見英文版 README 所指的 `LICENSE` 與 `COPYRIGHT`。
