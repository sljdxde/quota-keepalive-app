QKA Windows —— CI 冒烟用目录说明（不是发行物说明）

正式的 Windows 发行物是单个自包含 exe：
  QKA-<version>-windows-<arch>.exe
Bun、adapters/、provider-plugins/ 都内嵌在 exe 里，首次运行解压到
%LOCALAPPDATA%\QKA\runtime\<version>\；没有 zip、没有安装器，也不需要
把附属文件放在 exe 旁边。

本目录里的文件只被 .github/workflows/windows.yml 用于在 CI 上组装一个
“目录形态”的冒烟夹具（scripts/windows-smoke.ps1 走的是 CLI 行为检查），
让 QKA-cli.exe、adapters/、provider-plugins/ 和 bun.exe 能按旧布局被
逐项验证。它不是给用户下载的包。

发行构建请用：
  make release-windows        # scripts/build-windows-exe.sh
