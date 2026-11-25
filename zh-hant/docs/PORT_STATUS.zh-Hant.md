# Rust 移植邊界（繁體中文）

> 這一頁只做一件事：說清楚哪些部分已經在 Rust 裡，哪些還沒有。編譯成功的控制平面不等於完整的執行時期；Linux 原生來賓執行後端已移植並有即時見證，macOS／Windows 後端尚未移植，Go 原始碼已移除。一般 `qqbox run` 仍回傳 `UnsupportedPlatform`，但 `qqbox backend exec-elf` 已能從原始碼建置執行未修改的靜態 aarch64 ELF。這句話必須明講，不能用「建置通過」或「見證通過」帶過。

## 已經在 Rust 裡的部分

- 工作區：`crates/qqbox`（核心）、`crates/qqbox-cli`（CLI `qqbox`）、`crates/qqboxd`（與 Docker 相容的常駐程式 `qqboxd`）、`crates/qqbox-mcp`（MCP 伺服器）、`crates/qqbox-runtime`（OCI runtime shim），共五個 crate。`cargo build --workspace` 是建置入口。
- 型別化設定：`Options`／`Config`、映像檔設定、runtime-spec、Compose、標籤、連接埠、磁碟區、網路模式、capabilities、資源限制、`seccomp`／`AppArmor`、健康檢查與重新啟動政策。無法實作的安全欄位不是靜默丟棄，而是附診斷訊息明確拒絕。
- OCI 表面：映像檔設定、連接埠與連接埠轉送、`/etc/hosts`／`/etc/resolv.conf`／`/etc/hostname` 的產生、磁碟區、標籤、健康檢查、重新啟動政策、Compose（JSON 與 YAML，含 `depends_on` 條件）、多架構 index、capabilities、`seccomp`／`AppArmor`、資源限制。逐項對照見 [OCI_MATRIX.zh-Hant.md](OCI_MATRIX.zh-Hant.md)。
- 網路政策：`Options::net`／Rust CLI `--net` 之下的 `:53` DNS 攔截與宿主解析、TCP `connect()` 代理；`allow_domains`／`allow_cidrs`／`allow_ports` 只收緊；封閉預設下沒有 DNS 與宿主可達性，瀏覽器顯示通用離線頁面。單破折號旗標僅屬移除前的 Go 執行期。
- `vfs` Namespace 作為唯一路徑咽喉，含掛載遮蔽與合成 `/proc`／`/sys`／`/dev`；監督器擁有檔案與網路的資料平面，來賓 Zig stub 只負責轉送。
- 常駐程式：54 條路由、真實 JSON、零信任 bearer 權杖，每一條路由都要求 `Authorization: Bearer <secret>`，沒有未經驗證的預設值。
- 見證：`make witness` 跑過版本、安全不變量、ABI、連接埠、映像檔設定、Compose、runtime-spec、網路計畫與路由清單，並把結果印出。Zig stub 的共享通道版面保持兩端位元組一致；`cargo run -p qqbox-cli -- invariants` 可單獨核對安全不變量。

## 已移植：Linux 原生來賓執行後端

Linux 的 memfd／`seccomp`／hop 路徑已是真實 Rust：`memfd_create` chanPage、
seccomp-BPF `RET_TRAP` 過濾器、`SIGSYS` 暫存器保存／回復、Rust host-op
派發器，以及 `qqbox backend exec-elf` 對未修改靜態 aarch64 ET_EXEC 的映射與
執行（核心單獨執行結束碼 1，監督器下為 42，`trap_hits=2`）。詳見
[NATIVE_BACKEND.zh-Hant.md](NATIVE_BACKEND.zh-Hant.md)。

## 尚未移植：其餘兩個後端

