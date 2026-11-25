# 原生來賓執行後端（Rust 移植狀態）

**狀態：Linux 的 trap-to-supervisor 路徑已移植，並在真實 Linux 6.17.7 aarch64
核心上即時見證；未修改的靜態 aarch64 ELF 現在可從 Rust 建置直接執行。
完整的來賓執行環境**尚未**移植。`go-removal` 仍為待辦。

## 「後端」是什麼

qqbox 透過改寫 syscall 指令，讓未修改的 Linux ELF 直接在宿主 CPU 上執行；監督器
透過 128 位元組的 chanPage（`crates/qqbox/src/zig/abi.rs`）擁有資料平面。
已移除的 Go 引擎有三個各平台後端：

| 宿主 | Go 時期的啟動契約 |
|---|---|
| Linux | `memfd_create("lc-chan")`（fd 3）、lifeline pipe（fd 4）、`SOCK_DGRAM` fdpass socketpair（fd 5）、經 `/proc/self/fd/N` 執行 stub、seccomp 文字範圍 `RET_TRAP`、host-op hop 執行緒、約 205 個模擬 syscall。 |
| macOS | 具名 `MAP_SHARED` 暫存 chanPage（路徑放在 argv[1]）、`os_sync` 交握、Mach broker bootstrap port、編譯後的 Seatbelt profile、MAP_JIT W^X；低位址非 PIE 的 ET_EXEC 來賓使用 Hypervisor.framework VM（第二位址空間）。 |
| Windows | `CreateFileMapping` chanPage、每 slot 的 auto-reset event、LPAC AppContainer、VEH + x86-64 instrumentation callback、`RtlCloneUserProcess` fork。 |

Go 程式樹為 445 個檔案／85,620 行；`internal/sandbox` 單一目錄就有
322 個檔案／約 69,300 行。完整重現不是單一回合可完成的工作。

## 現在已用 Rust 實作的部分

`crates/qqbox/src/backend/`：

| 模組 | 內容 |
|---|---|
| `channel.rs` | 真實 `memfd_create` + `MAP_SHARED` chanPage；`ChannelRequest`／`ChannelResponse` 使用凍結的 ABI 位移；futex 字組就是 chanPage 位元組 8 的 `state`（ST_REQ／ST_REP）。 |
| `filter.rs` | seccomp-BPF 產生器：`trap_and_deny(trapped, deny)` 才是**已安裝**的過濾器（服務的呼叫 TRAP、二十項直接逃逸拒絕清單 KILL_PROCESS、其餘 ALLOW），另有 `validate()` 全域靜態驗證器。`text_range_prefix(lo,hi)`、`allow_list(arch, nrs)` 與 `trap_one(nr)` 是 Go 時代出處與驗證用元件，文字範圍前置**未安裝**。真實 `prctl(PR_SET_NO_NEW_PRIVS)` + `seccomp(SET_MODE_FILTER, flags=0)` 安裝。 |
| `trap.rs` | 真實 `sigaction(SIGSYS, SA_SIGINFO)` 處理器；讀取 `siginfo_t`（`si_code == SYS_SECCOMP`、`si_errno == 0x5ca1`），從 `ucontext_t` 取出 aarch64 `x0` 到 `x5`／`x8`／`pc`，發佈請求、以 futex 阻塞，並把監督器結果寫回 `x0` 讓來賓繼續。 |
| `hostop.rs` | 建於 vfs Namespace 之上的 Rust host-op 分派器：行程識別、brk、clock、stdio read/write、經 `Namespace::resolve` 的 openat、close、fd 表；未實作的 syscall 回傳 `-ENOSYS`，不假裝成功。 |
| `linux.rs` | 實作 `Platform` 的 `LinuxBackend`；`witness()` fork 來賓執行緒、安裝過濾器與處理器、執行真實 `getppid`、透過 chanPage 服務並回復。 |
| `macos.rs` | 透過 mach2 的真實 Mach 探測（`mach_task_self`、`vm_page_size`）；來賓啟動誠實回報 `UnsupportedPlatform`。 |
| `windows.rs` | 記錄契約；`x86_64-pc-windows-msvc` 交叉檢查被本 macOS 宿主上 `ring` 的 C 工具鏈／sysroot 缺口擋住，也沒有 Windows 宿主可做即時見證。 |

Linux 上的 `crates/qqbox/src/sandbox/mod.rs:install()` 現在呼叫
`backend::filter::install()`，不再回傳 `UnsupportedPlatform`。

CLI：`qqbox backend capabilities | witness | install | dispatch <nr> <args…> | memory | exec-elf <path>`。`install` 現在會同時安裝 chanPage、`SIGSYS` 處理器、服務中的 host-op 執行緒與過濾器；缺少監督器的 TRAP 過濾器會讓呼叫者懸置，因此絕不單獨安裝。

