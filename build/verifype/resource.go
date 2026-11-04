package main

import "fmt"

// 资源类型 ID（winuser.h 的一小部分）。
const (
	rtIcon      = 3
	rtGroupIcon = 14
	rtVersion   = 16
	rtManifest  = 24
)

// resourceEntry 是资源目录的一个叶子：类型→名称→语言→数据。
type resourceEntry struct {
	typ  uint32
	name uint32
	lang uint32
	data []byte
}

// resources 遍历资源目录（类型→名称→语言→叶子），返回全部叶子条目。
// 目录头 16 字节（条目数在偏移 12/14），随后每项 8 字节：Name/ID 与
// OffsetToData；OffsetToData 最高位为 1 时指向下一层子目录（相对资源目录
// 起始的低 31 位偏移），否则指向 IMAGE_RESOURCE_DATA_ENTRY——其内的
// OffsetToData 是叶子数据的 RVA，需按段表折回文件偏移。对畸形目录返回
// 错误，绝不 panic。
func (p *peFile) resources() ([]resourceEntry, error) {
	root := p.resourceDirOff
	if root == 0 {
		return nil, fmt.Errorf("找不到资源目录（数据目录与 .rsrc 段都没有）")
	}
	var out []resourceEntry
	seen := map[uint64]bool{}
	var walk func(dirOff uint64, typ, name, lang uint32, level int) error
	walk = func(dirOff uint64, typ, name, lang uint32, level int) error {
		if level > 3 {
			return fmt.Errorf("资源目录层级超过 3（偏移 0x%x），疑似损坏", dirOff)
		}
		if seen[dirOff] {
			return nil // 防环：同一子目录被重复引用时只走一次
		}
		seen[dirOff] = true
		if dirOff+16 > uint64(len(p.data)) {
			return fmt.Errorf("资源目录头越界（偏移 0x%x）", dirOff)
		}
		named := int(le16(p.data[dirOff+12 : dirOff+14]))
		ids := int(le16(p.data[dirOff+14 : dirOff+16]))
		count := named + ids
		if dirOff+16+uint64(count)*8 > uint64(len(p.data)) {
			return fmt.Errorf("资源目录条目越界（偏移 0x%x，%d 项）", dirOff, count)
		}
		for i := 0; i < count; i++ {
			e := dirOff + 16 + uint64(i)*8
			nameOrID := le32(p.data[e : e+4])
			offset := le32(p.data[e+4 : e+8])
			eTyp, eName, eLang := typ, name, lang
			switch level {
			case 0:
				if nameOrID&0x80000000 != 0 {
					continue // 命名类型（字符串名）；QKA 的资源都是标准 ID 类型
				}
				eTyp = nameOrID
			case 1:
				eName = nameOrID & 0x7fffffff
			case 2:
				eLang = nameOrID & 0x7fffffff
			}
			if offset&0x80000000 != 0 {
				if err := walk(root+uint64(offset&0x7fffffff), eTyp, eName, eLang, level+1); err != nil {
					return err
				}
				continue
			}
			leaf := root + uint64(offset&0x7fffffff)
			if leaf+16 > uint64(len(p.data)) {
				return fmt.Errorf("资源数据项越界（偏移 0x%x）", leaf)
			}
			dataRVA := le32(p.data[leaf : leaf+4])
			size := le32(p.data[leaf+4 : leaf+8])
			fileOff, err := p.rvaToOffset(dataRVA)
			if err != nil {
				return fmt.Errorf("资源(type=%d)：%w", eTyp, err)
			}
			if fileOff+uint64(size) > uint64(len(p.data)) {
				return fmt.Errorf("资源(type=%d)数据越界（size=%d）", eTyp, size)
			}
			out = append(out, resourceEntry{typ: eTyp, name: eName, lang: eLang, data: p.data[fileOff : fileOff+uint64(size)]})
		}
		return nil
	}
	if err := walk(root, 0, 0, 0, 0); err != nil {
		return nil, err
	}
	if out == nil {
		out = []resourceEntry{}
	}
	return out, nil
}

// firstOfType 返回第一个该类型的资源数据（没有则 nil）。
func firstOfType(entries []resourceEntry, typ uint32) []byte {
	for _, e := range entries {
		if e.typ == typ {
			return e.data
		}
	}
	return nil
}
