package main

import (
	"bytes"
	"fmt"
)

// PE 骨架解析（只取校验所需字段）。布局参考 winnt.h：DOS 头 e_lfanew 在
// 0x3C；PE 签名后跟 20 字节 COFF 头、optional header、每项 40 字节的段表。
// Subsystem 在 optional header 偏移 68（PE32 与 PE32+ 相同）。
const (
	peSubsystemWindowsGUI         = 2
	optionalHeaderSubsystemOffset = 68
)

// section 是段表里的一项（只保留校验用得到的字段）。
type section struct {
	Name             string
	VirtualSize      uint32
	VirtualAddress   uint32
	SizeOfRawData    uint32
	PointerToRawData uint32
}

// peFile 是解析结果；resourceDirOff 为资源目录在文件内的偏移，0 表示没有。
type peFile struct {
	data           []byte
	machine        uint16
	subsystem      uint16
	sections       []section
	resourceDirOff uint64
}

func le16(b []byte) uint16 {
	return uint16(b[0]) | uint16(b[1])<<8
}

func le32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

// parsePE 解析 PE 头与段表。对畸形输入返回错误，绝不越界读。
func parsePE(data []byte) (*peFile, error) {
	if len(data) < 0x40 {
		return nil, fmt.Errorf("文件只有 %d 字节，不是 PE", len(data))
	}
	if string(data[0:2]) != "MZ" {
		return nil, fmt.Errorf("缺少 MZ(DOS) 头")
	}
	peOffRaw := le32(data[0x3c:0x40])
	if peOffRaw == 0 || uint64(peOffRaw)+4+20 > uint64(len(data)) {
		return nil, fmt.Errorf("e_lfanew=0x%x 越界", peOffRaw)
	}
	peOff := int(peOffRaw)
	if string(data[peOff:peOff+4]) != "PE\x00\x00" {
		return nil, fmt.Errorf("缺少 PE\\0\\0 签名")
	}
	numSections := int(le16(data[peOff+6 : peOff+8]))
	sizeOfOpt := int(le16(data[peOff+20 : peOff+22]))
	if sizeOfOpt < optionalHeaderSubsystemOffset+2 {
		return nil, fmt.Errorf("optional header 只有 %d 字节，读不到 Subsystem", sizeOfOpt)
	}
	opt := peOff + 24
	if uint64(opt)+uint64(sizeOfOpt) > uint64(len(data)) {
		return nil, fmt.Errorf("optional header 越界（offset=0x%x size=%d）", opt, sizeOfOpt)
	}
	pf := &peFile{
		data:      data,
		machine:   le16(data[peOff+4 : peOff+6]),
		subsystem: le16(data[opt+optionalHeaderSubsystemOffset : opt+optionalHeaderSubsystemOffset+2]),
	}

	// 数据目录基址：PE32 在 optional header 偏移 96，PE32+ 在 112；资源表是
	// 第 2 项（索引 2）。目录数不足 3 时视为没有资源表。
	var dirBase int
	switch magic := le16(data[opt : opt+2]); magic {
	case 0x10b: // PE32
		dirBase = opt + 96
	case 0x20b: // PE32+
		dirBase = opt + 112
	default:
		return nil, fmt.Errorf("未知 optional header magic 0x%x", magic)
	}
	var resRVA uint32
	if dirBase+24 <= len(data) {
		resRVA = le32(data[dirBase+16 : dirBase+20])
	}

	secStart := opt + sizeOfOpt
	if uint64(secStart)+uint64(numSections)*40 > uint64(len(data)) {
		return nil, fmt.Errorf("段表越界（%d 段）", numSections)
	}
	for i := 0; i < numSections; i++ {
		off := secStart + i*40
		pf.sections = append(pf.sections, section{
			Name:             string(bytes.TrimRight(data[off:off+8], "\x00")),
			VirtualSize:      le32(data[off+8 : off+12]),
			VirtualAddress:   le32(data[off+12 : off+16]),
			SizeOfRawData:    le32(data[off+16 : off+20]),
			PointerToRawData: le32(data[off+20 : off+24]),
		})
	}

	// 资源目录优先取数据目录里的 RVA；拿不到（个别工具不写数据目录）再退
	// 回 .rsrc 段头（资源目录总是位于该段开头）。
	if resRVA != 0 {
		if off, err := pf.rvaToOffset(resRVA); err == nil {
			pf.resourceDirOff = off
		}
	}
	if pf.resourceDirOff == 0 {
		for _, s := range pf.sections {
			if s.Name == ".rsrc" && s.PointerToRawData != 0 {
				pf.resourceDirOff = uint64(s.PointerToRawData)
				break
			}
		}
	}
	return pf, nil
}

func (p *peFile) hasSection(name string) bool {
	for _, s := range p.sections {
		if s.Name == name {
			return true
		}
	}
	return false
}

// rvaToOffset 把相对虚拟地址按段表映射折算成文件内偏移。
func (p *peFile) rvaToOffset(rva uint32) (uint64, error) {
	for _, s := range p.sections {
		size := s.VirtualSize
		if size == 0 {
			size = s.SizeOfRawData
		}
		if size == 0 {
			continue
		}
		if uint64(rva) >= uint64(s.VirtualAddress) &&
			uint64(rva) < uint64(s.VirtualAddress)+uint64(size) {
			off := uint64(rva-s.VirtualAddress) + uint64(s.PointerToRawData)
			if off >= uint64(len(p.data)) {
				return 0, fmt.Errorf("RVA 0x%x 映射到文件外", rva)
			}
			return off, nil
		}
	}
	return 0, fmt.Errorf("RVA 0x%x 不落在任何段内", rva)
}