| 宿主 | 未移植的後端 | 它負責的工作 |
| --- | --- | --- |
| macOS | Mach／`Seatbelt`／HVF | 以 Mach task、`Seatbelt` 與 Hypervisor.framework 承載來賓 stub |
| Windows | `LPAC` | 以 AppContainer 與 instrumentation callback 承載來賓 stub |

此外 PIE／ET_DYN、trampoline island、約 205 個系統呼叫處理器、`PROT_R` hop
alias、訊號框架、Landlock、`SCM_RIGHTS` fd 傳遞與 ptrace 暫存器模擬也尚未
移植；Rust 目前服務 14 個具真實效果的系統呼叫，其餘回傳 `-ENOSYS`。`qqbox` 的一般執行
路徑仍回傳 `UnsupportedPlatform`，執行規劃的輸出不代表隔離已經生效；
`qqbox backend exec-elf` 則是已見證的真實執行，但僅限靜態 aarch64 ET_EXEC
與小型受陷系統呼叫集合。

## 已移除的部分

Go 原始碼已經移除，儲存庫僅含 Rust 與 Zig。這不是「Go 仍然是可用的後備執行時期」；沒有後備。一般 `qqbox run` 仍回傳 `UnsupportedPlatform`，但 `qqbox backend exec-elf` 已能從原始碼建置執行未修改的靜態 aarch64 ELF。任何說明文件、註解、提交訊息或回覆都必須照實陳述這個狀態；若有人宣稱已補上，請要求對抗式驗證的記錄與證據，以及三個宿主各自的見證。名稱由 laboon 更名為 qqbox 不改變這項事實，程式識別字與旗標維持拉丁原文也不等於後端已經移植完成。

## 誠實的驗證範圍

- 控制平面可以證明：設定被正確解析、不支援的安全欄位被拒絕、連接埠轉送計畫與 `/etc` 檔案被產生、Compose 依照 `depends_on` 條件排序、54 條路由都需要權杖。
- 控制平面不能證明：宿主檔案讀取被阻擋、RWX 記憶體對應被拒、記憶體上限在來賓內生效、或對外連線在真實來賓中被限制。這些都需要完整執行時期；`qqbox backend exec-elf` 目前只見證靜態 aarch64 ELF 的小型系統呼叫集合。
- 常見誤解：`daemon-probe` 之類的診斷是行程內的檢查，用來印出 HTTP 狀態，不是網路授權的端對端測試；`make witness` 是控制平面見證，`make backend-witness` 與 `make backend-exec-elf` 才是 Linux 後端見證。
- 修正原則：不得以 `sleep`、timeout 或週期性重查當作正確性的修正；同步問題請修正同步本身。程式庫吃選項不吃環境，這條在移植過程中沒有讓步。

## 剩餘限制（如實揭露，不宣稱已完成）

- `go-removal`、`migration-guest-memory`、`backend-macos-mach`、
  `backend-windows-lpac`、`backend-linux-seccomp` 這五列 PRD **維持待辦**。
  Linux 後端已能讓真實未修改的靜態 aarch64 ET_EXEC 在 Rust 監督器下執行；
  PIE／ET_DYN、trampoline island、`PROT_R` hop、訊號框架與完整系統呼叫表
  尚未移植。macOS Mach／Seatbelt／HVF 與 Windows LPAC 啟動契約在 Rust 中並不存在。
- 來賓實體記憶體、vCPU 暫存器與髒頁點陣圖**不可擷取**：qqbox 沒有 VMM，也沒有
  來賓 RAM API。`qqbox migrate` 只傳送監督器持有的控制平面狀態。
- 在本 macOS 宿主上 `cargo check --target x86_64-pc-windows-msvc`、
  `x86_64-unknown-linux-gnu`、`x86_64-unknown-linux-musl` 皆回傳 rc=101：`ring`
  的 C 組建需要未安裝的跨平台 C 編譯器與 sysroot。`aarch64-apple-darwin` 與
  `aarch64-unknown-linux-musl` 通過 check 與 clippy -D warnings。
