package freetokens

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// DecodeTurboStream 解码 AIHOT free-tokens.data 的 turbo-stream v2「flatten」
// 格式：顶层是 JSON 数组，下标即引用 id；对象写成 {"_<keyId>": <valueId>}
// （key 与 value 都是数组下标）；负数内联常量一律当 null；真正的对象键
// 也可以直接写成普通字符串。
//
// 返回 root 映射（保留原始键），由调用方取 free-tokens 节点；单独导出便于
// 单测覆盖解码细节。不引入任何第三方解码器（turbo-stream v3 只实现 v3，
// 会静默返回扁平数组，是已知的坑）。
func DecodeTurboStream(text []byte) (map[string]any, error) {
	var table []any
	decoder := json.NewDecoder(bytes.NewReader(text))
	decoder.UseNumber()
	if err := decoder.Decode(&table); err != nil {
		return nil, fmt.Errorf("free-tokens.data 不是合法 JSON: %w", err)
	}
	if len(table) == 0 {
		return nil, errors.New("free-tokens.data 结构异常（空数组）")
	}
	d := &flattenDecoder{table: table, memo: make(map[int]any, len(table))}
	root, err := d.hydrateRef(0)
	if err != nil {
		return nil, err
	}
	mapping, ok := root.(map[string]any)
	if !ok {
		return nil, errors.New("free-tokens.data 结构异常（根节点不是对象）")
	}
	return mapping, nil
}

// decodeSnapshot 解码并投影成 Snapshot 类型；字段缺失视为数据异常。
func decodeSnapshot(text []byte) (Snapshot, error) {
	root, err := DecodeTurboStream(text)
	if err != nil {
		return Snapshot{}, err
	}
	data, err := freeTokensData(root)
	if err != nil {
		return Snapshot{}, err
	}
	// 线上 serverNow / expiresAt 是毫秒时间戳（不是 ISO 字符串），统一归一化
	// 成 RFC3339，避免字段类型随上游编码方式漂移。
	normalizeSnapshotTimestamps(data)
	// 先回到 JSON 再解析成强类型：deals 里字段多且可选，强转会让解码器与
	// 数据模型各写一套；这里只需一次序列化，代价可忽略。
	raw, err := json.Marshal(data)
	if err != nil {
		return Snapshot{}, fmt.Errorf("free-tokens 数据无法序列化: %w", err)
	}
	var snapshot Snapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("free-tokens 数据字段异常: %w", err)
	}
	if len(snapshot.Deals) == 0 {
		return Snapshot{}, errors.New("free-tokens 数据缺失（deals 为空）")
	}
	return snapshot, nil
}

// normalizeSnapshotTimestamps 把时间字段里的毫秒时间戳归一化成 RFC3339。
// 上游有时用 turbo-stream 的 Date 编码（数字），有时直接给 ISO 字符串。
func normalizeSnapshotTimestamps(data map[string]any) {
	normalizeTimestampField(data, "serverNow")
	normalizeTimestampField(data, "expiresAt")
	for _, key := range []string{"deals", "ended"} {
		items, _ := data[key].([]any)
		for _, item := range items {
			if record, ok := item.(map[string]any); ok {
				normalizeTimestampField(record, "verifiedAt")
				normalizeTimestampField(record, "endsAt")
			}
		}
	}
}

func normalizeTimestampField(record map[string]any, key string) {
	value, ok := record[key]
	if !ok {
		return
	}
	if number, ok := value.(json.Number); ok {
		if millis, err := number.Int64(); err == nil && millis > 0 {
			record[key] = time.UnixMilli(millis).UTC().Format(time.RFC3339)
		}
	}
}

