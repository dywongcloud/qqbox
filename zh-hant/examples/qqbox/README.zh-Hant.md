# `examples/qqbox`：完整配置 Xubuntu 26 桌面（繁體中文）

本目錄是 [`../xubuntu/README.zh-Hant.md`](../xubuntu/README.zh-Hant.md) 的後繼版本，
以 **Ubuntu 26.04.1 LTS「Resolute Raccoon」** 為基底，搭配 Xubuntu 26.04.1 的
設定、主題與桌布，在 qqbox 原生來賓執行環境中透過 VNC TCP 5905 提供桌面。
英文版完整說明見 [`README.md`](../../../examples/qqbox/README.md)。

基底固定為 `linuxserver/webtop:ubuntu-xfce-ce206d66-ls292` 的 `linux/arm64`
資訊清單 `sha256:4721b798561ebabc9bfee60d3220f8c5d57b17fb78ab83168c94628e07e55e99`。
實測 `/etc/os-release`：`PRETTY_NAME="Ubuntu 26.04.1 LTS"`、
`VERSION_CODENAME=resolute`，`xubuntu-wallpapers` 為 26.04.1。

**如實邊界**：這是 Ubuntu 26.04 加上 XFCE 與 Xubuntu 26 的桌布、主題和設定，
不是 Xubuntu 發行版本身（`/etc/os-release` 仍為 `ID=ubuntu`）。Xubuntu 官方
沒有 `linux/arm64` OCI 映像檔，26.04 的 ISO 也只提供 amd64 與實驗性 riscv64；
`linuxserver/webtop` 沒有 `xubuntu` 標籤。26.04 LTS（2026-04-23）與
26.10（2026-10）同時存在，使用者所說的「Xubuntu 26」並未指定點版本，
此處採用 LTS。

| 需求 | 狀態 |
| --- | --- |
| 桌布／背景 | `xwallpaper --zoom /usr/share/xfce4/backdrops/xubuntu-wallpaper.png` 畫出真正的 Xubuntu 26 桌布，實測 17,855 種顏色。 |
| 底部工作列 | XFCE panel `p=10;x=0;y=0` → `1280x41+0+759`：顯示桌面、四個啟動器、目錄選單。 |
| vi／vim | `vim 9.1` 與 `vim-tiny`（`vi`），兩者編輯檔案皆 rc=0。 |
| GNU 工具鏈 | 約 70 個套件，見英文版 README。 |
| 套件安裝 | `apt-get install` 可重複執行且 rc=0（`fastfetch`／`tree`／`htop` 皆為 rc=0）；`GDBus … Spawn.PermissionsInvalid` 已修復。26.04 已移除 `neofetch`，即使 `apt-get update` 成功，`apt-get install -y neofetch` 仍回傳 rc=100 `E: Unable to locate package neofetch`；改以 `fastfetch` 2.57.1 取代。`npm` 同樣無法由 apt 安裝，9.2.0 來自 Node 18 payload。 |
| Chromium | Debian Chromium 150.0.7871.100 `linux/arm64`，四個 bookworm SONAME 隨附。 |
| Node／npm／Claude Code | Ubuntu 26.04 的 Node 22 在 qqbox 下因 futex 而 rc=134；改用 noble Node 18.19.1、npm 9.2.0、Claude Code 1.0.128（純 JS），皆已實測。 |

## 桌布為何需要 `xwallpaper`

Ubuntu 26.04 的 gdk-pixbuf 影像載入器改用 glycin，必須透過 `bwrap` 沙箱；
在 qqbox 下 `bwrap` 明確失敗：`bwrap: Failed to make / slave: Bad address`。
因此 `xfdesktop` 無法解碼 PNG，只會畫出單色底色（實測僅 1 種顏色）。
Xubuntu 26.04 自帶的 `xfce4-desktop.xml` 完全沒有 backdrop 設定，而
XFCE 4.20 使用 `monitorscreen/workspaceN/last-image`，不是 24.04 的
`monitor0/image-path`。`xwallpaper` 直接使用 libpng，繞過 glycin，因此可行：
寫入 xfconf 並設 `image-show=false`；群組監督器自行持有一條永久性 session
bus（不使用 `xfce4-session`，它是單點故障），並啟動 `xfwm4 --compositor=off`、
`xfce4-panel`、`xfdesktop --disable-wm-check`，最後以 `xwallpaper --zoom`
畫上根視窗。
對照組 `00-before.png` 只有 1 種顏色，修復後 `01-after.png` 有 17,855 種。
24.04 只靠 `xfdesktop` 即可（9,049 種顏色）；該做法無法套用到 26.04，
這是對抗式驗證找出並如實記錄的結果。

