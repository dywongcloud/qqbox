# `examples/xfce`：Alpine 上的 XFCE 桌面（繁體中文）

> 旗標出處：本頁的單破折號 `-net`、`-allow-domains`、`-allow-cidrs`、
> `-allow-ports` 是移除前 Go 執行期的旗標，用於歷史桌面範例的指令；
> 本儲存庫的 Rust CLI 使用 `--net`、`--no-net` 與 `--allow-domain`，
> CIDR 及連接埠限制由 `Options`／`Config` 欄位設定。

> 這是一個以 Alpine 為基底的 XFCE 桌面範例，透過 `x11vnc` 在 VNC TCP 5905 提供畫面。它展示的不是合成截圖，而是來賓內真正執行的 XFCE 工作階段、原生 `xfce4-terminal` 與 Debian Chromium 150 `linux/arm64`。qqbox 讓未經修改的 Linux ELF 與 OCI 容器直接在宿主 CPU 上執行，沒有虛擬機器，也沒有第二個核心；只有系統呼叫與同類指令被重寫並陷入監督器，`vfs` Namespace 是唯一的檔案系統。

## 內容與啟動條件

`examples/xfce` 以 Alpine 建立 XFCE 工作階段，並以 `x11vnc` 在 VNC TCP 5905 提供 RFB 畫面。建置階段需要 `移除前 Go 執行期的 -net`：`apk` 的套件安裝必須透過監督器對外，而 `-net` 正是那個開關。啟用後監督器攔截每一個 `:53` DNS 查詢、在宿主解析網域名稱，並把 TCP `connect()` 代理到真實的宿主通訊端；沒有 `-net` 就沒有 DNS 與宿主可達性，瀏覽器只會顯示通用的離線頁面。`-allow-domains`、`-allow-cidrs`、`-allow-ports` 只收緊、不放寬，加上去只會縮小可達的目的地，不能取代 `-net`，也不能授予新權限。

映像檔建置使會崩潰的 `ldconfig` dpkg trigger 失效，並修復 `/var/lib/dpkg/status`，讓之後的安裝仍能持續；這對 Debian 系的 [../xubuntu/README.zh-Hant.md](../xubuntu/README.zh-Hant.md) 尤其關鍵。`apt` 與 `apk` 在 `移除前 Go 執行期的 -net` 下都能安裝套件，這是「套件登錄可用」的實際證據，不是對來賓開啟任意網路的授權。所有讀取、寫入、開啟、關閉都經由監督器的資料平面，來賓端只負責轉送。

## 原生終端機為什麼能動

`xfce4-terminal` 使用 VTE，需要真正的 PTY。它之所以能開啟子殼並畫出提示字元與輸出，是因為 `TIOCPKT` 以指標形式的 ioctl 被正確轉送到宿主端：這個 ioctl 帶的是指向整數的指標，不是整數本身；把數值當成指標會讓核心解參考錯誤位址，而只回傳成功卻不啟用封包模式，則會讓 VTE 讀到無法分框的串流，留下黑畫面。終端機的配色來自 `Xfconf`；`terminalrc` 已被移轉，改它不會改變前景與背景。這兩件事合起來，才是「原生終端機能用」的完整原因，也是見證時應該一起留下的記錄與證據。

## 瀏覽器與圖形

安裝的是真實的 Debian Chromium 150 `linux/arm64`，不是 NetSurf。它的 setuid 沙箱需要 qqbox 不授予的特權，因此沙箱狀態與 `SwiftShader` 軟體渲染狀態都如實回報：不能以「已啟用硬體加速」或「沙箱正常」之類的說法掩飾。macOS 的圖形處理器不會透傳給 Linux ELF 來賓，合成 `/sys` 也缺少部分背光與電源服務；這些警告不影響桌面、終端機與對外連線。色彩、字型與視窗管理由來賓內的 XFCE 與 Xvfb 提供，監督器只負責系統呼叫與資料平面，不會替來賓偽造圖形能力。

## 網路與 ICMP 的界線

ICMP echo（`ping`）有明確的支援路徑：使用者空間 ICMP echo 客戶端經宿主未特權的 `SOCK_DGRAM`／`IPPROTO_ICMP` 通訊端送出；未特權環境沒有 `SOCK_RAW`。qqbox 配對的是通訊端點而不是封包，因此沒有 `AF_PACKET`、沒有 `TUN`、沒有原始通訊端、也沒有來賓核心內的 ICMP；802.11 無線網路、`nl80211`、`wpa_supplicant` 與 `NetworkManager` 都無法模擬。在支援路徑之外看到 `ping` 失敗，是預期結果，不是服務中斷。請以 DNS 解析、TCP 與 HTTPS 的實際連線（例如取得真實頁面）作為「網路可用」的證據，並保留記錄與證據供對抗式驗證；不要用 `sleep`、timeout 或週期性重查去「等」一個連線變成可用，那只會把錯誤搬到更少出現的時序。

## VNC 暴露與密碼

`x11vnc -nopw` 不要求密碼。搭配 `移除前 Go 執行期的 -net` 的連接埠轉送，TCP 5905 的接聽端會被區域網路內的其他主機連上，任何人都能操作這個來賓桌面。這不是 qqbox 隔離邊界被跨越，而是部署選擇；上線前請改用 `x11vnc -rfbauth <vncpasswd>`，並以支援密碼的 VNC 客戶端連線。不要把 `-nopw` 帶到不受信任的網路，也不要以為加上 `-allow-ports` 就能補上密碼：允許清單只收緊目的連接埠，不提供身分驗證。憑證與密碼請勿寫入記錄檔或提交到儲存庫。

## 已知限制

Linux memfd／`seccomp`／hop 已移植到 Rust 並有即時見證，macOS Mach／`Seatbelt`／HVF 與 Windows `LPAC` 兩組尚未移植；Go 原始碼已移除，儲存庫是 Rust 與 Zig。一般 `qqbox run` 仍回傳 `UnsupportedPlatform`，但 `qqbox backend exec-elf` 會執行真實未修改的靜態 aarch64 ELF。本頁描述的桌面畫面來自先前的執行時期見證，不應被解讀為目前工作區已能執行完整桌面。也不得以 `sleep`、timeout 或週期性重查當作正確性的修正；啟動時序問題請修正同步本身。其餘已知問題請見 [../../README.zh-Hant.md](../../README.zh-Hant.md) 與 [../../docs/PORT_STATUS.zh-Hant.md](../../docs/PORT_STATUS.zh-Hant.md)。

## 說明文件連結

- [../README.zh-Hant.md](../README.zh-Hant.md)、[../xubuntu/README.zh-Hant.md](../xubuntu/README.zh-Hant.md)
- [../../README.zh-Hant.md](../../README.zh-Hant.md)、[../../SECURITY.zh-Hant.md](../../SECURITY.zh-Hant.md)、[../../AGENTS.zh-Hant.md](../../AGENTS.zh-Hant.md)
- [../../docs/OCI_MATRIX.zh-Hant.md](../../docs/OCI_MATRIX.zh-Hant.md)、[../../docs/SINGLE_ADDRESS_SPACE.zh-Hant.md](../../docs/SINGLE_ADDRESS_SPACE.zh-Hant.md)
- 英文版：[examples/xfce/README.md](../../../examples/xfce/README.md)、[README.md](../../../README.md)