// freeTokensData 从根节点取出 free-tokens.data：数据里 free-tokens 与 root
// 是两个同级 key，deals 在 root["free-tokens"].data.deals。
func freeTokensData(root map[string]any) (map[string]any, error) {
	ft, ok := root["free-tokens"].(map[string]any)
	if !ok {
		if nested, ok := root["root"].(map[string]any); ok {
			ft, ok = nested["free-tokens"].(map[string]any)
		}
	}
	if !ok {
		return nil, errors.New("free-tokens 数据缺失（free-tokens 节点未找到）")
	}
	data, ok := ft["data"].(map[string]any)
	if !ok {
		return nil, errors.New("free-tokens 数据缺失（data 节点未找到）")
	}
	if _, ok := data["deals"].([]any); !ok {
		return nil, errors.New("free-tokens 数据缺失（deals 未找到）")
	}
	return data, nil
}

type flattenDecoder struct {
	table []any
	memo  map[int]any
}

// hydrateRef 解引用一个 id：负数内联常量（-1..-5）统一按 null 处理——AIHOT
// 数据里只出现 null，不做 undefined/true/false 的区分。
func (d *flattenDecoder) hydrateRef(index int) (any, error) {
	if index < 0 {
		return nil, nil
	}
	if index >= len(d.table) {
		return nil, fmt.Errorf("free-tokens.data 引用越界: %d", index)
	}
	if value, ok := d.memo[index]; ok {
		return value, nil
	}
	raw := d.table[index]
	switch value := raw.(type) {
	case []any:
		// 先占位再填充，兼容自引用/共享引用，避免无限递归。
		out := make([]any, len(value))
		d.memo[index] = out
		for i := range value {
			item, err := d.hydrateValue(value[i])
			if err != nil {
				return nil, err
			}
			out[i] = item
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(value))
		d.memo[index] = out
		for key, item := range value {
			hydrated, err := d.hydrateValue(item)
			if err != nil {
				return nil, err
			}
			if len(key) > 0 && key[0] == '_' {
				keyIndex, convErr := parseKeyIndex(key[1:])
				if convErr != nil {
					return nil, convErr
				}
				if keyIndex < 0 || keyIndex >= len(d.table) {
					return nil, fmt.Errorf("free-tokens.data 键引用越界: %d", keyIndex)
				}
				decoded, ok := d.table[keyIndex].(string)
				if !ok {
					return nil, fmt.Errorf("free-tokens.data 键引用不是字符串: %d", keyIndex)
				}
				out[decoded] = hydrated
				continue
			}
			out[key] = hydrated
		}
		return out, nil
	default:
		return raw, nil
	}
}

// hydrateValue 处理非表格值：数字是引用；内联对象/数组递归解码（flatten
// 数据一般全是引用，但手工构造或未来版本可能内联）；其它原样。
func (d *flattenDecoder) hydrateValue(value any) (any, error) {
	switch typed := value.(type) {
	case json.Number:
		index, err := typed.Int64()
		if err != nil {
			return nil, fmt.Errorf("free-tokens.data 引用不是整数: %q", typed.String())
		}
		return d.hydrateRef(int(index))
	case []any:
		out := make([]any, len(typed))
		for i := range typed {
			item, err := d.hydrateValue(typed[i])
			if err != nil {
				return nil, err
			}
			out[i] = item
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			hydrated, err := d.hydrateValue(item)
			if err != nil {
				return nil, err
			}
			if len(key) > 0 && key[0] == '_' {
				keyIndex, convErr := parseKeyIndex(key[1:])
				if convErr != nil {
					return nil, convErr
				}
				if keyIndex < 0 || keyIndex >= len(d.table) {
					return nil, fmt.Errorf("free-tokens.data 键引用越界: %d", keyIndex)
				}
				decoded, ok := d.table[keyIndex].(string)
				if !ok {
					return nil, fmt.Errorf("free-tokens.data 键引用不是字符串: %d", keyIndex)
				}
				out[decoded] = hydrated
				continue
			}
			out[key] = hydrated
		}
		return out, nil
	default:
		return value, nil
	}
}

func parseKeyIndex(text string) (int, error) {
	var index int
	if _, err := fmt.Sscanf(text, "%d", &index); err != nil {
		return 0, fmt.Errorf("free-tokens.data 键引用非法: %q", text)
	}
	return index, nil
}
