package main

import (
	"fmt"
	"io"
)

// verifyPE 逐项校验并打印结果，返回失败项描述（空切片 = 全部通过）。
// expectVersion / expectIdentity 为空表示跳过对应项（打印跳过说明）。
func verifyPE(w io.Writer, data []byte, expectVersion, expectIdentity string) []string {
	var fails []string
	ok := func(format string, args ...any) {
		fmt.Fprintf(w, "  ok   "+format+"\n", args...)
	}
	fail := func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		fails = append(fails, msg)
		fmt.Fprintf(w, "  FAIL "+format+"\n", args...)
	}

	pe, err := parsePE(data)
	if err != nil {
		fail("PE 解析：%v", err)
		return fails
	}

	// ③ 子系统必须是 Windows GUI。
	if pe.subsystem == peSubsystemWindowsGUI {
		ok("Subsystem=%d（Windows GUI）", pe.subsystem)
	} else {
		fail("Subsystem=%d，应为 %d（Windows GUI）", pe.subsystem, peSubsystemWindowsGUI)
	}

	// ① .rsrc 段存在；缺失时说明资源根本没链接进 exe，后续资源项一并跳过。
	var entries []resourceEntry
	if !pe.hasSection(".rsrc") {
		fail("段表缺少 .rsrc 段：图标/版本/manifest 都不会链接进 exe")
	} else {
		ok("段表存在 .rsrc 段")
		entries, err = pe.resources()
		if err != nil {
			fail("资源目录解析：%v", err)
			entries = nil
		}
	}

	if entries != nil {
		// ② 类型级资源齐全。
		types := map[uint32]bool{}
		for _, e := range entries {
			types[e.typ] = true
		}
		if types[rtIcon] && types[rtGroupIcon] {
			ok("RT_ICON(3) 与 RT_GROUP_ICON(14) 均存在")
		} else {
			fail("图标资源不完整：RT_ICON 存在=%v，RT_GROUP_ICON 存在=%v（两者都需要）",
				types[rtIcon], types[rtGroupIcon])
		}

		// ④ 版本资源与期望一致。
		if expectVersion == "" {
			fmt.Fprintf(w, "  --   版本校验跳过（未传 -version）\n")
		} else {
			verRes := firstOfType(entries, rtVersion)
			if verRes == nil {
				fail("缺少 RT_VERSION(16) 版本资源")
			} else {
				fails = append(fails, checkVersion(w, verRes, expectVersion)...)
			}
		}

		// ⑤ manifest 里 identity 一致。
		if expectIdentity == "" {
			fmt.Fprintf(w, "  --   manifest 校验跳过（未传 -identity/-winres）\n")
		} else {
			manRes := firstOfType(entries, rtManifest)
			if manRes == nil {
				fail("缺少 RT_MANIFEST(24)")
			} else if containsIdentity(decodeManifestText(manRes), expectIdentity) {
				ok("manifest 包含 identity %q", expectIdentity)
			} else {
				fail("manifest 未包含 identity %q", expectIdentity)
			}
		}
	}
	return fails
}

// checkVersion 校验版本资源：定长 FileVersion/ProductVersion 必须等于期望
// （四元组归一化比较，"0.1.23" 与 "0.1.23.0" 视为相同）；字符串表带
// FileVersion/ProductVersion 时也必须一致。打印逐项结果并返回失败项。
func checkVersion(w io.Writer, res []byte, expect string) []string {
	var fails []string
	want, okq := parseVersionQuad(expect)
	if !okq {
		msg := fmt.Sprintf("期望版本 %q 无法解析为 x.y.z[.w]", expect)
		fmt.Fprintf(w, "  FAIL %s\n", msg)
		return []string{msg}
	}
	fixed, _, err := parseVSFixedInfo(res)
	if err != nil {
		fmt.Fprintf(w, "  FAIL 版本资源：%v\n", err)
		return []string{err.Error()}
	}
	gotFile, gotProd := formatQuad(fixed.FileVersion), formatQuad(fixed.ProductVersion)
	if fixed.FileVersion == want && fixed.ProductVersion == want {
		fmt.Fprintf(w, "  ok   FileVersion=%s ProductVersion=%s\n", gotFile, gotProd)
	} else {
		msg := fmt.Sprintf("版本不一致：FileVersion=%s ProductVersion=%s，期望 %s",
			gotFile, gotProd, formatQuad(want))
		fmt.Fprintf(w, "  FAIL %s\n", msg)
		fails = append(fails, msg)
	}
	for _, key := range []string{"FileVersion", "ProductVersion"} {
		val, present := versionStringValues(res)[key]
		if !present || val == "" {
			continue
		}
		if got, okv := parseVersionQuad(val); okv && got == want {
			fmt.Fprintf(w, "  ok   版本字符串 %s=%q\n", key, val)
		} else {
			msg := fmt.Sprintf("版本字符串 %s=%q 与期望 %s 不一致", key, val, formatQuad(want))
			fmt.Fprintf(w, "  FAIL %s\n", msg)
			fails = append(fails, msg)
		}
	}
	return fails
}
