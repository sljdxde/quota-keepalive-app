package providers

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// accountLabelMaxRunes 是账号备注的长度上限：手机号、昵称、邮箱都放得下，
// 又不会把账号卡标题撑成两行。
const accountLabelMaxRunes = 40

// errAccountLabel 在备注请求缺少供应商或账号时返回；两者都是内部 UI 参数，
// 正常调用不会缺。
var errAccountLabel = errors.New("providers: 账号备注缺少供应商或账号")

// SetAccountLabel 保存一个账号的自定义备注（界面上的账号名）。
//
// 备注只影响 QKA 里的展示：它不写回插件、不参与
// provider_account_identities 的身份哈希，所以改备注不会触发账号历史重置。
// label 为空表示清除备注，界面回落到上游账号标识。
func (s *Service) SetAccountLabel(ctx context.Context, provider, account, label string) error {
	provider = strings.TrimSpace(provider)
	account = strings.TrimSpace(account)
	if provider == "" || account == "" {
		return errAccountLabel
	}
	label = normalizeAccountLabel(label)
	now := s.now().UTC().Format(time.RFC3339)
	var err error
	if label == "" {
		_, err = s.db.ExecContext(ctx,
			`DELETE FROM account_labels WHERE provider = ? AND account_id = ?`,
			provider, account)
	} else {
		_, err = s.db.ExecContext(ctx, `
INSERT INTO account_labels (provider, account_id, label, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(provider, account_id) DO UPDATE SET
  label = excluded.label, updated_at = excluded.updated_at`,
			provider, account, label, now)
	}
	if err != nil {
		s.writeAuditFailure(ctx, provider, account, "account", err)
		return err
	}
	s.invalidateView()
	message := "账号备注已清除"
	if label != "" {
		message = "账号备注已更新"
	}
	s.writeAuditLog(ctx, provider, account, "account", "completed", "info", message)
	return nil
}

// AccountLabel 返回一个账号当前保存的备注；没有备注时是空字符串。
func (s *Service) AccountLabel(ctx context.Context, provider, account string) (string, error) {
	var label string
	err := s.db.QueryRowContext(ctx, `
SELECT label FROM account_labels WHERE provider = ? AND account_id = ?`,
		provider, account).Scan(&label)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return label, err
}

// accountLabels 一次取回一个供应商下所有账号的备注，避免逐账号查询。
func (s *Service) accountLabels(ctx context.Context, provider string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT account_id, label FROM account_labels WHERE provider = ?`, provider)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	labels := map[string]string{}
	for rows.Next() {
		var accountID, label string
		if err := rows.Scan(&accountID, &label); err != nil {
			return nil, err
		}
		if accountID != "" && label != "" {
			labels[accountID] = label
		}
	}
	return labels, rows.Err()
}

// deleteAccountLabel 在账号被删除或插槽换人后清掉备注，避免旧名字留给下一个
// 复用同一插槽的账号。
func (s *Service) deleteAccountLabel(ctx context.Context, provider, account string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM account_labels WHERE provider = ? AND account_id = ?`,
		provider, account)
	return err
}

// normalizeAccountLabel 去掉不可见字符与换行，按 rune 截到上限。
// 账号名会出现在标题、日志与无障碍标签里，所以不允许换行/制表符。
func normalizeAccountLabel(label string) string {
	label = strings.ToValidUTF8(label, "�")
	// Fields 顺带把换行、制表符与连续空格收敛成单个空格。
	label = strings.Join(strings.Fields(label), " ")
	runes := []rune(label)
	if len(runes) > accountLabelMaxRunes {
		runes = runes[:accountLabelMaxRunes]
	}
	return strings.TrimSpace(string(runes))
}
