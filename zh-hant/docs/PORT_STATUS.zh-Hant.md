# Rust 移植邊界（繁體中文）

> 這一頁只做一件事：說清楚哪些部分已經在 Rust 裡，哪些還沒有。編譯成功的控制平面不等於完整的執行時期；原生來賓執行後端尚未移植，Go 原始碼已移除，因此從原始碼建置 Rust 工作區只能準備控制平面，目前仍無法執行來賓。這句話必須明講，不能用「建置通過」或「見證通過」帶過。

## 已經在 Rust 裡的部分

- 工作區：`crates/qqbox`（核心）、`crates/qqbox-cli`（CLI `qqbox`）、`crates/qqboxd`（與 Docker 相容的常駐程式 `qqboxd`）。`cargo build --workspace` 是建置入口。
- 型別化設定：`Options`／`Config`、映像檔設定、runtime-spec、Compose、標籤、連接埠、磁碟區、網路模式、capabilities、資源限制、`seccomp`／`AppArmor`、健康檢查與重新啟動政策。無法實作的安全欄位不是靜默丟棄，而是附診斷訊息明確拒絕。
- OCI 表面：映像檔設定、連接埠與連接埠轉送、`/etc/hosts`／`/etc/resolv.conf`／`/etc/hostname` 的產生、磁碟區、標籤、健康檢查、重新啟動政策、Compose（JSON 與 YAML，含 `depends_on` 條件）、多架構 index、capabilities、`seccomp`／`AppArmor`、資源限制。逐項對照見 [OCI_MATRIX.zh-Hant.md](OCI_MATRIX.zh-Hant.md)。
- 網路政策：`-net` 之下的 `:53` DNS 攔截與宿主解析、TCP `connect()` 代理；`-allow-domains`／`-allow-cidrs`／`-allow-ports` 只收緊；封閉預設下沒有 DNS 與宿主可達性，瀏覽器顯示通用離線頁面。
- `vfs` Namespace 作為唯一路徑咽喉，含掛載遮蔽與合成 `/proc`／`/sys`／`/dev`；監督器擁有檔案與網路的資料平面，來賓 Zig stub 只負責轉送。
- 常駐程式：48 條路由、真實 JSON、零信任 bearer 權杖，每一條路由都要求 `Authorization: Bearer <secret>`，沒有未經驗證的預設值。
- 見證：`make witness` 跑過版本、安全不變量、ABI、連接埠、映像檔設定、Compose、runtime-spec、網路計畫與路由清單，並把結果印出。Zig stub 的共享通道版面保持兩端位元組一致；`cargo run -p qqbox-cli -- invariants` 可單獨核對安全不變量。

## 尚未移植：原生來賓執行後端

三組後端都還沒有移植到 Rust：

| 宿主 | 未移植的後端 | 它負責的工作 |
| --- | --- | --- |
| Linux | memfd／`seccomp`／hop | 建立共享通道、安裝文字範圍 `RET_TRAP` 後盾、執行 host-op 執行緒、服務系統呼叫表 |
| macOS | Mach／`Seatbelt`／HVF | 以 Mach task、`Seatbelt` 與 Hypervisor.framework 承載來賓 stub |
| Windows | `LPAC` | 以 AppContainer 與 instrumentation callback 承載來賓 stub |

這三組後端才是「讓 Linux 程式在沒有虛擬機器的情況下真正跑起來」的部分。它們缺席時，`cargo build --workspace` 只會準備並驗證控制平面：`make witness` 見證的是接受／拒絕、解析與規劃結果，不是來賓執行的端對端證明。`qqbox` 的執行規劃可以計算並印出，但不代表來賓被執行；請不要把它寫成「已可執行」，也不要把規劃輸出當作隔離已經生效的證據。

## 已移除的部分

Go 原始碼已經移除，儲存庫僅含 Rust 與 Zig。這不是「Go 仍然是可用的後備執行時期」；沒有後備。也就是說，目前沒有任何從原始碼建置出來的路徑可以真正執行來賓。任何說明文件、註解、提交訊息或回覆都必須照實陳述這個狀態；若有人宣稱已補上，請要求對抗式驗證的記錄與證據，以及三個宿主各自的見證。名稱由 laboon 更名為 qqbox 不改變這項事實，程式識別字與旗標維持拉丁原文也不等於後端已經移植完成。

## 誠實的驗證範圍

- 控制平面可以證明：設定被正確解析、不支援的安全欄位被拒絕、連接埠轉送計畫與 `/etc` 檔案被產生、Compose 依照 `depends_on` 條件排序、48 條路由都需要權杖。
- 控制平面不能證明：宿主檔案讀取被阻擋、RWX 記憶體對應被拒、記憶體上限在來賓內生效、或對外連線在真實來賓中被限制。這些都需要實際執行來賓；目前沒有。
- 常見誤解：`daemon-probe` 之類的診斷是行程內的檢查，用來印出 HTTP 狀態，不是網路授權的端對端測試；`make witness` 不是來賓執行見證。
- 修正原則：不得以 `sleep`、timeout 或週期性重查當作正確性的修正；同步問題請修正同步本身。程式庫吃選項不吃環境，這條在移植過程中沒有讓步。
- 網路限制照實陳述：ICMP echo 只走宿主未特權的 `SOCK_DGRAM`／`IPPROTO_ICMP` 使用者空間路徑；`SOCK_RAW`、`AF_PACKET`、`TUN`、來賓核心內 ICMP、802.11、`nl80211`、`wpa_supplicant`、`NetworkManager` 都不在契約內。

## 接下來要做的事

把三組後端收斂成 Rust 的平臺介面，維持已提交的 Zig stub 位元組不變，重現 memfd／Mach／`LPAC` 的啟動契約，並透過共享通道服務系統呼叫表；每一個宿主都要通過既有的回歸語料與對抗式驗證之後，才談得上「可執行來賓」。在此之前，請把本頁當作邊界，而不是待辦清單的樂觀版本。桌面範例的畫面、終端機的 `TIOCPKT` 指標形式轉送與 `SwiftShader` 軟體渲染狀態，都是先前執行時期的見證，不應被解讀為目前工作區已能執行來賓。

## 說明文件連結

- [../README.zh-Hant.md](../README.zh-Hant.md)、[../AGENTS.zh-Hant.md](../AGENTS.zh-Hant.md)、[../SECURITY.zh-Hant.md](../SECURITY.zh-Hant.md)
- [OCI_MATRIX.zh-Hant.md](OCI_MATRIX.zh-Hant.md)、[SINGLE_ADDRESS_SPACE.zh-Hant.md](SINGLE_ADDRESS_SPACE.zh-Hant.md)
- [../examples/README.zh-Hant.md](../examples/README.zh-Hant.md)
- 英文版：[PORT_STATUS.md](../../docs/PORT_STATUS.md)、[README.md](../../README.md)
