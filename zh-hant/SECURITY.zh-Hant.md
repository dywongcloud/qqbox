# 安全政策與威脅模型（繁體中文）

> qqbox 讓未經修改的 Linux ELF 與 OCI 容器直接在宿主 CPU 上執行，沒有虛擬機器，也沒有第二個核心。正因為它是沙箱，任何讓來賓繞過監督器中介、碰到宿主記憶體／檔案／網路，或破壞監督器本身的路徑，都是安全問題。本頁說明 qqbox 隔離什麼、不隔離什麼，以及發現問題時該如何回報。

## 威脅模型

來賓視為完全不受信任。需要保護的資產是宿主記憶體、宿主檔案、宿主可達的網路、監督器與控制平面狀態，以及各執行個體彼此的隔離。隔離不來自第二個核心，而來自三件事同時成立：只有系統呼叫與同類指令被重寫並陷入監督器；所有跨越邊界的檔案與網路動作都經監督器的資料平面與 `vfs` Namespace；Linux 的 `seccomp` 程式對監督器服務的呼叫下 `RET_TRAP`（`SIGSYS` 導回 chanPage）、對固定的直接逃逸清單下終止判決、其餘允許；Go 時代的預設拒絕文字範圍後盾並未移植，靜態重寫因此是正確性的必要條件。macOS 以 `Seatbelt`、Windows 以 instrumentation callback 搭配 `LPAC` 阻止繼續執行；三者行為不對稱，改動時不得把它們寫成同一種結果，也不得宣稱未移植的後盾存在。

因此，以下情形屬於安全問題：以未經 `vfs` 的路徑讀取或寫入宿主檔案；未經許可的對外連線；寫入另一個執行個體的通道頁、重寫快取或對外政策；偽造控制平面的回應；讓監督器執行來賓可控的程式碼；或讓任一條路由在未經驗證下被呼叫。相對地，不在契約內的能力不是漏洞：原始通訊端、`SOCK_RAW`、`AF_PACKET`、`TUN`、來賓核心內 ICMP、802.11 無線網路、`nl80211`、`wpa_supplicant`、`NetworkManager`、macOS 圖形處理器透傳，以及 Chromium 的 setuid 沙箱特權。請勿以「修好它」為由要求放寬邊界；這類能力應留在不在契約內的清單，並如實陳述。

## 零信任常駐程式權杖

`qqboxd` 是與 Docker 相容的常駐程式，提供 54 條路由。每一條路由都要求 `Authorization: Bearer <secret>`；沒有未經驗證的預設值，也沒有「本機就放行」的例外。啟動時以 `--token <secret>` 帶入憑證，並把憑證視為不可回復的機密：不要寫入記錄檔、不要放進映像檔或 Compose 設定、不要提交到儲存庫、不要在回報問題時附上真實權杖；輪替時舊憑證應立即失效。控制平面與資料平面的授權不可混為一談：取得 API 存取權，不等於取得宿主檔案讀取權，也不等於取得來賓位址空間的寫入權。若懷疑有路由略過驗證，請把它當作隔離邊界的缺陷回報，而不是當作便利功能保留。

## 對外允許清單與 `--net`

`Options::net`（Rust CLI `--net`）是唯一啟用來賓對外連線的開關。啟用後，監督器攔截每一個送往 `:53` 的 DNS 查詢、在宿主端完成網域名稱解析，並把 TCP `connect()` 代理到真實的宿主通訊端。未啟用時，來賓沒有 DNS，也沒有宿主可達性，瀏覽器只會顯示通用的離線頁面；這是預設的封閉狀態，不是故障。

`allow_domains`、`allow_cidrs` 與 `allow_ports` 只「收緊」：加上去只會縮小可達的網域名稱、CIDR 與目的連接埠，不能放寬、不能取代 `--net`，也不能授予新權限。單破折號旗標僅屬移除前的 Go 執行期。部署時先以最窄清單開始，再把需要的目的逐項加入；每次變更都留下設定、見證輸出與判定理由。套件登錄 `apt` 與 `apk` 在啟用網路後可安裝，映像檔建置使會崩潰的 `ldconfig` dpkg trigger 失效並修復 `/var/lib/dpkg/status`，讓後續安裝持續可用；這項修復不應被誤用為放寬對外政策的理由。程式庫以 `Options`／`Config` 承接政策，不讀環境變數，避免隱藏的全域輸入改變邊界。

## ICMP 與無線網路的界線

