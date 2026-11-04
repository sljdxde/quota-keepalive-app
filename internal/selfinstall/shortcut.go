package selfinstall

import (
	"bytes"
	"encoding/binary"
	"os"
	"strings"
	"unicode/utf16"
)

// Shell Link (.lnk) 二进制格式的常量，见 [MS-SHLLINK] 2.x。这里只解析出目标
// 本地路径，不读写其它结构。
const (
	// lnkHeaderSize 是 ShellLinkHeader 的固定长度：size(4) + LinkCLSID(16) +
	// LinkFlags(4) + FileAttributes(4) + 三个时间戳(24) + 文件大小等(16)。
	lnkHeaderSize = 0x4C
	// lnkFlagsOffset 是 LinkFlags 在头部里的偏移。
	lnkFlagsOffset = 0x14
	// lnkHasIDList / lnkHasLinkInfo 是 LinkFlags 里影响布局的两个位：前者表示
	// 头部之后跟着 IDList（要先跳过才能落到 LinkInfo），后者表示存在 LinkInfo
	// （本地目标路径就在里面）。
	lnkHasIDList   = 0x00000001
	lnkHasLinkInfo = 0x00000002

	// linkInfoMinHeaderSize 是 LinkInfo 头部的最小长度：size + headerSize +
	// flags + 四个偏移，共 7 个 uint32。
	linkInfoMinHeaderSize = 0x1C
	// linkInfoLocalPathUnicodeHeaderSize 是带 Unicode 本地路径偏移的头部长度
	// （0x1C 之上再加两个 uint32）。
	linkInfoLocalPathUnicodeHeaderSize = 0x24
	// linkInfoLocalPathFlag 是 LinkInfoFlags 里的 VolumeIDAndLocalBasePath 位，
	// 置 1 才有 ANSI 版 LocalBasePath。
	linkInfoLocalPathFlag = 0x00000001
)

// ShortcutTarget 解析 lnkPath 指向的 .lnk 快捷方式，返回它指向的目标本地路径。
// 给 installer 在 install.json 丢失后探测「是否装过」用：开始菜单快捷方式的
// 目标就是已安装 exe 的位置。
//
// 只做二进制解析、不开 COM：探测要求低成本（一次文件读加几十字节的结构
// 走查），而且解析失败一律 ok=false——探测语义下任何失败都只是一条「无线索」，
// 调用方按未命中处理即可，不需要区分失败原因。路径优先取 Unicode 变体
// LocalBasePathOffsetUnicode（ANSI 变体在路径含非 ANSI 字符时会丢失）；指向
// 网络位置（UNC）的快捷方式没有本地路径，同样返回 false。
//
// 已知局限：部分工具（如 WScript.Shell）写出的 .lnk 只有 ANSI 版 LocalBasePath，
// 且字节按系统代码页（如 GBK）编码。这里不引入代码页解码依赖，按原始字节
// 返回——纯 ASCII 路径不受影响；含非 ASCII 字符时调用方的存在性检查自然不
// 中，等价于按未命中降级，不会误锁安装目录。
func ShortcutTarget(lnkPath string) (string, bool) {
	data, err := os.ReadFile(lnkPath)
	if err != nil {
		return "", false
	}
	if len(data) < lnkHeaderSize || binary.LittleEndian.Uint32(data) != lnkHeaderSize {
		return "", false
	}
	flags := binary.LittleEndian.Uint32(data[lnkFlagsOffset:])
	off := lnkHeaderSize
	if flags&lnkHasIDList != 0 {
		// IDList 的前缀是 uint16 的总字节数，整段跳过后才是 LinkInfo。
		if off+2 > len(data) {
			return "", false
		}
		off += 2 + int(binary.LittleEndian.Uint16(data[off:]))
		if off > len(data) {
			return "", false
		}
	}
	if flags&lnkHasLinkInfo == 0 {
		return "", false
	}
	info := data[off:]
	if len(info) < linkInfoMinHeaderSize {
		return "", false
	}
	infoSize := int(binary.LittleEndian.Uint32(info))
	if infoSize < linkInfoMinHeaderSize || infoSize > len(info) {
		return "", false
	}
	infoHeaderSize := int(binary.LittleEndian.Uint32(info[4:]))
	infoFlags := binary.LittleEndian.Uint32(info[8:])

	// 头部足够长时优先取 UTF-16 的 LocalBasePath：现代 Windows 写 .lnk 时两个
	// 变体都在，Unicode 版不会被路径里的非 ANSI 字符截断。
	if infoHeaderSize >= linkInfoLocalPathUnicodeHeaderSize {
		if uOff := int(binary.LittleEndian.Uint32(info[0x1C:])); uOff > 0 && uOff < infoSize {
			if path, ok := utf16LocalBasePath(info[uOff:infoSize]); ok {
				return path, true
			}
		}
	}
	if infoFlags&linkInfoLocalPathFlag == 0 {
		return "", false
	}
	// LocalBasePathOffset 相对 LinkInfo 起点，指向 NUL 结尾的 ANSI 字符串。
	localOff := int(binary.LittleEndian.Uint32(info[0x10:]))
	if localOff <= 0 || localOff >= infoSize {
		return "", false
	}
	end := bytes.IndexByte(info[localOff:infoSize], 0)
	if end < 0 {
		return "", false
	}
	path := string(info[localOff : localOff+end])
	if strings.TrimSpace(path) == "" {
		return "", false
	}
	return path, true
}

// utf16LocalBasePath 解析 UTF-16LE、NUL 结尾的本地路径；缺少终止符视为损坏。
func utf16LocalBasePath(b []byte) (string, bool) {
	units := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		unit := binary.LittleEndian.Uint16(b[i:])
		if unit == 0 {
			path := string(utf16.Decode(units))
			return path, strings.TrimSpace(path) != ""
		}
		units = append(units, unit)
	}
	return "", false
}
