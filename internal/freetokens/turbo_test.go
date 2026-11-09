package freetokens

import (
	"encoding/json"
	"strings"
	"testing"
)

// flattenJSON 用真实 turbo-stream v2 形状构造数据：下标即引用 id，负数
// 内联常量当 null。
func flattenJSON(t *testing.T, table []any) []byte {
	t.Helper()
	raw, err := json.Marshal(table)
	if err != nil {
		t.Fatalf("构造 flatten 数据失败: %v", err)
	}
	return raw
}

func validFlattenTable() []any {
	return []any{
		map[string]any{"root": 1, "free-tokens": 2},
		map[string]any{"version": 1},
		map[string]any{"data": 3},
		map[string]any{
			"deals":     []any{4},
			"ended":     []any{},
			"basis":     5,
			"serverNow": "2026-11-09T10:00:00.000Z",
			"expiresAt": "2026-11-09T10:10:00.000Z",
		},
		map[string]any{
			"id": 6, "title": 7, "provider": 8, "kind": 9,
			"counted": 10, "label": 11, "yuan": 12, "tags": []any{13},
		},
		map[string]any{"mix": 14, "ratio": 15, "usdCny": 16},
		"minimax-daily-400",
		"MiniMax 每日领 400",
		"MiniMax",
		"daily",
		true,
		"400 积分",
		6.5,
		"每日签到",
		"一般用量",
		"7 : 2 : 1",
		6.6713,
	}
}

func TestDecodeTurboStreamHydratesRoot(t *testing.T) {
	root, err := DecodeTurboStream(flattenJSON(t, validFlattenTable()))
	if err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	ft, ok := root["free-tokens"].(map[string]any)
	if !ok {
		t.Fatalf("缺少 free-tokens 节点: %+v", root)
	}
	data, ok := ft["data"].(map[string]any)
	if !ok {
		t.Fatalf("缺少 data 节点: %+v", ft)
	}
	if _, ok := data["deals"].([]any); !ok {
		t.Fatalf("缺少 deals: %+v", data)
	}
}

func TestDecodeSnapshotProjectsDeal(t *testing.T) {
	snapshot, err := decodeSnapshot(flattenJSON(t, validFlattenTable()))
	if err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	if len(snapshot.Deals) != 1 {
		t.Fatalf("deals 数量 = %d，want 1", len(snapshot.Deals))
	}
	deal := snapshot.Deals[0]
	if deal.ID != "minimax-daily-400" || deal.Provider != "MiniMax" || deal.Kind != "daily" {
		t.Fatalf("deal 字段异常: %+v", deal)
	}
	if deal.Yuan == nil || *deal.Yuan != 6.5 || !deal.Counted {
		t.Fatalf("yuan/counted 异常: %+v", deal)
	}
	if len(deal.Tags) != 1 || deal.Tags[0] != "每日签到" {
		t.Fatalf("tags 异常: %+v", deal.Tags)
	}
	if snapshot.Basis.USDCNY != 6.6713 || !strings.Contains(snapshot.Basis.Ratio, "7") {
		t.Fatalf("basis 异常: %+v", snapshot.Basis)
	}
}

func TestDecodeSnapshotRejectsBadPayloads(t *testing.T) {
	cases := []struct {
		name string
		data string
		want string
	}{
		{"非 JSON", "not-json", "不是合法 JSON"},
		{"非数组", `{"free-tokens":1}`, "不是合法 JSON"},
		{"空数组", `[]`, "空数组"},
		{"缺少 free-tokens", `[{"root":-2}]`, "free-tokens"},
		{"deals 为空", `[{"free-tokens":{"data":{"deals":[]}}}]`, "deals"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeSnapshot([]byte(tc.data))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v，want 包含 %q", err, tc.want)
			}
		})
	}
}

func TestDecodeSnapshotSupportsEncodedKeys(t *testing.T) {
	// 对象键也可以走 "_<keyId>" 编码：A[1]="free-tokens" 作为键名。
	table := []any{
		map[string]any{"_1": 2},
		"free-tokens",
		map[string]any{"data": 3},
		map[string]any{"deals": []any{4}},
		map[string]any{"id": "x", "provider": "Zed", "kind": "once", "title": "t"},
	}
	snapshot, err := decodeSnapshot(flattenJSON(t, table))
	if err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	if len(snapshot.Deals) != 1 || snapshot.Deals[0].Provider != "Zed" {
		t.Fatalf("编码键解码结果异常: %+v", snapshot.Deals)
	}
}

// 线上 serverNow / expiresAt 是毫秒时间戳而不是 ISO 字符串：解码后必须
// 归一化成 RFC3339，否则强类型投影会报字段类型错误。
func TestDecodeSnapshotNormalizesNumericTimestamps(t *testing.T) {
	table := []any{
		map[string]any{"free-tokens": 1},
		map[string]any{"data": 2},
		map[string]any{
			"deals":     []any{3},
			"serverNow": 4,
			"expiresAt": 5,
		},
		map[string]any{"id": "x", "provider": "Zed", "kind": "once", "title": "t", "verifiedAt": 6},
		1762680000000.0, 1762680600000.0, 1762670000000.0,
	}
	snapshot, err := decodeSnapshot(flattenJSON(t, table))
	if err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	if snapshot.ServerNow == "" || snapshot.ServerNow == "1762680000000" {
		t.Fatalf("serverNow 未归一化: %q", snapshot.ServerNow)
	}
	if snapshot.ExpiresAt == "" {
		t.Fatal("expiresAt 未归一化")
	}
	if snapshot.Deals[0].VerifiedAt == "" {
		t.Fatal("verifiedAt 未归一化")
	}
}