## D-Bus setuid helper 錯誤

錯誤訊息原文：

```
Error: GDBus.Error:org.freedesktop.DBus.Error.Spawn.PermissionsInvalid:
The permission of the setuid helper is not correct
```

成因是 qqbox 的 `execve` 忽略 `S_ISUID`，導致 `dbus-daemon-launch-helper`
無法取得 root；dbus 1.14 的 `check_permissions()` 要求
`getuid()==messagebus && geteuid()==0`，helper 因此回傳 7，由 daemon 轉成
上述字串。此版本沒有 stat() 模式檢查，`chmod 4754` 無效，OCI 解包丟失
setuid 位元也不是成因。修復方式：以 root 身分啟動系統匯流排
（`/etc/dbus-1/qqbox-system.conf`），並移除兩個會啟動 `User=root` 服務的觸發點：

```sh
rm -f /etc/apt/apt.conf.d/20packagekit \
      /usr/share/dbus-1/system-services/org.freedesktop.PackageKit.service
```

**對抗式修正**：在舊的 24.04 映像檔上，真正讓 `apt-get install` 回傳 rc=100
的是 17 個「已解包但未設定」的 npm 相關套件（`node-neo-async` 的
`angelfall.js`／`AngelFall.js` 在宿主大小寫不分檔案系統上衝突），不是這行
錯誤；單純移除觸發點只會讓錯誤訊息消失，安裝仍失敗。此 26.04 映像檔
兩者皆已修復：不透過 apt 安裝 npm（改用 registry tarball），usrmerge 的
`ldconfig` divert 讓 dpkg 保持一致，root 匯流排與觸發點移除則讓訊息消失。
實測：`apt-get update` rc=0、重複安裝 `fastfetch` 與 `tree` rc=0、
`NO_DBUS_ERROR`。

## 其他兩個 26.04 專屬修正

- **usrmerge 的 `ldconfig`**：26.04 的 `/usr/sbin/ldconfig` 是靜態 PIE，
  在 qqbox 重寫器下會 segfault，使 `libc-bin` 的 postinst 以 139 結束。
  因為 `libc-bin` 擁有 `/usr/sbin/ldconfig`，直接覆寫會在解包時被還原，
  必須用 `dpkg-divert --local --rename --divert /usr/sbin/ldconfig.qqbox-real
  /usr/sbin/ldconfig` 再加 no-op 腳本。
- **Node**：26.04 apt 只有 Node 22，官方 nodejs.org tarball 也同樣因
  「The futex facility returned an unexpected error code」而 rc=134。
  因此改附 noble 的 Node 18 二進位檔、六個 SONAME、五個外部化 builtin，
  以及 npm registry 的 npm 9.2.0。

## 桌面外觀：還原原廠 Xubuntu 面板與 Chromium 權限

### 還原原廠雙面板配置

先前的組態把映像內整套面板設定換成單一自訂底部面板（`p=10`），只有應用程式
選單、三個啟動器與工作列，因此**頂部標題列消失**，底部 dock 也與原廠 Xubuntu
不同。映像其實提供兩份預設：

| 檔案 | 配置 |
| --- | --- |
| `/etc/xdg/xfce4/panel/default.xml`（通用 xfce4-panel） | 兩面板：頂部 `p=6` 標題列、底部 `p=10` dock |
| `/etc/xdg/xdg-xubuntu/xfce4/panel/default.xml`（xubuntu-default-settings） | 單一頂部 `p=6` 面板，含 Whisker 選單 |

