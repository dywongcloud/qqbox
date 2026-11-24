# 為什麼執行個體不共用單一位址空間（繁體中文）

> 結論：不要把 qqbox 的各個執行個體合併成單一位址空間。每個執行個體保留自己的 stub 處理程序與自己的位址空間；共享的是監督器控制平面，不是頁表。這不是偏好，而是由既有隔離原語推導出來的結果。

## 「單一位址空間」已經指的是什麼

在一次執行之內，來賓與 Zig stub 本來就共用同一個位址空間：來賓可以寫入 stub 的每一個位元組。正因如此，host-op 執行緒把迴圈狀態放在暫存器裡，而每個執行緒的來賓 `seccomp` 過濾器才是真正的邊界；可以對應記憶體、持有描述子與 fork 的動作，只發生在不執行來賓程式碼的 host-op 執行緒上。也就是說，「一個執行個體內部」已經是單一位址空間；問題從來不是這個，而是「執行個體之間」要不要合併。把這兩個層次混為一談，會讓人誤以為現況還需要一次大改造。

## 是什麼把執行個體分開

每次執行都是獨立的 stub 處理程序，隔離是以處理程序為範圍：

| 宿主 | 邊界 |
| --- | --- |
| Linux | 每個執行緒的來賓 `seccomp` 過濾器：Rust 監督器服務的系統呼叫以 `RET_TRAP` 搭配 `SIGSYS` 導回 chanPage，固定的二十項直接逃逸拒絕清單以 `RET_KILL_PROCESS` 終止，其餘允許；memfd 共享通道、hop 執行緒 |
| macOS | `Seatbelt`、Mach task、Hypervisor.framework、共享通道 |
| Windows | `LPAC` AppContainer、instrumentation callback、事件物件 |

請注意三者行為不對稱：Linux 過濾器對已服務的呼叫交回監督器，讓靜態重寫成為最佳化而不是唯一的正確性來源；固定拒絕清單則直接終止處理程序。這裡沒有預設拒絕的文字範圍後盾：Go 時代約 205 項系統呼叫表與文字範圍前綴尚未移植，相關 Go 路徑僅為歷史出處。Rust 的實作在 `crates/qqbox/src/backend/filter.rs` 的 `trap_and_deny` 與 `crates/qqbox/src/backend/linux.rs` 的 `SIGSYS` 處理器，皆已在 Linux aarch64 即時見證。macOS 與 Windows 的後盾則直接阻止繼續執行；Windows arm64 沒有回呼後盾。這份不對稱不能為了「統一介面」而被抹平，否則 JIT 密集的來賓、延遲發現的間接分支位置，以及資料段裡恰好出現的 `0f 05` 位元組，都會被誤判。上面每一條邊界都綁在處理程序上，這是本決定最吃重的事實。

## 為什麼合併會造成退化

1. **後盾綁在處理程序。** 單一位址空間意味著單一處理程序，於是一個 `seccomp` 判決或 `Seatbelt`／`LPAC` 的拒絕會一次殺掉所有執行個體；某個來賓漏改寫的系統呼叫，會從單一錯誤變成全體中斷。
2. **來賓可以寫入 stub 的每個位元組。** 數個執行個體共用位址空間時，一個來賓就能改寫另一個執行個體的通道頁、跳板、訊號框架、重寫快取、`vfs` Namespace 或對外政策；現有原語擋不住這件事，因為規則三明講來賓可寫的範圍只排除 hop 執行緒以暫存器持有的狀態。
3. **共享通道以槽位索引，不以執行個體索引。** 它是版面固定的共用記憶體；合併需要全新的逐執行個體鍵值設定，而訊息種類空間本身已有碰撞，樸素的聯集會把回覆送錯地方，進而產生難以重現的錯誤。
4. **逐執行個體的狀態會被汙染。** 重寫快取、`vfs` Namespace、對外允許清單與網路登錄都是逐執行個體的；未分割就共享，等於容許快取汙染與跨執行個體的檔案、網路存取，也讓 `Options` 的 `allow_domains` 之類的限制失去明確的作用範圍。
5. **那是全新的隔離設計，不是移植。** 合併設計必須以行程內原子操作取代 futex／`os_sync`／Windows 事件、拿掉 exec 與 stub 視窗設定、放棄 hop 執行緒。在提出 MPK／PKU、受驗證的行程內監控或硬體輔助區隔之前，不該動手改程式碼；沒有替代原語就合併，等於把隔離交給運氣。

## 實際交付的是什麼

Rust 監督器可以在同一個處理程序裡規劃多個執行個體：共享控制平面、單一 `qqboxd` 常駐程式、單一 OCI／Compose 計畫、共享映像檔快取與重寫快取；但每個執行個體仍保有自己的 stub 處理程序與位址空間。這才是操作體驗上的改進，且不必拿隔離交換。Zig stub 的 ABI 保持位元組一致，控制平面可為不同執行個體推導各自的視窗，而不動到後盾依賴的兩組頁表；`vfs` Namespace 與資料平面依舊由監督器持有。未來的單一位址空間模式必須閘在一個具名的隔離原語，以及每個宿主各自的對抗式驗證之後，預設不啟用。

## 證據與邊界

上述推導來自 [../AGENTS.zh-Hant.md](../AGENTS.zh-Hant.md) 的規則一與規則三、宿主端後盾的行為差異（`RET_TRAP`／`SIGSYS`、`Seatbelt`、instrumentation callback 與 `LPAC`）、共享通道的版面，以及英文版 [SINGLE_ADDRESS_SPACE.md](../../docs/SINGLE_ADDRESS_SPACE.md) 的逐項分析；逐項的 OCI 對照另見 [OCI_MATRIX.zh-Hant.md](OCI_MATRIX.zh-Hant.md)。

請特別注意證據的層次：Linux memfd／`seccomp`／hop 已移植到 Rust 並有即時見證，macOS Mach／`Seatbelt`／HVF 與 Windows `LPAC` 兩組尚未移植；Go 原始碼已移除，儲存庫是 Rust 與 Zig。`cargo build --workspace` 建置控制平面與 Linux 後端，`make witness` 見證控制平面的解析、接受／拒絕與規劃，`make backend-witness` 與 `make backend-exec-elf` 才是 Linux 後端的即時見證。因此這裡列的是設計邊界、既有實作與已見證範圍，不是完整來賓執行效能的量測。任何「已合併且無退化」的宣稱，都必須附上對抗式驗證的記錄與證據，而不是只留下口頭結論。也不得以 `sleep`、timeout 或週期性重查當作正確性的修正；隔離的推導與同步的修正，都應該能被重現。

## 說明文件連結

- [../README.zh-Hant.md](../README.zh-Hant.md)、[../SECURITY.zh-Hant.md](../SECURITY.zh-Hant.md)、[../llms.zh-Hant.txt](../llms.zh-Hant.txt)
- [PORT_STATUS.zh-Hant.md](PORT_STATUS.zh-Hant.md)、[OCI_MATRIX.zh-Hant.md](OCI_MATRIX.zh-Hant.md)
- [../examples/README.zh-Hant.md](../examples/README.zh-Hant.md)
- 英文版：[SINGLE_ADDRESS_SPACE.md](../../docs/SINGLE_ADDRESS_SPACE.md)、[README.md](../../README.md)
