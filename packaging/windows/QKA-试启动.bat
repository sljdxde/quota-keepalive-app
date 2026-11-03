@echo off
chcp 65001 >nul
cd /d "%~dp0"

echo 正在以「强制弹主窗口 + 关闭 WebView2 硬件加速」方式启动……
set QKA_DEBUG_SHOW=main
set WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS=--disable-gpu
start "" "%~dp0QKA.exe"

echo 已启动，等 8 秒检查进程。
echo   - 出现标题为 Quota Keepalive 的窗口：说明只是显卡/WebView2 渲染问题
echo   - 没有任何窗口但进程存活：说明是托盘图标问题，不是崩溃
timeout /t 8 /nobreak >nul
tasklist /fi "imagename eq QKA.exe" | find /i "QKA.exe" >nul
if errorlevel 1 (echo [X] QKA.exe 已经退出：启动即失败，请运行 QKA-诊断.bat) else (echo [OK] QKA.exe 正在运行)
echo.
pause