`start-xubuntu.sh` 現在寫入雙面板組態，只使用映像中確實存在的 plugin：頂部
`p=6` 含 Whisker 選單、工作列、桌面切換器、系統匣、音量與時鐘；底部 `p=10`
dock 含顯示桌面、四個啟動器與目錄選單。plugin ID 互不相交（頂部 1-9、
dock 11-18）。保留四個原廠啟動器 `.desktop` 名稱，但 `Exec` 指向在 qqbox 下
可運作的執行檔（原廠 `exo-open` 會導向會 segfault 的 snap stub）。
`xfce4-whiskermenu-plugin` 已寫入 Dockerfile，舊映像則於開機時安裝；若無法
取得，XML 會退回 `applicationsmenu`。`actions` plugin 在沒有 session manager
時崩潰，已移除。

見證（live 5905）：獨立 RFB 3.8 幀 `qbox:99` 1280x800、7,593 種顏色，可見
頂部標題列的 Whisker 選單、工作列、桌面切換器、時鐘與音量，以及底部 dock 的
四個啟動器與目錄選單；`menu_plugin=whiskermenu whisker=yes`；沒有缺少 plugin 或崩潰訊息。

### Chromium sandbox：為何仍關閉，以及實際改了什麼

Chromium **自身的** sandbox 在 qqbox 下無法啟用。42 個即時變體（先前 27 個、
本次再測 15 個）因兩個獨立原因失敗：

| 嘗試 | 結果 |
| --- | --- |
| 以 root 啟用 sandbox | `rc=1`，`Running as root without --no-sandbox is not supported`（`zygote_host_impl_linux.cc:101`） |
| 以 uid 911（`abc`）啟用 sandbox | `rc=134/133`，`FATAL … zygote_host_impl_linux.cc:221 Check failed: . : No such file or directory (2)` — zygote 在送出 hello 訊息前即死亡 |
| `--disable-setuid-sandbox`、`--no-zygote`、`--single-process`、使用者命名空間 | 全部失敗；`unshare -U -r` 本身 rc=0 |
| `--no-sandbox` | 可運作，能呈現 `https://example.com/` |

zygote 需要真正的 setuid helper／PID namespace／seccomp-bpf 安裝；qqbox 的
`execve` 忽略 setuid、PID namespace 模擬為空操作，且安裝 seccomp filter 會
殺掉 stub。這是底層限制，不是旗標選擇。

**實際變更**：Chromium 不再以 **root** 執行。`chromium-wrapper.sh` 現在透過
`setpriv` 降級到映像內的非特權使用者 `abc`（uid 911／gid 1001），並給予專屬
的 HOME、使用者資料目錄、crash 目錄與 runtime 目錄。即使 renderer 遭到攻陷，
也不再具備 root 權限。宿主層的 qqbox backstop 仍是外層邊界。
`--no-sandbox`、`--single-process`、`--no-zygote` 保留：多行程的 headless
Chromium 在 qqbox 下會卡住（已見證）。wrapper 會把 `chromium_uid=911` 寫入
`/out/chromium-uid.txt`。Chromium 自身的 `--no-sandbox` 警告橫幅**刻意不隱藏**
— 隱藏安全警告並不誠實。

## 事件驅動監督：工作列不再於執行中消失

執行約 15 小時後，5905 桌面的工作列、導覽列與所有視窗同時消失，但桌布（根
像素圖）仍在。鑑識結果：

| 證據 | 值 |
| --- | --- |
| 修復前 RFB | 只剩桌布，1280x800、17,916 種顏色，`ServerInit qbox:99` 未變 |
| `x11vnc.log` | 只有 06:18:58 一次 `screen setup finished`，X server 未重啟 |
| `xfce.log` | Thunar 中止；重複 `Wnck:ERROR:../libwnck/xutils.c:1510:default_icon_at_size: assertion failed (base)` 與 `Bail out!` |
| `xfce.log` | `xfce4-terminal`、`xfsettingsd`、`xfwm4`、`xfce4-panel` 於 21:10:23 全部 `ICE I/O Error`／`Disconnected from session manager` |