- Compose 與 OCI runtime spec 仍使用 `serde_yaml 0.9.34+deprecated`。它可在離線
  環境正確建置與解析；本機 registry 快取沒有已維護的替代品，更換需要網路抓取與
  新一輪即時見證。
- VNC 5905 上的即時 Xubuntu 26 桌面仍由存放在版本庫之外的移除前 Go 執行期二進位檔
  啟動；Rust 的 `qqbox run` 不是桌面啟動器。Chromium 保留
  `--no-sandbox --single-process --no-zygote`，因為 42 種即時沙箱變體全數失敗；
  啟動器改以 `setpriv` 降至 uid 911，且刻意不隱藏不支援旗標的橫幅。
- 即時 Xubuntu 26 桌面的原生 XFCE 終端機，在每次啟動互動式殼層時仍會印出
  `bash: initialize job control: no job control in background: Bad file descriptor`。
  離庫保留的移除前 Go 執行時期，其 PTY 模擬未實作 `TIOCSPGRP`，
  `tcsetpgrp` 因此回傳 `EBADF`。工作控制在讀取任何 rcfile 之前即初始化：
  `SHELLOPTS=`、rcfile 內的 `set +m` 與 `setsid --ctty` 在真實來賓 PTY
  逐一實測，`setsid --ctty` 另在即時 VNC 5905 終端機打字實測，都無法消除
  該列。殼層本身完整可用。
  先前宣稱此錯誤已修復的敘述在此撤回。
- 網路限制照實陳述：ICMP echo 只走宿主未特權的 `SOCK_DGRAM`／`IPPROTO_ICMP` 使用者空間路徑；`SOCK_RAW`、`AF_PACKET`、`TUN`、來賓核心內 ICMP、802.11、`nl80211`、`wpa_supplicant`、`NetworkManager` 都不在契約內。
- 桌布 A／B 不是受控見證。較早對抗式掃描留下的兩份離庫腳本結論相反：
  `wall-26b/out/t.sh` 在 `xwallpaper --zoom` 之後由 1 種顏色變成 17,855 種，
  而 `wf26/refute-xubuntu26-wallpaper/guests/r26-xwallpaper/r26-xwallpaper.sh`
  在同樣的 `xwallpaper_rc=0` 與 `_XROOTPMAP_ID` 之下，前後 PNG 逐位元組相同、
  都只有 101 種顏色。兩份腳本至少在八個變數上不同（執行期才安裝套件、
  `xfdesktop --version`、`xfconf-query` 傾印、6 秒與 8 秒延遲、XML 排版、
  bwrap 對照、映像出處），因此兩者都無法證明或推翻桌布修正；對兩份腳本
  執行 `diff -q` 回傳非零。已交付的 `examples/qqbox/start-xubuntu.sh` 走的是
  另一條事件驅動路徑，以即時 RFB 畫面見證，而非這兩份腳本。未解項目 U1
  維持 OPEN，需要只改變單一變數的受控 A／B。此處如實揭露，不宣稱已解。
- 沒有見證過真正的 `docker run --runtime=qqbox`、containerd 或 Podman 整合；
  OCI runtime shim 只以真實 bundle 演練。
- 常駐程式與 MCP 的 HTTP 傳輸使用 `crates/qqbox/src/net/http.rs`，這是一個擁有
  每個已接受通訊端的小型 HTTP/1.1 伺服器。`tiny_http` 0.12 會在
  `EqualReader::drop` 排空未讀完的請求本體，因此宣告大型 `Content-Length`
  後停滯的對等端會占用工作者直到連線關閉。Rust 傳輸在讀取本體之前就拒絕超過
  `MAX_BODY_BYTES` 的宣告長度，限制標頭與 chunked 本體，並以丟棄通訊端直接關閉
  （不排空）。每個通訊端都有讀取期限；那是對不受信任網路輸入的上界，不是正確性
  修正或週期性重查，並行上限飽和時回傳具型別的 503。`make transport-witness`
  會啟動真實 `qqboxd`，讓 64 個停滯的超大請求對等端同時存在，並見證一般請求仍
  被服務。常駐程式不再支援 keep-alive：每個回應都帶 `Connection: close`，用戶端
  每次請求重新連線。Moby／Docker、containerd 與 Podman 整合仍未見證，如上所述。