ICMP echo（`ping`）透過使用者空間的 ICMP echo 客戶端，經宿主未特權的 `SOCK_DGRAM`／`IPPROTO_ICMP` 通訊端送出；未特權環境拿不到 `SOCK_RAW`。qqbox 配對的是通訊端點而非封包，所以不提供 `AF_PACKET`、`TUN`、原始通訊端與來賓核心內 ICMP；802.11 無線網路、`nl80211`、`wpa_supplicant`、`NetworkManager` 無法模擬。在支援路徑之外看到 `ping` 失敗是預期結果；請以 DNS 解析、TCP 與 HTTPS 的實際連線作為「網路可用」的證據。若有人宣稱已補上封包層，請要求對抗式驗證的記錄與證據，不要接受口頭保證。

## 瀏覽器與圖形處理器

Debian Chromium 150 `linux/arm64` 是真實瀏覽器，不是 NetSurf。它的 setuid 沙箱需要 qqbox 不授予的特權，因此沙箱狀態與 `SwiftShader` 軟體渲染狀態都如實回報；macOS 的圖形處理器不會透傳給 Linux ELF 來賓，硬體加速不可用。這些是如實陳述的已知限制，不是隔離被突破的跡象，也不應以關閉監督器檢查或放寬 capabilities 來「修正」。

## VNC 暴露

`examples/xfce` 與 `examples/xubuntu` 以 `x11vnc` 在 VNC TCP 5905 提供桌面。`x11vnc -nopw` 不要求密碼，在啟用網路的連接埠轉送下會被區域網路內的其他主機連上，任何人都能操作來賓桌面；這是部署選擇，不是 qqbox 的隔離不變量。上線前改用 `x11vnc -rfbauth <vncpasswd>`，以支援密碼的 VNC 客戶端連線，同時把 5905 的暴露範圍限制在可信網路。原生 `xfce4-terminal`（VTE）依賴 `TIOCPKT` 的指標形式 ioctl 轉送，配色由 `Xfconf` 提供；兩者與 VNC 的驗證彼此獨立，加上 `allow_ports` 只會縮小目的連接埠，並不會補上密碼。

## 誠實的驗證範圍

Linux memfd／`seccomp`／hop 原生來賓執行後端已移植到 Rust 並有即時見證；macOS Mach／`Seatbelt`／HVF 與 Windows `LPAC` 兩組尚未移植。Go 原始碼已移除，儲存庫是 Rust 與 Zig。一般 `qqbox run` 仍回傳 `UnsupportedPlatform`，但 `qqbox backend exec-elf` 會執行真實未修改的靜態 aarch64 ELF。`make witness` 只見證控制平面的接受／拒絕與規劃結果，不是完整執行時期的端對端證明。因此在目前狀態下，不應宣稱「宿主檔案讀取已被阻擋」或「記憶體上限與 RWX 對應已端對端驗證」；請把這類宣示標為控制平面結果，並到 [docs/PORT_STATUS.zh-Hant.md](docs/PORT_STATUS.zh-Hant.md) 查看邊界。也不得以 `sleep`、timeout 或週期性重查當作正確性的修正，同步問題必須修正同步本身。

## 如何回報問題

疑似隔離缺陷請走私人通報管道，依[英文版 SECURITY.md](../SECURITY.md) 的指示使用儲存庫 Security 頁的「Report a vulnerability」，不要開公開 issue，也不要把可重現的攻擊步驟貼到公開討論區。回報請附：qqbox 的提交版本、宿主作業系統與架構、最小重現（程式或映像檔、完整旗標、`--net` 與允許清單）、你認為被跨越的隔離邊界，以及可重現的記錄與證據；若涉及憑證，請改以遮罩後的值說明，切勿附上真實權杖。我們會先確認邊界，再協調修正與揭露時程。功能限制（無線網路、原始通訊端、圖形處理器透傳、setuid 沙箱）請走一般 issue，並附上英文版說明文件的對照，避免把已知問題誤報成漏洞。

## 說明文件連結

- [README.zh-Hant.md](README.zh-Hant.md)、[AGENTS.zh-Hant.md](AGENTS.zh-Hant.md)、[llms.zh-Hant.txt](llms.zh-Hant.txt)
- [docs/PORT_STATUS.zh-Hant.md](docs/PORT_STATUS.zh-Hant.md)、[docs/OCI_MATRIX.zh-Hant.md](docs/OCI_MATRIX.zh-Hant.md)、[docs/SINGLE_ADDRESS_SPACE.zh-Hant.md](docs/SINGLE_ADDRESS_SPACE.zh-Hant.md)
- [examples/README.zh-Hant.md](examples/README.zh-Hant.md)、[英文版 README.md](../README.md)
