@echo off
chcp 65001 >nul
cd /d "%~dp0"
set "LOG=%~dp0QKA-诊断结果.txt"

echo === QKA 诊断 %date% %time% === > "%LOG%"
echo. >> "%LOG%"

echo [1] Windows 版本 >> "%LOG%"
ver >> "%LOG%" 2>&1
echo. >> "%LOG%"

echo [2] WebView2 运行时版本（下面为空/报错 = 未安装） >> "%LOG%"
reg query "HKLM\SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" /v pv >> "%LOG%" 2>&1
reg query "HKCU\SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" /v pv >> "%LOG%" 2>&1
echo. >> "%LOG%"

echo [3] 必备文件（QKA.exe 必须和这些在同一目录） >> "%LOG%"
for %%f in (QKA.exe QKA-cli.exe bun.exe) do if exist "%%f" (echo   OK  %%f >> "%LOG%") else (echo   缺失 %%f >> "%LOG%")
if exist "adapters\" (echo   OK  adapters\ >> "%LOG%") else (echo   缺失 adapters\ >> "%LOG%")
if exist "provider-plugins\" (echo   OK  provider-plugins\ >> "%LOG%") else (echo   缺失 provider-plugins\ >> "%LOG%")
echo. >> "%LOG%"

echo [4] 后台启动 QKA-cli.exe tray，6 秒后看进程是否存活、有没有报错 >> "%LOG%"
del "%TEMP%\qka-tray-err.txt" >nul 2>&1
start "" /min cmd /c ""%~dp0QKA-cli.exe" tray 2> "%TEMP%\qka-tray-err.txt""
timeout /t 6 /nobreak >nul
tasklist /fi "imagename eq QKA-cli.exe" | find /i "QKA-cli.exe" >nul
if errorlevel 1 (echo   进程已退出 = 启动失败 >> "%LOG%") else (echo   进程存活 = GUI 事件循环已启动 >> "%LOG%")
if exist "%TEMP%\qka-tray-err.txt" (
  echo   --- 错误输出 --- >> "%LOG%"
  type "%TEMP%\qka-tray-err.txt" >> "%LOG%"
)
taskkill /f /im QKA-cli.exe >nul 2>&1
echo. >> "%LOG%"

echo [5] 当前 QKA.exe 进程 >> "%LOG%"
tasklist /fi "imagename eq QKA.exe" >> "%LOG%" 2>&1
echo. >> "%LOG%"

echo 完成。结果文件：%LOG% >> "%LOG%"
type "%LOG%"
echo.
echo 结果已写入：%LOG%
echo 把上面的内容（或这个文件）发给我。
pause