`xfce4-session` 擁有 session bus，是單點故障：它一死，所有客戶端連線中斷、
所有視窗被撤銷映射；桌布根像素圖仍留在 X server。X server 重啟與 VNC 重新
連線已排除（`ServerInit` 名稱相同、沒有第二次 screen setup、`client_count`
正常）。原本也沒有任何重啟機制。

修復：**完全移除 `xfce4-session`**。自行持有永久性 session bus
（`dbus-daemon --session --fork`）與 `xfconfd`，並以事件驅動的 `wait $pid`
迴圈分別監督 `xfwm4`、`xfce4-panel`、`xfdesktop`、終端機與 Chromium。
`wait -n -p` 會在子行程結束時才返回：沒有 sleep、timeout 或週期性重查（符合 gm
鐵律）。熔斷器會在元件連續六次立即結束時停止重啟。Chromium 重啟前清除
singleton lock。此設計維持恰好一個面板與一個 xfconfd，不會重演先前的重複管理
員錯誤。

實測：刻意 `pkill -x xfce4-panel` rc=0 → `panel exited rc=0 → restarting`，
視窗回到 `1280x41+0+0 +0+759`。切換後的獨立 RFB 3.8 幀：`qbox:99`、
1280x800、4,096,000 raw bytes、7,484 種顏色、底部 50px 有 1,182 種顏色，
畫面可見 Applications 選單、三個啟動器、含 `Example Domain - Chromium` 與
`QQBOX-TERMINAL` 的工作列。全新開機日誌沒有 ICE 錯誤、Wnck 斷言或重複管理員
訊息。Chromium 加上 `--password-store=basic` 並隱藏 gnome-keyring 的
autostart 項目，移除了「Choose password for new keyring」對話框。

### 強化：session bus 也受監督，且 shim 能承受 dpkg

對抗式 swarm（9 個代理）找出兩個殘留缺口。第一，永久性 session bus 本身仍是
單點故障。六個獨立迴圈已改為**單一群組監督器**，同時持有 bus 與所有客戶端：
FIFO 讀取阻塞於 bus 位址，`wait -n -p` 阻塞於第一個子行程結束，而
`BUS_EXITED` 事件會 kill／wait 整個群組並啟動全新 bus。`xfconfd` 刻意不受監督
— 它可由 D-Bus 啟動，舊迴圈每 300 秒以 rc=0 結束一次。

第二，`dpkg -i bubblewrap_*.deb` 會覆寫 glycin shim。映像現在註冊
`dpkg-divert --local --divert /usr/bin/bwrap.distrib --rename --add
/usr/bin/bwrap`，開機指令稿則冪等地重新宣告。真正的 ELF 保存在
`bwrap.qqbox-orig`（於 `--rename` 前以 magic `7f454c46` 驗證），因此 fallback
絕不會遞迴執行 shim 本身。

全新來賓（VNC 5985）的實測證據，live 5905 完全未動：

| 探測 | 結果 |
| --- | --- |
| 乾淨開機 | `xfce4_session_procs=0`、`xfconfd_procs=1`、`xfce4_panel_procs=1`、`bwrap_shim=#!/bin/sh size=250`、divert 存在、零 `Wnck:ERROR`／`ICE I/O Error` |
| `kill -9` session bus（pid 89） | `kill9_rc=0`；`BUS_EXITED rc=137 -> group restart`；第二個 `SESSION_BUS pid=1462`；`panel_procs_after_bus_kill=1`；`supervisor_alive=yes` |
| `dpkg -i bubblewrap_0.11.1-1ubuntu0.3_arm64.deb` | `dpkg_rc=0`；`bwrap_head=#!/bin/sh`；`bwrap_size=250`；divert 保留；`panel_procs_after_dpkg=1`；零 Wnck／ICE 錯誤 |

取樣瞬間的 `xfconfd_procs_after_bus_kill=0` 屬預期：它在新 bus 上是延遲啟動。
見證日誌：`/Users/dylanwong/.gm-work/qqbox/harden-probe.log`。

