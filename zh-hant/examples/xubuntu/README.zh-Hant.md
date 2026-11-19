# `examples/xubuntu`：原廠 Xubuntu／XFCE 桌面（繁體中文）

> 這是以原廠 `linuxserver/webtop` ubuntu-xfce 映像檔為基底的桌面範例，透過 `x11vnc` 在 VNC TCP 5905 提供畫面，並以確切的 `linux/arm64` 資訊清單摘要固定版本：`sha256:5f170506429db91eba6aae8c6366185d71bc842980d1b0d9caa179e91c965c55`。它展示的是原廠桌面、原生 `xfce4-terminal` 與真實的 `linux/arm64` Chrome-for-Testing，在「沒有虛擬機器、沒有第二個核心」的 qqbox 沙箱中運作。

## 原廠映像檔與支援範圍

`examples/xubuntu` 以原廠映像檔為基底，不多做桌面改造；多架構 index 的選取讓上面這個 arm64 摘要被確切鎖定，避免日後標籤浮動讓見證無法重現。這也對應已支援的 OCI 表面：映像檔設定、連接埠與連接埠轉送、`/etc/hosts`／`/etc/resolv.conf`／`/etc/hostname` 的產生、磁碟區、標籤、健康檢查、重新啟動政策、Compose（JSON 與 YAML，含 `depends_on` 條件）、多架構 index、capabilities、`seccomp`／`AppArmor` 與資源限制。無法實作的安全欄位會附診斷訊息明確拒絕，不會被靜默丟棄。逐項對照見 [../../docs/OCI_MATRIX.zh-Hant.md](../../docs/OCI_MATRIX.zh-Hant.md)。

建置與安裝需要 `-net`：`apt` 的套件安裝要靠它，監督器會攔截每個 `:53` DNS 查詢、在宿主解析網域名稱，並把 TCP `connect()` 代理到真實的宿主通訊端。映像檔建置使會崩潰的 `ldconfig` dpkg trigger 失效，並修復 `/var/lib/dpkg/status`，讓後續安裝不會被前面的失敗卡住；另一個 [../xfce/README.zh-Hant.md](../xfce/README.zh-Hant.md) 範例則展示 `apk` 在 `-net` 下也能安裝套件。`-allow-domains`、`-allow-cidrs`、`-allow-ports` 只收緊、不放寬；少了 `-net`，來賓沒有 DNS 與宿主可達性，瀏覽器只會顯示通用的離線頁面，這不是故障，而是預設的封閉狀態。

## 原生終端機與配色

`xfce4-terminal`（VTE）能開啟真正的 PTY、畫出提示字元與命令輸出，是因為 `TIOCPKT` 以指標形式的 ioctl 正確轉送：它帶的是指向整數的指標，不是整數本身；把數值當指標會得到錯誤位址，只回傳成功而不啟用封包模式則會留下黑畫面。終端機配色由 `Xfconf` 提供，`terminalrc` 已被移轉，改它不會生效。這兩項修正一起構成了「原生終端機能用」的完整證據，而不是單一錯誤碼被消除。

## 瀏覽器、圖形與 ICMP

瀏覽器是真實的 `linux/arm64` Chrome-for-Testing，不是 NetSurf。它的 setuid 沙箱需要 qqbox 不授予的特權，因此沙箱狀態與 `SwiftShader` 軟體渲染狀態如實回報；macOS 圖形處理器不會透傳給 Linux ELF 來賓，硬體加速不可用。合成 `/sys` 缺少部分背光、電源與桌面服務，相關警告不影響畫面、終端機與對外連線。

ICMP echo（`ping`）只在使用者空間 ICMP echo 客戶端與宿主未特權的 `SOCK_DGRAM`／`IPPROTO_ICMP` 通訊端上有效；未特權環境沒有 `SOCK_RAW`。qqbox 配對通訊端點而非封包，所以沒有 `AF_PACKET`、`TUN`、原始通訊端與來賓核心內 ICMP，802.11 無線網路、`nl80211`、`wpa_supplicant`、`NetworkManager` 也無法模擬。在支援路徑外看到 `ping` 失敗是預期結果；請以 DNS 解析、TCP 與 HTTPS 的實際連線（例如取得 HTTP 200 的真實頁面）作為「網路可用」的證據，並留下記錄與證據供對抗式驗證。

## VNC 暴露與密碼

VNC 由 `x11vnc` 在 TCP 5905 提供。`x11vnc -nopw` 不要求密碼，搭配 `-net` 的連接埠轉送，這個接聽端會被區域網路內的其他主機連上，任何人都能操作來賓桌面。這是部署選擇，不是 qqbox 的隔離不變量；上線前改用 `x11vnc -rfbauth <vncpasswd>`，以支援密碼的 VNC 客戶端連線，並把 5905 限制在可信網路。`-allow-ports` 只會縮小目的連接埠，不會補上密碼。完整威脅模型見 [../../SECURITY.zh-Hant.md](../../SECURITY.zh-Hant.md)。

## 已知限制與誠實邊界

Linux memfd／`seccomp`／hop、macOS Mach／`Seatbelt`／HVF、Windows `LPAC` 三組原生來賓執行後端尚未移植到 Rust；Go 原始碼已移除，儲存庫是 Rust 與 Zig。`cargo build --workspace` 只準備控制平面，`make witness` 只見證控制平面的解析、接受／拒絕與規劃，因此從原始碼建置目前仍無法執行來賓；本頁描述的桌面、終端機與連線畫面來自先前的執行時期見證，不應被解讀為目前工作區已能跑起來。也不得以 `sleep`、timeout 或週期性重查當作正確性的修正；程式庫接受 `Options`／`Config` 選項，不讀環境變數。細節見 [../../docs/PORT_STATUS.zh-Hant.md](../../docs/PORT_STATUS.zh-Hant.md)。

## 說明文件連結

- [../README.zh-Hant.md](../README.zh-Hant.md)、[../xfce/README.zh-Hant.md](../xfce/README.zh-Hant.md)
- [../../README.zh-Hant.md](../../README.zh-Hant.md)、[../../AGENTS.zh-Hant.md](../../AGENTS.zh-Hant.md)、[../../SECURITY.zh-Hant.md](../../SECURITY.zh-Hant.md)
- [../../docs/PORT_STATUS.zh-Hant.md](../../docs/PORT_STATUS.zh-Hant.md)、[../../docs/OCI_MATRIX.zh-Hant.md](../../docs/OCI_MATRIX.zh-Hant.md)、[../../docs/SINGLE_ADDRESS_SPACE.zh-Hant.md](../../docs/SINGLE_ADDRESS_SPACE.zh-Hant.md)
- 英文版：[examples/xubuntu/README.md](../../../examples/xubuntu/README.md)、[README.md](../../../README.md)
