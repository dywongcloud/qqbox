# OCI 建構項目與支援狀態（繁體中文）

> 本表是「已支援的 OCI 表面」的逐項對照。狀態欄只有三種：支援、條件式支援、以及不在契約內。判定來自控制平面的見證（`make witness`）與英文版說明文件；因為原生來賓執行後端尚未移植到 Rust，Go 原始碼已移除，下表不是來賓執行的端對端證明。

## 1. 已支援的建構項目

| 構面 | 建構項目 | 狀態 | 說明 |
| --- | --- | --- | --- |
| 映像檔設定 | `Entrypoint`、`Cmd`、`Env`、`WorkingDir`、`User`、`ExposedPorts`、`Volumes`、`Labels` | 支援 | 解析後納入容器設定與檢視中繼資料，標籤不被靜默丟棄。 |
| 映像檔設定 | `StopSignal`、`Healthcheck`、`Hostname`、`Domainname` | 支援 | 驅動停止、健康狀態與 `/etc/hostname` 的產生。 |
| 映像檔設定 | 架構、`os`、`variant`、`os.version`、`os.features`、多架構 index | 支援 | 依平臺選取資訊清單；`examples/xubuntu` 以確切的 arm64 摘要固定原廠映像檔。 |
| 連接埠 | `80`、`8080:80`、主機 IP 綁定、範圍、`/tcp`、`/udp`、`/sctp`、短暫主機連接埠 | 支援 | 產生連接埠轉送計畫並做衝突偵測，停止或刪除時可拆解。 |
| 網路檔案 | `/etc/hosts`、`/etc/resolv.conf`、`/etc/hostname` | 支援 | 由轉送器與控制平面產生，不假手來賓，也不讀環境變數。 |
| 磁碟區 | bind、唯讀 bind、命名／匿名／`tmpfs` 磁碟區、傳播旗標 | 支援 | 全部落在 `vfs` Namespace；裝置節點來自固定允許清單。 |
| 標籤 | 映像檔與容器標籤、清單篩選、檢視中繼資料 | 支援 | 標籤會保留，並回傳為真實 JSON。 |
| 生命週期 | 健康檢查、重新啟動政策、停止訊號 | 支援 | 由常駐程式維護狀態，寫入與讀取都經過監督器。 |
| Compose | JSON 與 YAML、服務、連接埠、磁碟區、`depends_on` 條件 | 支援 | 轉為依依賴排序的執行個體計畫，健康條件會被讀取。 |
| Compose | 環境、標籤、`cap_add`／`cap_drop`、`security_opt`、部署與重新啟動 | 支援 | 以 `Options`／`Config` 承接，不以環境變數偷偷改行為。 |
| 安全 | capabilities、`seccomp`、`AppArmor`、資源限制、`no-new-privileges` | 支援／明確拒絕 | 可實作者套用；無法實作者附診斷訊息拒絕，不靜默丟棄。 |
| Docker 相容 API | 容器、映像檔、網路、磁碟區、exec、events、build、auth、system 共 48 條路由 | 支援 | 每條路由都要求 `Authorization: Bearer <secret>`；零信任、無未驗證預設。 |

## 2. 條件式支援