### Live 5905 切換

先前的 5905 wrapper 在 24 小時的 `gtimeout` 到期後結束；在此之前，
2026-11-20 05:37 時舊版六迴圈監督器因為宿主磁碟耗盡、每個 X 客戶端都立即
結束，而觸發全部六個熔斷器。live 5905 隨後以強化後的 `start-xubuntu.sh`
（群組監督器 + dpkg-divert shim）重新啟動，`/out` 指向
`/Users/dylanwong/.gm-work/qqbox/live-5905-hardened`。

獨立的 RFB 3.8 見證：`qbox:99`、1280x800、4,096,000 raw bytes、6,187 種顏色，
可見 Applications 選單、三個啟動器、含 `Example Domain - Chro…` 與
`QQBOX-TERMINAL` 的工作列、Chromium 呈現 `https://example.com/`、
`root@qbox:/out#` 終端機與 Xubuntu 桌布，且沒有 keyring 對話框。開機證據：
`xfce4_session_procs=0`、`xfconfd_procs=1`、`xfce4_panel_procs=1`、面板視窗
`1280x41+0+0 +0+759`、shim `#!/bin/sh` size 250、divert 存在、
`QQBOX_BOOT_READY`。這消除了先前 live 5905 仍執行強化前指令稿的殘留問題。

## XFCE 錯誤與子 PTY 錯誤的處理

早期 26.04 開機流程會產生一連串錯誤。重複行程、D-Bus、PTY 載荷與視窗管理員錯誤皆已修復並實測；仍有一則診斷訊息，詳見下表與 [PORT_STATUS.zh-Hant.md](../../docs/PORT_STATUS.zh-Hant.md)。

| 錯誤 | 成因 | 修復 |
| --- | --- | --- |
| 每個互動式子 PTY 出現 `x27: command not found` | Dockerfile 的 `printf` 使用 `\x27`，dash 不解譯 `\x`，使字面 `\x27` 寫入 `/root/.bashrc` | 改用八進位 `\047`，啟動腳本就地移除殘留 `\x27` 行 |
| `bash: initialize job control: no job control in background: Bad file descriptor` | 離庫保留的移除前 Go 執行時期之 PTY 模擬未實作 `TIOCSPGRP`，`tcsetpgrp` 在讀取任何 rcfile 之前的工作控制初始化階段即回傳 `EBADF` | **尚未修復。** `SHELLOPTS=` 已隨附；rcfile 內的 `set +m` 與 `setsid --ctty` 在真實來賓 PTY 逐一實測，`setsid --ctty` 另在即時 VNC 5905 終端機打字實測，皆無法消除該列；終端機本身完整可用（`root@qbox:/out#`、RFB 打字回顯並執行）。先前宣稱已修復的敘述在此撤回；詳見 [PORT_STATUS.zh-Hant.md](../../docs/PORT_STATUS.zh-Hant.md) 的剩餘限制 |
| `bash: cannot set terminal process group (1)` | 證據腳本在無 PTY 下執行 `bash -i -c` | 改為 `grep -F 'PS1='`；真正的終端機由 RFB 打字見證 |
| `Another Window Manager … is already running`、`Another desktop manager …` | 舊版啟動腳本同時啟動 `xfwm4`／`xfce4-panel`／`xfdesktop`，而 `xfce4-session` 又各自 autostart 一份（兩個面板、兩個 xfconfd） | 完全移除 `xfce4-session`，由群組監督器各持一份 |
| `Failed to run gnome-keyring-daemon: Failed to execute child process` | 舊版 `xfce4-session` 的 `compat/LaunchGNOME=true` | 移除 `xfce4-session`，停用 keyring 的 autostart 並安裝 `gnome-keyring` |
| `FileNotFoundError: 'xdg-user-dir'` Python traceback | 缺少 `xdg-user-dirs` | 已安裝 |
| `xfdesktop_regular_file_icon_new … assertion` 與 `thunarx` GLib CRITICAL | vfs-only 根目錄上的桌面圖示外掛 | `desktop-icons/style=0`，桌布仍由 `xwallpaper` 繪製 |
| `SESSION_MANAGER environment variable not defined` | 舊版終端機在 session 之外啟動 | 由群組監督器直接啟動終端機，不再依賴 session manager |
| 從工作列點擊 Chromium 時 `/usr/bin/chromium … Segmentation fault` | 原廠 `chromium.desktop` → `wrapped-chromium` → Ubuntu snap stub | `.desktop` 的 Exec 覆寫為 `/usr/local/bin/chromium`，`wrapped-chromium` 指向 Debian wrapper |