## 即時見證（本回合）

見證執行於真實 Linux VM（`podman machine ssh`，Linux 6.17.7 aarch64，
`seccomp/actions_avail` 含 `trap`），使用透過 virtiofs 取得的靜態連結
`aarch64-unknown-linux-musl` 版 `qqbox`。

真實 `qqbox backend witness` 輸出：

```json
{
  "host": "linux", "arch": "aarch64",
  "channel_fd": 3, "channel_bytes": 4096,
  "filter_instructions": 7,
  "trapped_syscall": 173,
  "supervisor_result": 1364279887,
  "guest_observed_result": 1364279887,
  "untrapped_control_result": 51848,
  "control_differs": true,
  "trap_hits": 1, "served": 1, "match_ok": true
}
```

**對照組**正是否證：同一個 `getppid` 在安裝過濾器**之前**執行，回傳核心的真實
父行程 pid 51848；安裝之後，同一指令回傳 Rust 監督器的哨兵值
`0x5151424f` = 1364279887，且 SIGSYS 命中恰好一次。這證明發生了真實的
seccomp trap、真實的 SIGSYS 暫存器保存／回復、真實的 chanPage 請求、真實的
Rust 監督器分派，以及真實的來賓繼續執行。

同一核心上的 `qqbox backend dispatch` 見證（pid 每次不同）：
`getpid(172) -> 91676`、`brk(214) -> 0`、`clock_gettime(113) -> -14`
（EFAULT：未提供來賓緩衝區）、未知 `999 -> -38`（ENOSYS）。
`qqbox backend memory` 以真實緩衝區證明有界的資料平面：
`clock_gettime_result=0`、`uname_result=0`（Linux 成功回傳值）、
`tv_sec=1795229114`、`sysname=Linux`、rc=0。寫入位元組由緩衝區內容證明，
而非回傳值；部分 `process_vm_writev` 現在回傳 `-EFAULT`。沒有任何系統呼叫
假裝成功。

Linux 上的 `qqbox backend capabilities` 回報 `memfd_channel=true`、
`seccomp_trap=true`、`sigsys_handler=true`、`implemented_syscalls=14`。
這個數字是 `hostop::IMPLEMENTED.len()`，由分派表推導，不是手寫常數。

交叉目標：`aarch64-apple-darwin` 與 `aarch64-unknown-linux-musl` 的
`cargo check` 及 `cargo clippy -- -D warnings` 皆通過。
`x86_64-unknown-linux-gnu` 與 `x86_64-pc-windows-msvc` 無法檢查：`ring` 的
C 組建需要本 macOS 宿主沒有的跨平台 C 編譯器／sysroot。這是工具鏈缺口，
不是後端程式碼錯誤，也不宣稱已驗證。

## 尚未移植（誠實邊界）

- PIE／ET_DYN、`lc_syscall_entry` trampoline island 與符號解析、
  `StubWindow` 採用、`REP_START`、79 MiB memfd 幾何、`PROT_R` hop alias／
  hop 執行緒／`HOP_FORK`。靜態 aarch64 ET_EXEC 的區段映射已由 `guest.rs`
  實作並見證。
- 約 205 個真實 syscall 處理器（fs、net、procfs、signal、SysV IPC、ptrace）。
  Rust 目前服務 14 個具真實效果的系統呼叫，其餘回傳 `-ENOSYS`。
- 訊號框架、`tgkill` KICK、Landlock、`process_vm_readv/writev`、`SCM_RIGHTS`
  fd 傳遞、`openat2` 咽喉點、ptrace 暫存器模擬。
- macOS：具名 chanPage、`os_sync` 交握、編譯後 Seatbelt profile、MAP_JIT、
  Mach broker／fdlink／futex、ad-hoc codesign、`legacyX18` 修補、
  `posix_spawn` special port，以及需要 `com.apple.security.hypervisor`
  entitlement 的 HVF VM。
- Windows：LPAC AppContainer 建立、VEH + instrumentation callback、
  `RtlCloneUserProcess`、`NtCreateSection`、event pair 交握。
- 因此 `qqbox::run` 在**一般**情況仍回傳 `Error::UnsupportedPlatform`；
  只有 `qqbox backend exec-elf` 會執行真實未修改的 ELF，且僅限靜態 aarch64
  ET_EXEC 與小型受陷系統呼叫集合。VNC 5905 上的即時桌面仍由存放在儲存庫之外
  的移除前 Go 執行檔啟動。

`go-removal` 維持待辦，直到三個後端都在各自宿主上通過歷史回歸測試為止。
