# 原生來賓執行後端（Rust 移植狀態）

**狀態：Linux 的 trap-to-supervisor 路徑已移植，並在真實 Linux 6.17.7 aarch64
核心上即時見證。完整的來賓執行環境**尚未**移植。`go-removal` 仍為待辦。

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
| `filter.rs` | seccomp-BPF 產生器：`text_range_prefix(lo,hi)`（六指令的 `[__ehdr_start, etext)` TRAP 前置）；`allow_list(arch, nrs)`；`trap_one(nr)`；真實 `prctl(PR_SET_NO_NEW_PRIVS)` + `seccomp(SET_MODE_FILTER, flags=0)` 安裝。 |
| `trap.rs` | 真實 `sigaction(SIGSYS, SA_SIGINFO)` 處理器；讀取 `siginfo_t`（`si_code == SYS_SECCOMP`、`si_errno == 0x5ca1`），從 `ucontext_t` 取出 aarch64 `x0` 到 `x5`／`x8`／`pc`，發佈請求、以 futex 阻塞，並把監督器結果寫回 `x0` 讓來賓繼續。 |
| `hostop.rs` | 建於 vfs Namespace 之上的 Rust host-op 分派器：行程識別、brk、clock、stdio read/write、經 `Namespace::resolve` 的 openat、close、fd 表；未實作的 syscall 回傳 `-ENOSYS`，不假裝成功。 |
| `linux.rs` | 實作 `Platform` 的 `LinuxBackend`；`witness()` fork 來賓執行緒、安裝過濾器與處理器、執行真實 `getppid`、透過 chanPage 服務並回復。 |
| `macos.rs` | 透過 mach2 的真實 Mach 探測（`mach_task_self`、`vm_page_size`）；來賓啟動誠實回報 `UnsupportedPlatform`。 |
| `windows.rs` | 記錄契約並交叉編譯；本機沒有 Windows 宿主。 |

Linux 上的 `crates/qqbox/src/sandbox/mod.rs:install()` 現在呼叫
`backend::filter::install()`，不再回傳 `UnsupportedPlatform`。

CLI：`qqbox backend capabilities | witness | install | dispatch <nr> <args…>`。

## 即時見證（本回合）

見證執行於真實 Linux VM（`podman machine ssh`，Linux 6.17.7 aarch64，
`seccomp/actions_avail` 含 `trap`），使用透過 virtiofs 取得的靜態連結
`aarch64-unknown-linux-musl` 版 `qqbox`。

真實 `qqbox backend witness` 輸出：

```json
{
  "host": "linux", "arch": "aarch64",
  "channel_fd": 3, "channel_bytes": 4096,
  "filter_instructions": 4,
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

同一核心上的 `qqbox backend dispatch` 見證：
`getpid(172) -> 52472`、`brk(214) -> 0`、`clock_gettime(113) -> 0`、
未知 `999 -> -38`（ENOSYS，不假裝成功）。

Linux 上的 `qqbox backend capabilities` 回報 `memfd_channel=true`、
`seccomp_trap=true`、`sigsys_handler=true`、`implemented_syscalls=16`。

交叉目標：`aarch64-apple-darwin` 與 `aarch64-unknown-linux-musl` 的
`cargo check` 及 `cargo clippy -- -D warnings` 皆通過。
`x86_64-unknown-linux-gnu` 與 `x86_64-pc-windows-msvc` 無法檢查：`ring` 的
C 組建需要本 macOS 宿主沒有的跨平台 C 編譯器／sysroot。這是工具鏈缺口，
不是後端程式碼錯誤，也不宣稱已驗證。

## 尚未移植（誠實邊界）

- ELF 區段映射、`lc_syscall_entry` trampoline island 與符號解析、
  `StubWindow` 採用、`REP_START`、79 MiB memfd 幾何、`PROT_R` hop alias／
  hop 執行緒／`HOP_FORK`。
- 約 205 個真實 syscall 處理器（fs、net、procfs、signal、SysV IPC、ptrace）。
  Rust 目前實作 16 個，其餘回傳 `-ENOSYS`。
- 訊號框架、`tgkill` KICK、Landlock、`process_vm_readv/writev`、`SCM_RIGHTS`
  fd 傳遞、`openat2` 咽喉點、ptrace 暫存器模擬。
- macOS：具名 chanPage、`os_sync` 交握、編譯後 Seatbelt profile、MAP_JIT、
  Mach broker／fdlink／futex、ad-hoc codesign、`legacyX18` 修補、
  `posix_spawn` special port，以及需要 `com.apple.security.hypervisor`
  entitlement 的 HVF VM。
- Windows：LPAC AppContainer 建立、VEH + instrumentation callback、
  `RtlCloneUserProcess`、`NtCreateSection`、event pair 交握。
- 因此 `qqbox::run` 在備妥控制平面後仍回傳 `Error::UnsupportedPlatform`。
  從原始碼建置的 Rust 版本尚無法執行未修改的來賓 ELF；VNC 5905 上的即時桌面
  仍由存放在儲存庫之外的移除前 Go 執行檔啟動。

`go-removal` 維持待辦，直到三個後端都在各自宿主上通過歷史回歸測試為止。