其餘日誌為資訊性質而非錯誤：一則 `libxfce4util-WARNING: Failed to get a
systemd proxy`（qqbox 沒有 systemd `login1`）、xfwm4 發布屬性前的 `_NET_*`
「assuming」訊息，以及移除 D-Bus 觸發點後預期中的
`org.freedesktop.PackageKit ServiceUnknown`。全新開機的
panel／terminal／wm／xfdesktop 日誌沒有 ERROR、CRITICAL、assertion、
child-exec、PTY 或 Traceback 行。

## 底部工作列為何消失

Xubuntu 26 的第一個建置完全沒有底部工作列。實測 `panel.log` 顯示：
26.04 的 gdk-pixbuf 2.44 把可縮放圖示交給 glycin，而 glycin 的載入器由
bubblewrap 啟動；qqbox 下 `bwrap` 明確失敗為
`bwrap: Failed to make / slave: Bad address`，`xfce4-panel` 因此在 GTK
icon-helper 斷言上中止，Applications 選單、啟動器與工作列全部消失。
只安裝 `librsvg2-common` 無效（GTK 仍優先使用
`/usr/share/glycin-loaders/2+/conf.d/` 的登錄），單純移除登錄則變成
`No image loaders are configured`。

修復（已寫入映像檔）：把 `/usr/bin/bwrap` 換成狹義相容墊片，只在參數中出現
`/usr/libexec/glycin-loaders/…` 時直接執行**真正的** glycin 載入器並保留其
參數；其餘呼叫一律交回保存的 `/usr/bin/bwrap.qqbox-orig`。這裡只捨棄 qqbox
無法提供的 mount 沙箱；qqbox 的 `unshare()` 本來就是「回報成功但無效果」，
bubblewrap 在此不可能提供真正的隔離，來賓仍受 qqbox 本身限制。此取捨如實
揭露。實測：`xfce4-panel 1280x41+0+0 +0+759`、兩個面板行程、底部 50px
有 2,623 種顏色，畫面可見 Applications 選單、三個啟動器與含
`Example Domain - Chromium`、`QQBOX-TERMINAL` 的工作列。

## 建置與執行

```sh
sh fetch-chromium.sh
sh fetch-extras.sh
QQBOX_RUNTIME=/path/to/pre-removal-laboon-runtime
"$QQBOX_RUNTIME" build -net -t qqbox-xubuntu:26 -f Dockerfile -o qqbox-xubuntu26.tar .
"$QQBOX_RUNTIME" -image qqbox-xubuntu:26 -net -e VNC_PORT=5905 -mount /tmp/qqbox-out:/out
vncviewer 127.0.0.1:5905
```

`build` 與 `-image` 是**移除前的 Go 執行期二進位檔**的指令（保存在版本庫
之外）。本專案的 Rust `qqbox` CLI 沒有 `build` 子命令，也沒有 `-image` 旗標
（`qqbox build --help` 回傳 rc=2）；請勿換成 Rust 二進位檔。

Chromium 的真實沙箱在 qqbox 下不可啟用（27 種組態實測 rc=1／134），
並非效能開關而是安全邊界；`/dev/dri` 不存在，SwiftShader 軟體 WebGL 1.0 可用。
PulseAudio null sink 可用，但沒有 `/dev/snd`，聲音不會傳到宿主喇叭。
詳見 [`README.md`](../../../examples/qqbox/README.md) 與 [`../../docs/PORT_STATUS.zh-Hant.md`](../../docs/PORT_STATUS.zh-Hant.md)。
