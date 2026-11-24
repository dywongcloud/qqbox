# 權限與限制（繁體中文）

qqbox 預設即受限制；以下每一項能力都必須明確啟用。程式庫接受型別化的
選項，不讀取環境變數：Rust 的入口是 `Options`
（`crates/qqbox/src/options.rs`），常駐程式與 OCI 的序列化形式是 `Config`
（`crates/qqbox/src/config.rs`）。下游說明裡出現的 `With…` 建構器名稱屬於
上游 Go API，在本 Rust 專案中並不存在。

## 網路

網路預設關閉，來賓只有 loopback，如同 `docker --network none`。
`Options { net: true, .. }`（Rust CLI 為 `--net`）會啟用經監督器代理的對外連線；
常駐程式的等價形式是 `Config { net: true, .. }`。請再加上
`allow_domains`、`allow_cidrs` 與 `allow_ports` 加以限縮；只啟用網路並不會
自動建立限縮清單。網域名規則包含子網域，CIDR 規則加入可直接連線的位址，
連接埠規則限制目的連接埠。

`Options { no_net: true, .. }` 在 `net` 缺席時連 loopback 也移除。Windows
x86-64 的啟動契約即使在 loopback 也要求 `net`；該契約在本 Rust 樹中尚未
移植，此處只如實記錄邊界。

## 檔案系統

`Options::mounts` 與 `Config::mounts` 接受型別化的
`Mount { host, guest, read_only }`：讀寫掛載會暴露宿主目錄，並限制在該子樹
內；唯讀掛載會以 `EROFS` 拒絕建立、修改、刪除與更名。路徑解析一律經過
`vfs::Namespace::resolve`，而 `vfs::path::confined` 會拒絕 `..` 逸出，以及
正規化之後落在根目錄之外的符號連結。掛載也可能暴露其下的宿主 Unix
socket，因此一次掛載就是一次明確的授權。

## 記憶體

可寫且可執行的來賓記憶體，為了 V8 之類的 JIT，預設允許；
`reject_rwx: true` 會拒絕它。沒有預設的記憶體上限。`max_memory` 限制 Linux
上的映射位址空間，以及 Windows 上 stub 行程的認可記憶體；**macOS 尚未
強制執行**。在本 Rust 樹中，這些是型別化的控制平面數值：Linux 的
seccomp／trap 後端會對它所映射的 ELF 區段強制 W^X，完整來賓執行時期的
記憶體會計則尚未移植。

## 安全預設與後盾

宿主作業系統後盾是不對稱的。在 Linux 上，躲過重寫的系統呼叫會被
`RET_TRAP` 導入 Rust 監督器，而不是終止來賓；macOS Seatbelt 與 Windows
x86-64 instrumentation callback 會終止，Windows arm64 沒有 callback 後盾。
授予 API 存取權不等於授予宿主檔案讀取權，也不等於授予來賓位址空間的寫入
權；控制平面與資料平面的授權不可混為一談。常駐程式以零信任 bearer 權杖
保護每一條路由，還原能力則由獨立的第二組密鑰衍生，兩者不可互相推算。

## 來源

型別化的表面見
[options.rs](../../crates/qqbox/src/options.rs)、
[config.rs](../../crates/qqbox/src/config.rs) 與
[vfs/mod.rs](../../crates/qqbox/src/vfs/mod.rs)；後端邊界見
[NATIVE_BACKEND.zh-Hant.md](NATIVE_BACKEND.zh-Hant.md) 與
[PORT_STATUS.zh-Hant.md](PORT_STATUS.zh-Hant.md)。本頁只描述契約，不把
控制平面的接受或拒絕当成來賓執行的端對端證明；即時見證請看該兩頁所列
的 Linux 後端指令。
