# 範例（繁體中文）

> 旗標出處：本頁的單破折號 `-net`、`-allow-domains`、`-allow-cidrs`、
> `-allow-ports` 是移除前 Go 執行期的旗標，用於歷史桌面範例的指令；
> 本儲存庫的 Rust CLI 使用 `--net`、`--no-net` 與 `--allow-domain`，
> CIDR 及連接埠限制由 `Options`／`Config` 欄位設定。

> 這個目錄收錄 qqbox 的桌面範例。`examples/xfce` 是 Alpine 上的 XFCE 桌面，`examples/xubuntu` 是原廠 `linuxserver/webtop` ubuntu-xfce；兩者都以 `x11vnc` 在 VNC TCP 5905 提供畫面。範例的目的，是讓人看見在「沒有虛擬機器、沒有第二個核心」的前提下，真實的 Linux 桌面、原生終端機與瀏覽器如何透過監督器運作。

## 兩個桌面範例

| 範例 | 基底 | 重點 |
| --- | --- | --- |
| `examples/xfce` | Alpine | 自建 XFCE 工作階段、`apk` 安裝、原生 `xfce4-terminal` |
| `examples/xubuntu` | 原廠 `linuxserver/webtop` ubuntu-xfce，arm64 資訊清單 `sha256:5f170506429db91eba6aae8c6366185d71bc842980d1b0d9caa179e91c965c55` | 以確切摘要固定原廠映像檔、`apt` 安裝、桌面本身保持原廠 |

兩者共通的關鍵事實：VNC 由 `x11vnc` 在 TCP 5905 提供；`xfce4-terminal`（VTE）能開啟真正的 PTY 並畫出文字，是因為 `TIOCPKT` 以指標形式的 ioctl 正確轉送。這個 ioctl 帶的是指向整數的指標，不是整數本身；把數值當指標傳會得到錯誤位址，只回傳成功卻不啟用封包模式，則會讓 VTE 讀到無法分框的串流而留下黑畫面。終端機配色不是來自 `terminalrc`，而是由 `Xfconf` 提供，改前者不會改變前景與背景。

`移除前 Go 執行期的 -net` 是必要條件：套件登錄的 `apt` 與 `apk` 安裝都要靠它。啟用後監督器攔截每一個送往 `:53` 的 DNS 查詢、在宿主解析網域名稱，並把 TCP `connect()` 代理到真實的宿主通訊端；映像檔建置使會崩潰的 `ldconfig` dpkg trigger 失效，並修復 `/var/lib/dpkg/status`，讓後續安裝不會被前面的失敗卡住。少了 `-net`，來賓沒有 DNS 也沒有宿主可達性，瀏覽器只會顯示通用的離線頁面；`-allow-domains`、`-allow-cidrs`、`-allow-ports` 只用來收緊，不能用來開啟連線或授予新權限。

瀏覽器是實際的 Debian Chromium 150 `linux/arm64`，不是 NetSurf。它的 setuid 沙箱需要 qqbox 不授予的特權，因此沙箱狀態與 `SwiftShader` 軟體渲染狀態如實回報；macOS 的圖形處理器不會透傳給 Linux ELF 來賓，請不要期待硬體加速。合成 `/sys` 缺少部分背光與電源服務、桌面出現的警告，不影響終端機、畫面與對外連線；把它們如實記錄下來，比用「已啟用」之類的說法掩飾更有價值。

## 網路與 ICMP 的界線

ICMP echo（`ping`）只在使用者空間 ICMP echo 客戶端與宿主未特權的 `SOCK_DGRAM`／`IPPROTO_ICMP` 通訊端上有效，`SOCK_RAW` 拿不到。qqbox 配對通訊端點而非封包，所以沒有 `AF_PACKET`、`TUN`、原始通訊端與來賓核心內 ICMP；802.11 無線網路、`nl80211`、`wpa_supplicant`、`NetworkManager` 也無法模擬。在支援路徑外看到 `ping` 失敗是預期結果，不是服務中斷。請以 DNS 解析、TCP 與 HTTPS 的實際連線當作「網路可用」的證據，並保留記錄與證據供對抗式驗證，而不是以通用離線頁面的外觀下結論。

## 安全提醒

`x11vnc -nopw` 不要求密碼；在 `移除前 Go 執行期的 -net` 的連接埠轉送下，這個 5905 接聽端會被區域網路內的其他主機連上，任何人都能操作來賓桌面。這是部署選擇，不是 qqbox 的隔離不變量，也不代表監督器被突破。上線前請改用 `x11vnc -rfbauth <vncpasswd>`，以支援密碼的 VNC 客戶端連線，同時把 5905 限制在可信網路。加上 `-allow-ports` 只會縮小目的連接埠，並不會補上密碼。完整的威脅模型與通報方式見 [../SECURITY.zh-Hant.md](../SECURITY.zh-Hant.md)。

## 建置與見證的誠實邊界

`cargo build --workspace` 與 `make witness` 屬於控制平面；`make witness` 會拿本目錄的 OCI 設定、Compose 與常駐程式請求範本做解析與規劃見證，並印出結果供核對。請注意：Linux memfd／`seccomp`／hop 已移植到 Rust 並有即時見證，macOS Mach／`Seatbelt`／HVF 與 Windows `LPAC` 兩組尚未移植；Go 原始碼已移除，儲存庫是 Rust 與 Zig。一般 `qqbox run` 仍回傳 `UnsupportedPlatform`，但 `qqbox backend exec-elf` 會執行真實未修改的靜態 aarch64 ELF；桌面範例的畫面證據來自先前的執行時期，不應被解讀為「一般路徑已能執行完整桌面」。也不得以 `sleep`、timeout 或週期性重查當作正確性的修正；啟動時序的問題請修正同步本身。細節見 [../docs/PORT_STATUS.zh-Hant.md](../docs/PORT_STATUS.zh-Hant.md)。

## 說明文件連結

- [xfce/README.zh-Hant.md](xfce/README.zh-Hant.md)、[xubuntu/README.zh-Hant.md](xubuntu/README.zh-Hant.md)
- [../README.zh-Hant.md](../README.zh-Hant.md)、[../AGENTS.zh-Hant.md](../AGENTS.zh-Hant.md)、[../SECURITY.zh-Hant.md](../SECURITY.zh-Hant.md)
- [../docs/PORT_STATUS.zh-Hant.md](../docs/PORT_STATUS.zh-Hant.md)、[../docs/OCI_MATRIX.zh-Hant.md](../docs/OCI_MATRIX.zh-Hant.md)、[../docs/SINGLE_ADDRESS_SPACE.zh-Hant.md](../docs/SINGLE_ADDRESS_SPACE.zh-Hant.md)
- 英文版：[examples/README.md](../../examples/README.md)、[README.md](../../README.md)