- Linux host-op 監督器具備明確的停止旗標與已 join 的執行緒。強制
  `seccomp(SET_MODE_FILTER)` 失敗時，會停止服務執行緒、以哨兵喚醒 futex、
  join 並重設安裝宣告，讓重試得以進行；真實 Linux 見證為
  `BA3_FORCED_FAILURE_JOIN_PASS=true`（執行緒數前後皆為 1）。一般 `qqbox run`
  路徑仍為 `Error::UnsupportedPlatform`；這是資源生命週期的修正，不是三組原生
  來賓後端已移植的宣告。
- `qqbox::net::dns::parse_response` 會解析所有宣告的回答，不再於
  `MAX_ANSWERS` 截斷：64 個 CNAME 後的 A 記錄現在會回傳位址；上限只套用至
  儲存的位址向量。MCP stdio 傳輸只計算 JSON 本體，不含框架換行，因此恰好
  `MAX_BODY_BYTES` 的框架會被接受，多一個位元組則回傳具型別的錯誤。
- 已安裝的 Zig 是 0.16.0，而 43 份 Zig 原始碼是針對 0.13.0 撰寫。四份 stub
  建置腳本現在遇到非 0.13.x 工具鏈都會失敗關閉；`stub/build.sh` 在
  `zig objcopy --strip-debug` 失敗時也失敗關閉，不再悄悄交付未去除除錯資訊的
  二進位。`make stub-verify` 在乾淨簽出、沒有 Zig 工具鏈的情況下驗證八份已提交
  二進位的 ELF／PE／Mach-O 標頭。`make stub-rebuild-verify` 會在真實 Linux aarch64
  核心以釘版 Zig 0.13.0 重建八份二進位並比對與安全相關的位元組，見證結果為
  `STUB_REBUILD_BLOBS=8 failures=0`。兩份 amd64 Linux ELF 與兩份 macOS Mach-O
  逐位元組一致；兩份 Windows PE 在歸零 4 位元組 COFF 時間戳記後一致；兩份 arm64
  Linux ELF 只在 `.symtab` 差 3 個位元組，位於所有 PT_LOAD 範圍之外，可載入區段
  完全相同。同一虛擬機內兩次獨立重建逐位元組一致，因此剩餘差異是宿主後設資料，
  而非不確定性。CI 的 `zig-stubs` 工作會下載 Zig 0.13.0 並執行同一道閘門。
  在 macOS 上 Zig 0.13.0 的 `objcopy` 無法連結（`undefined symbol: _sigaction`），
  不支援在該宿主重建 Linux ELF stub。移植全部原始碼是另一項需要獨立把關的變更。

## 第二次對抗式複驗（`wf_30fbb515-ec8`）

四條唯讀驗證通道複驗 `be48688`，確認四項缺陷；皆已在提交 `e5ed464` 修正（受檢版本的說明在 `938bb1a` 完全相同），並在同一回合見證乾淨檢出閘門。隨後五條通道複驗 `e5ed464`（`wf_21566622-03f`）又找出三處殘缺，也在此修正。

