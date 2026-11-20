# qqbox 即時遷移

**狀態：監督器所擁有的控制平面狀態已實作並通過即時驗證。
來賓記憶體／CPU 的即時遷移明確「未實作」，且在本程式樹中不可能實作 —
請見下方「邊界」。**

qqbox 不是虛擬機。它在宿主行程中執行未修改的 Linux ELF，只改寫 syscall
指令；監督器擁有資料平面，vfs Namespace 是唯一的檔案系統。Firecracker 則是
VMM：它擁有來賓實體記憶體、vCPU 暫存器、髒頁點陣圖與 `pause`/`resume` 開關。
這個差異決定了「即時遷移」在此處能代表什麼。

## 1. Loophole Labs 實際做了什麼（已查證）

研究對象：[loopholelabs/firecracker @ `main-live-migration-pvm`](https://github.com/loopholelabs/firecracker/tree/main-live-migration-pvm)，
head `7f6c070fa09c32eebbbfaeac6c61a96b05cf91ee`（2025-07-31），相對上游
`main` 領先 44 個提交、落後 1749 個。唯一實質的實作提交是
`cfed8de59a74`（2024-08-31）「feat: Add live migration and PVM support」—
16 個檔案、新增 370 行／刪除 82 行。三點 diff 共 18 個檔案，**沒有新增 Rust
原始檔**；HTTP 路由表、裝置序列化、vsock、userfaultfd 後端與
pause/resume 全部未改動。

**該分支中沒有遷移協定。**它加入的是*底層原語*：

| 原語 | 實作 |
| --- | --- |
| 共享來賓 RAM | `vstate/memory.rs`：`snapshot_file(shared: bool)` 改用 `MAP_NORESERVE\|MAP_SHARED` 而非 `MAP_PRIVATE`，讓核心在 VM 執行時把髒頁寫回檔案映射區 |
| 顯式清出 | `GuestMemoryExtension::msync()` → `libc::msync(MS_SYNC)`；`vm.rs` 的 `Vm::snapshot_memory_to_file` 支援新的 `SnapshotType::Msync`／`MsyncAndState` |
| 狀態／記憶體分離 | `persist.rs`：`Msync` **不**寫狀態檔，`MsyncAndState` 兩者皆寫；`snapshot/mod.rs` 在 header + bincode + CRC64 之前加上 `u64` 長度前綴 |
| PVM | 8 個 PVM MSR `0x4b56_4df0`–`df7` 加入 `SERIALIZABLE_MSR_RANGES`；`set_tsc_khz` 變為空操作；停用 CPU 型號檢查 |
| API | **沒有新端點**：`PUT /snapshot/create`（`snapshot_type: Full\|Diff\|Msync\|MsyncAndState`）、`PUT /snapshot/load`（新增 `shared` 欄位）、`PATCH /vm` `Paused\|Resumed` |

傳送本身交給**外部編排器**複製兩個純檔案；分支中沒有 socket、TCP、vsock 或
收斂迴圈。KVM 髒頁記錄（`KVM_MEM_LOG_DIRTY_PAGES`、`get_dirty_log`、
`dump_dirty`）是继承自上游，只有 `Diff` 使用 — `Msync` 完全繞過它。分支自身
已知缺陷：`msync()` 的回傳值被丟棄；新的長度前綴會破壞 `snapshot-editor`；
vsock／佇列還原的修正因造成 VM resume 失效而被還原（`f60a14959632`）；且落後
上游 1749 個提交。PVM 需要 tree 外的 `virt-pvm/linux` 宿主核心。

### 我們採用了什麼，沒有採用什麼

我們採用**架構紀律**，而非酬載：

- 具版本、完整性校驗、明確長度前綴的狀態檔（他們的 CRC64 + bincode →
  我們的 SHA-256 + 正規 JSON）；
- 持久規格狀態與即時執行狀態的乾淨分離（他們的 `Msync` 對
  `MsyncAndState` → 我們四個互不相交的 `MigrationState` 分割，以及明確的
  `Boundary::GuestExecution::NotCapturable`）；
- 由外部、capability 閘控的編排器負責，而非 VM 內的資料平面；
- `PUT /snapshot/create`／`PUT /snapshot/load` 的形狀 → 我們的
  `/qqbox/v1/migrate/checkpoint`／`restore`。

我們**沒有**抄襲 pre-copy 迴圈，因為該分支根本沒有。我們的迭代 `delta` 回合是
自行設計，作用於監督器擁有的控制平面值；它們絕不是來賓髒頁追蹤。該分支仰賴
核心對 `MAP_SHARED` 來賓 RAM 的寫回，而 qqbox 完全沒有來賓 RAM — 因此該原語
不適用。該分支的 `Msync` 路徑無法移植；若宣稱可以，即是範疇錯誤。

Loophole Labs 另一個 `drafter` 專案未在本研究中查證；此處不對它作任何宣稱。

## 2. 邊界（刻意先說明）

三個原生來賓後端 — Linux memfd/seccomp/hop、macOS Mach/Seatbelt/HVF、
Windows LPAC — 在本程式樹中**尚未移植到 Rust**（繼承的 `go-removal` PRD 列
仍為待辦）。由原始碼可證：`sandbox::run::run` 在所有宿主上都回傳
`Error::UnsupportedPlatform`，`sandbox::install` 亦然；Rust 端從未配置
memfd、Mach port、HVF VM 或共享 channel page。八個已提交的 Zig `.bin` stub
僅為 `include_bytes!` 資料。

因此**不存在**來賓實體記憶體、vCPU 暫存器、髒頁點陣圖、執行中的 syscall、
訊號框架、真實 OS fd 或 TCP 連線狀態，也沒有可暫停的 VMM。
`sandbox/mod.rs` 的 `GuestState` 是在 `prepare` 內建立後即丟棄的四欄結構。
若宣稱本程式樹能進行 Firecracker 式來賓記憶體遷移，即為捏造。
`MigrationState::boundary.guest_execution` 記錄
`GuestExecution::NotCapturable`，讓 ADT 本身無法被誤讀；
`migrate::features()` 也回報相同邊界與明確的 `unsupported` 清單。

**實際被遷移的**，是 Rust 監督器真正擁有的狀態：

| 分割 | 內容 | 來源 |
| --- | --- | --- |
| `spec` | args、env、stdio、user、安全設定、資源限制、healthcheck、restart/log 政策、image、entrypoint、cmd、labels、hostname | `Config` |
| `fs` | root、upper、mounts、devices、volumes、已解析的 vfs Namespace 掛載表 | `Config` + `vfs::Namespace` |
| `chan` | pid、brk、映射位元組、fd 表、chanPage 幾何與 stub window base | `GuestState` + `zig::abi` 常數 |
| `net` | net 旗標、allow 清單、exposed/published ports、network mode、DNS、extra hosts、衍生解析綁定 | `Config` + `net::ports::expand` |

各分割**由建構保證互不相交**：`MigrationState::capture` 以不含 `..` 的窮盡
解構拆解 `Config`，把每個欄位移入恰好一個分割。為 `Config` 新增欄位會造成
編譯錯誤，直到它被指派為止 — 這是防止靜默遺失狀態的 Poka-Yoke 防呆
（`Γ_t ⊇ Γ_{t-1}`）。

## 3. 代數與不變式

`capture` 與 `apply` 是純態射：

- `capture(apply(s)) == normalize(s)` — 由 `qqbox migrate verify` 即時驗證，
  目的端在 ack 之前會再驗證一次。
- `normalize(normalize(s)) == normalize(s)` — `f ∘ f ≡ f`。排序／去重無序集合，
  並固定版本與邊界。
- `apply_delta(apply_delta(s, d), d) == apply_delta(s, d)` — delta 為指派，
  絕不累加。
- `digest(s) == digest(normalize(s))` — 對正規 JSON 取 SHA-256。
- `delta(s, s)` 為空。

`normalize` 具決定性且無副作用；`digest` 是純函式。沒有環境狀態、沒有讀取
環境變數、沒有全域可變計數器、沒有隱藏變更。

## 4. 線路協定

```
MAGIC "QQBM" | wire_version u8 | kind u8 | length u32 BE | payload | SHA-256
```

- `length` 在**配置記憶體之前**即以 `MAX_FRAME`（16 MiB）檢查。
- 摘要覆蓋 header + payload，並以常數時間比較。
- 全部使用 `read_exact`；每個失敗都是具型別的 `MigrationError::Wire`。
- 訊框種類：`Hello`、`State`、`Delta`、`Commit`、`Ack`、`Reject`。
- `read_expect(kind)` 在解碼前拒絕型別混淆的訊框。
- 所有 reader／writer 都是明確參數 — `Effects ≡ IO_Monad`。

## 5. 仿射型別狀態與零信任

`Session<Idle> → Prepared → Streaming → Committed → Restored`。每次轉換
**消耗 `self`**，且 `Session` 刻意不實作 `Clone` 或 `Copy`，因此階段無法重用，
遷移也無法被 commit 兩次。每個具權限的轉換都要求 `Capability` — 呼叫者提供
之密鑰的 SHA-256，以常數時間比對。密鑰絕不作為原始碼中的字面值，也絕不出現在
訊框中；訊框中只有其摘要。

daemon 端點另外由既有的 bearer token 保護：

| 路由 | 行為 |
| --- | --- |
| `GET /qqbox/v1/migrate/features` | 版本、階段、分割、定律、邊界、不支援清單 |
| `GET /qqbox/v1/migrate/state` | 最近一次 checkpoint 與摘要，若無則 404 |
| `POST /qqbox/v1/migrate/checkpoint` | `{"container":"<id>"}` → 從真實容器的 `Config` 擷取 |
| `POST /qqbox/v1/migrate/restore` | `{"capability":"<hex>","state":<MigrationState JSON>}` → 錯誤 capability 為 403，有效還原為 200 + 摘要 |

## 6. CLI

```bash
qqbox migrate features
qqbox migrate checkpoint --config config.json --out src.frame
qqbox migrate verify      --input src.frame
qqbox migrate restore     --input src.frame --out restored.json
qqbox migrate serve       --secret S --port-file p --ready-fifo f --out dest.frame
qqbox migrate send        --secret S --endpoint 127.0.0.1:P --input src.frame \
                          --delta delta1.json --final-out source-final.frame
```

`serve` 綁定臨時埠、發佈埠號，然後把 `READY` 寫入 FIFO。FIFO 寫入會阻塞至
讀取端開啟為止：就緒判定是事件驅動，未使用 sleep、timeout 或週期性重查作為
正確性機制（gm 鐵律）。

## 7. 即時驗證（與實作同一回合）

暫存驅動程式：`/Users/dylanwong/.gm-work/qqbox/migration-witness-driver.sh`
與 `/Users/dylanwong/.gm-work/qqbox/migration-daemon-witness-driver.sh`。

| 檢查 | 結果 |
| --- | --- |
| 四條代數定律 | `capture_apply_identity=true`、`normalize_idempotent=true`、`digest_canonical=true`、`delta_idempotent=true` |
| 損毀訊框 | 拒絕，`integrity digest mismatch` |
| 截斷訊框 | 拒絕，`digest read: failed to fill whole buffer` |
| 超長長度 | 拒絕，`frame 16777217 exceeds MAX_FRAME 16777216` |
| 兩個真實行程 + 真實 TCP socket | `send_rounds=1`，來源摘要 == 目的摘要 |
| pre-copy 後的狀態一致性 | `source_final_equals_dest=True`、`original_differs_after_delta=True` |
| TCP 上錯誤 capability | 接收端 `migration capability rejected`，傳送端 rc=1 |
| daemon 無 bearer token | 401 |
| checkpoint 前的 daemon 狀態 | 404 |
| daemon checkpoint → state | 200，摘要相符 |
| daemon restore 錯誤 capability | 403 |
| daemon restore 正確 capability | 200，摘要相符 |
| 公告的遷移路由 | 4 條全部存在 |

## 8. 尚待完成

- `go-removal`（繼承，刻意保持待辦）：把三個原生來賓後端移植到 Rust，是任何
  來賓記憶體遷移的前置條件。
- `migration-guest-memory`（本回合，刻意保持待辦）：來賓 RAM、vCPU 暫存器、
  髒頁追蹤、執行中的 syscall、真實 fd/TCP 狀態與來賓靜止。需要 VMM 或等價的
  來賓記憶體 API。
- 解壓後的 OCI rootfs 位元組是內容定址且可重新拉取；本實作遷移的是指向它們的
  規格，而非 layer tar 位元組。

## 9. 設計理由（正交性）

- **系統**：純狀態態射與 `IO_Monad` 邊界分離；四個互不相交分割上的分離邏輯。
- **範疇**：嚴格詞法邊界、capability 傳遞、無環境／全域／隱含狀態，每個
  reader／writer 皆參數化。
- **對齊**：全函式（遷移路徑中為 `Result`，無 `unwrap`／`panic`）、可構造的
  往返定律、對來賓狀態不作任何推測性宣稱。