| 項目 | 必要條件 | 說明 |
| --- | --- | --- |
| 來賓對外連線 | 明確加上 `-net` | 監督器攔截每個 `:53` DNS 查詢、在宿主解析網域名稱，並把 TCP `connect()` 代理到真實宿主通訊端；沒有 `-net` 就沒有 DNS 與宿主可達性，瀏覽器顯示通用離線頁面。 |
| 目的地限制 | `-allow-domains`、`-allow-cidrs`、`-allow-ports` | 三者只收緊、不放寬；加上去只會縮小可達的目的地，不能取代 `-net`，也不會授予新權限。 |
| 套件登錄 | `-net` | `apt` 與 `apk` 的安裝可正常運作；映像檔建置使會崩潰的 `ldconfig` dpkg trigger 失效並修復 `/var/lib/dpkg/status`。 |
| ICMP echo（`ping`） | 使用者空間 ICMP echo 客戶端 + 宿主未特權 `SOCK_DGRAM`／`IPPROTO_ICMP` | 這是唯一的支援路徑；`SOCK_RAW` 不可用，路徑外的 `ping` 失敗是預期結果。以 DNS、TCP 與 HTTPS 的實際連線作為網路可用的證據。 |
| Chrome-for-Testing 沙箱與圖形 | 如實回報 | setuid 沙箱特權不授予，沙箱狀態與 `SwiftShader` 軟體渲染狀態照實呈現；macOS 圖形處理器不透傳給 Linux ELF 來賓，硬體加速不可用。 |
| VNC TCP 5905 | `x11vnc` + `-net` 連接埠轉送 | `x11vnc -nopw` 在區域網路可達，部署時改用 `-rfbauth` 加密碼；這是部署選擇，不是隔離不變量。 |

## 3. 不在契約內

| 項目 | 狀態 | 原因 |
| --- | --- | --- |
| 802.11 無線網路、`nl80211`、`wpa_supplicant`、`NetworkManager` | 不在契約內 | qqbox 配對通訊端點而非封包，沒有無線網卡可模擬。 |
| `AF_PACKET`、`TUN`、原始通訊端、`SOCK_RAW`、來賓核心內 ICMP | 不在契約內 | 沒有封包層，也沒有第二個核心；未特權環境拿不到 `SOCK_RAW`。 |
| 第二個核心、虛擬機器、macOS 圖形處理器透傳、Chrome-for-Testing setuid 沙箱特權 | 不在契約內 | 來賓機器碼在宿主 CPU 上原生執行；這些能力需要 qqbox 不授予的特權或硬體路徑。 |
| 靜默丟棄不支援的安全欄位 | 明確禁止 | 無法實作的欄位必須附診斷訊息拒絕，這是本表要消滅的失敗模式。 |

## 證據與邊界

逐項的標準很單純：每個建構項目不是被實作並見證，就是被明確拒絕，絕不靜默丟棄。`make witness` 會解析本目錄與範例的映像檔設定、Compose 與常駐程式請求，印出連接埠轉送計畫、網路計畫與路由清單供核對；`cargo build --workspace` 則建置 `crates/qqbox`、`crates/qqbox-cli`、`crates/qqboxd`。請把這些輸出理解為控制平面的證據。

必須照實說明的限制是：Linux memfd／`seccomp`／hop、macOS Mach／`Seatbelt`／HVF、Windows `LPAC` 三組原生來賓執行後端尚未移植到 Rust，Go 原始碼已移除，儲存庫是 Rust 與 Zig。從原始碼建置只準備控制平面，不能執行來賓，因此「宿主檔案讀取被阻擋」「RWX 記憶體對應被拒」之類的宣稱，目前只能是控制平面的接受／拒絕結果，不是來賓執行的端對端證明。若有人宣稱某列已升級為端對端支援，請要求對抗式驗證的記錄與證據；也不得以 `sleep`、timeout 或週期性重查當作正確性的修正。

## 說明文件連結

- [../README.zh-Hant.md](../README.zh-Hant.md)、[../SECURITY.zh-Hant.md](../SECURITY.zh-Hant.md)、[../AGENTS.zh-Hant.md](../AGENTS.zh-Hant.md)
- [PORT_STATUS.zh-Hant.md](PORT_STATUS.zh-Hant.md)、[SINGLE_ADDRESS_SPACE.zh-Hant.md](SINGLE_ADDRESS_SPACE.zh-Hant.md)
- [../examples/README.zh-Hant.md](../examples/README.zh-Hant.md)
- 英文版：[OCI_MATRIX.md](../../docs/OCI_MATRIX.md)、[README.md](../../README.md)