- `docs/SINGLE_ADDRESS_SPACE.md` 與繁中鏡像仍寫「靜態重寫是最佳化，trap 是後盾」，與 `llms.txt` 矛盾。Rust 過濾器以系統呼叫編號給出 TRAP／KILL_PROCESS／ALLOW 三種判決，沒有文字範圍後盾，因此靜態重寫是正確性的必要條件。`AGENTS.md` 規則一現在逐一列出三種判決與 `Error::Killed`。第二輪掃描指出修正未擴及 `crates/qqbox/src/backend/filter.rs` 模組說明、`stub/zig/seccomp.zig`、`stub/zig/aarch64.zig`、`stub/zig/x86_64.zig`、`zh-hant/README.zh-Hant.md`、`zh-hant/docs/PERMISSIONS.zh-Hant.md` 與 `zh-hant/docs/PERF.zh-Hant.md`；這些位置現在都陳述同一套三判決契約，Go 時代的 Zig 註解亦標明為歷史出處。
- `examples/README.md` 的出處標記被塞進行內建置指令，讓移除前 Go 執行期的 `-net` 建置列無法執行。真正的指令已還原為圍欄殼層區塊，並通過 `sh -n`（rc=0、stderr 為空）。
- 二十個行內程式碼 span 被塞入出處標記，憑空造出不存在的旗標名稱。兩種語言的 Go 執行期 `-net` 與 `-allow-*` span 現在都只放真正的旗標，標記放在同一列的散文中；第二輪掃描獨立確認此類 span 為零。
- 從其他工作目錄執行 `make flag-provenance-verify` 會以 `FILES=0 occurrences=0` 通過。閘門現在把掃描根目錄錨定在第一份 makefile 所在目錄、要求九份哨兵文稿與 `Cargo.toml` 工作區標記並檢查內容、要求覆蓋率下限、以反引號奇偶追蹤拒絕行內旗標 span 內的標記，並執行四項非空洞自我檢查。`override define` 阻擋命令列與環境變數替換驗證程式；`$(firstword $(MAKEFILE_LIST))` 讓後置 `-f` 無法移動根目錄；哨兵內容檢查則拒絕由空白檔案捏造的根目錄。即時見證：儲存庫根目錄、外部工作目錄、指定 `make FLAG_PROVENANCE_PY`、`make -e` 與後置 `-f` 都執行**真正的**驗證程式（PASS、39 檔、55 處），不再略過掃描；空白根目錄與捏造的九檔根目錄皆 FAIL rc=1。


## 接下來要做的事
Rust 的平臺介面（`backend::Platform`）與 Linux memfd／chanPage／`seccomp`／`SIGSYS` 啟動契約都已完成並見證。接著要收斂 macOS Mach／`Seatbelt`／HVF 與 Windows `LPAC` 的啟動契約，維持已提交的 Zig stub 位元組不變，補上 PIE／ET_DYN 與約 205 個系統呼叫處理器；每一個宿主都要通過既有的回歸語料與對抗式驗證之後，才談得上完整的「可執行來賓」。在此之前，請把本頁當作邊界，而不是待辦清單的樂觀版本。桌面範例的畫面、終端機的 `TIOCPKT` 指標形式轉送與 `SwiftShader` 軟體渲染狀態，都是先前執行時期的見證，不應被解讀為目前工作區已能完整執行來賓。

## 說明文件連結

- [../README.zh-Hant.md](../README.zh-Hant.md)、[../AGENTS.zh-Hant.md](../AGENTS.zh-Hant.md)、[../SECURITY.zh-Hant.md](../SECURITY.zh-Hant.md)
- [OCI_MATRIX.zh-Hant.md](OCI_MATRIX.zh-Hant.md)、[SINGLE_ADDRESS_SPACE.zh-Hant.md](SINGLE_ADDRESS_SPACE.zh-Hant.md)、[NATIVE_BACKEND.zh-Hant.md](NATIVE_BACKEND.zh-Hant.md)、[LIVE_MIGRATION.zh-Hant.md](LIVE_MIGRATION.zh-Hant.md)
- [../examples/README.zh-Hant.md](../examples/README.zh-Hant.md)
- 英文版：[PORT_STATUS.md](../../docs/PORT_STATUS.md)、[README.md](../../README.md)
