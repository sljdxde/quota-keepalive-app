package selfinstall

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// buildLinkInfo 造一个指向 target 的 LinkInfo 结构：不带 VolumeID，
// LocalBasePath 直接排在头部之后（LocalBasePathOffset = 头部长度）。
func buildLinkInfo(target string, withUnicode bool) []byte {
	headerSize := linkInfoMinHeaderSize
	if withUnicode {
		headerSize = linkInfoLocalPathUnicodeHeaderSize
	}
	size := headerSize + len(target) + 1
	info := make([]byte, size)
	binary.LittleEndian.PutUint32(info[0:], uint32(size))
	binary.LittleEndian.PutUint32(info[4:], uint32(headerSize))
	binary.LittleEndian.PutUint32(info[8:], linkInfoLocalPathFlag)
	binary.LittleEndian.PutUint32(info[0x10:], uint32(headerSize)) // LocalBasePathOffset
	copy(info[headerSize:], target)
	return info
}

// writeLnkFixture 生成一个最小可解析的 .lnk：76 字节头部 + IDList + LinkInfo。
// withUnicode 时头部声明 0x24 并附上 UTF-16 版本地路径（优先于 ANSI 版）。
func writeLnkFixture(t *testing.T, target string, withUnicode bool) string {
	t.Helper()
	header := make([]byte, lnkHeaderSize)
	binary.LittleEndian.PutUint32(header, lnkHeaderSize)
	copy(header[4:], make([]byte, 16)) // LinkCLSID，解析不校验，全零即可
	flags := uint32(lnkHasIDList | lnkHasLinkInfo)
	binary.LittleEndian.PutUint32(header[lnkFlagsOffset:], flags)

	// IDList 字段 = uint16 总字节数 + 该数量的数据字节；这里声明 2 字节数据。
	idList := []byte{0x02, 0x00, 0x00, 0x70}

	var info []byte
	if withUnicode {
		unicode := make([]byte, 0, linkInfoLocalPathUnicodeHeaderSize)
		for _, r := range target {
			if r > 0xFFFF { // 测试用例都是 BMP 内字符，直接截断
				r = '?'
			}
			unicode = append(unicode, byte(r), byte(r>>8))
		}
		unicode = append(unicode, 0, 0)

		headerSize := linkInfoLocalPathUnicodeHeaderSize
		size := headerSize + len("C:\\ansi-fallback") + 1 + len(unicode)
		info = make([]byte, size)
		binary.LittleEndian.PutUint32(info[0:], uint32(size))
		binary.LittleEndian.PutUint32(info[4:], uint32(headerSize))
		binary.LittleEndian.PutUint32(info[8:], linkInfoLocalPathFlag)
		binary.LittleEndian.PutUint32(info[0x10:], uint32(headerSize))                            // ANSI 版偏移
		binary.LittleEndian.PutUint32(info[0x1C:], uint32(headerSize+len("C:\\ansi-fallback")+1)) // Unicode 版偏移
		copy(info[headerSize:], "C:\\ansi-fallback")
		copy(info[headerSize+len("C:\\ansi-fallback")+1:], unicode)
	} else {
		info = buildLinkInfo(target, false)
	}

	data := append(header, idList...)
	data = append(data, info...)
	path := filepath.Join(t.TempDir(), "Quota Keepalive.lnk")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestShortcutTarget(t *testing.T) {
	t.Run("解析 ANSI 本地路径", func(t *testing.T) {
		lnk := writeLnkFixture(t, `C:\Users\me\AppData\Local\Programs\Quota Keepalive\QKA.exe`, false)
		got, ok := ShortcutTarget(lnk)
		if !ok || got != `C:\Users\me\AppData\Local\Programs\Quota Keepalive\QKA.exe` {
			t.Fatalf("ShortcutTarget = %q/%v", got, ok)
		}
	})

	t.Run("优先 Unicode 本地路径", func(t *testing.T) {
		lnk := writeLnkFixture(t, `D:\应用目录\QKA.exe`, true)
		got, ok := ShortcutTarget(lnk)
		if !ok || got != `D:\应用目录\QKA.exe` {
			t.Fatalf("ShortcutTarget = %q/%v，want Unicode 变体", got, ok)
		}
	})

	t.Run("文件不存在", func(t *testing.T) {
		if got, ok := ShortcutTarget(filepath.Join(t.TempDir(), "missing.lnk")); ok || got != "" {
			t.Fatalf("不存在的 .lnk 应为未命中，实际 %q/%v", got, ok)
		}
	})

	t.Run("头部尺寸不对", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "bad.lnk")
		if err := os.WriteFile(path, []byte("not a shortcut at all"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got, ok := ShortcutTarget(path); ok || got != "" {
			t.Fatalf("非 .lnk 内容应为未命中，实际 %q/%v", got, ok)
		}
	})

	t.Run("没有 LinkInfo 标志", func(t *testing.T) {
		header := make([]byte, lnkHeaderSize)
		binary.LittleEndian.PutUint32(header, lnkHeaderSize)
		binary.LittleEndian.PutUint32(header[lnkFlagsOffset:], lnkHasIDList) // 只有 IDList
		path := filepath.Join(t.TempDir(), "no-linkinfo.lnk")
		if err := os.WriteFile(path, append(header, 0x00, 0x00), 0o644); err != nil {
			t.Fatal(err)
		}
		if got, ok := ShortcutTarget(path); ok || got != "" {
			t.Fatalf("没有 LinkInfo 应为未命中，实际 %q/%v", got, ok)
		}
	})

	t.Run("IDList 声明的长度越界", func(t *testing.T) {
		header := make([]byte, lnkHeaderSize)
		binary.LittleEndian.PutUint32(header, lnkHeaderSize)
		binary.LittleEndian.PutUint32(header[lnkFlagsOffset:], lnkHasIDList|lnkHasLinkInfo)
		path := filepath.Join(t.TempDir(), "overflow.lnk")
		// IDList 声明 0x7000 字节，但文件后面什么都没有。
		if err := os.WriteFile(path, append(header, 0x00, 0x70), 0o644); err != nil {
			t.Fatal(err)
		}
		if got, ok := ShortcutTarget(path); ok || got != "" {
			t.Fatalf("越界的 IDList 应为未命中，实际 %q/%v", got, ok)
		}
	})

	t.Run("截断的 LinkInfo", func(t *testing.T) {
		header := make([]byte, lnkHeaderSize)
		binary.LittleEndian.PutUint32(header, lnkHeaderSize)
		binary.LittleEndian.PutUint32(header[lnkFlagsOffset:], lnkHasLinkInfo)
		path := filepath.Join(t.TempDir(), "truncated.lnk")
		if err := os.WriteFile(path, append(header, 0x20, 0x00, 0x00, 0x00), 0o644); err != nil {
			t.Fatal(err)
		}
		if got, ok := ShortcutTarget(path); ok || got != "" {
			t.Fatalf("截断的 LinkInfo 应为未命中，实际 %q/%v", got, ok)
		}
	})

	t.Run("LocalBasePath 缺少 NUL 终止符", func(t *testing.T) {
		headerSize := linkInfoMinHeaderSize
		target := `C:\no-terminator\QKA.exe`
		// 结构与 buildLinkInfo 相同，但末尾不补 NUL。
		size := headerSize + len(target)
		info := make([]byte, size)
		binary.LittleEndian.PutUint32(info[0:], uint32(size))
		binary.LittleEndian.PutUint32(info[4:], uint32(headerSize))
		binary.LittleEndian.PutUint32(info[8:], linkInfoLocalPathFlag)
		binary.LittleEndian.PutUint32(info[0x10:], uint32(headerSize))
		copy(info[headerSize:], target)

		header := make([]byte, lnkHeaderSize)
		binary.LittleEndian.PutUint32(header, lnkHeaderSize)
		binary.LittleEndian.PutUint32(header[lnkFlagsOffset:], lnkHasLinkInfo)
		path := filepath.Join(t.TempDir(), "no-nul.lnk")
		if err := os.WriteFile(path, append(header, info...), 0o644); err != nil {
			t.Fatal(err)
		}
		if got, ok := ShortcutTarget(path); ok || got != "" {
			t.Fatalf("缺终止符应为未命中，实际 %q/%v", got, ok)
		}
	})
}
