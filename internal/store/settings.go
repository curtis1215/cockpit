package store

import (
	"database/sql"
	"errors"
	"log"
)

// GetSetting 讀 key-value 設定；不存在回空字串（呼叫端以空值代表未設定）。
// 真正的 DB 錯誤（非 no rows）會記 log——對翻譯端點而言，靜默回空會讓
// 翻譯悄悄 fallback 回 translate_cmd，必須留下線索。
func (s *Store) GetSetting(key string) string {
	var v string
	if err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("store: GetSetting(%s): %v", key, err)
		}
		return ""
	}
	return v
}

// SetSetting 寫入（upsert）單一設定值。
func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// SetSettings 在單一 transaction 內 upsert 多個設定值——要嘛全部生效要嘛全部不動，
// 避免中途失敗留下半套設定（例如 endpoint 已寫、model 沒寫）。
func (s *Store) SetSettings(kv map[string]string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for k, v := range kv {
		if _, err := tx.Exec(
			`INSERT INTO settings (key, value) VALUES (?, ?)
			 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}
