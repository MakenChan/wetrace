// Package transcribe 提供语音转写结果的本地持久化缓存。
//
// 设计要点：
//   - 独立于微信原始 DB，存放在 <workDir>/wetrace.db
//   - 表：voice_transcribe_cache(msg_id PK, text, engine, created_at)
//   - msg_id 使用与 Contents["voice"] 同源的字符串（V4: server_id 的十进制字符串）
//   - engine 字段区分来源，目前只会写 "local"；"wechat" 由 packed_info_data 原生提供，不入此表
package transcribe

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Cache 本地语音转写缓存。线程安全。
type Cache struct {
	db *sql.DB
	mu sync.Mutex // 序列化写操作，避免 SQLite busy
}

// NewCache 打开/创建缓存库。dbDir 是 wetrace 的工作目录。
func NewCache(dbDir string) (*Cache, error) {
	dbPath := filepath.Join(dbDir, "wetrace.db")
	// _busy_timeout 给 5 秒，_journal_mode=WAL 读写并发更好
	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_journal_mode=WAL", dbPath)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open wetrace.db: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping wetrace.db: %w", err)
	}
	// 单连接写串行化，避免 WAL 写冲突；读走 WAL 并发无阻
	db.SetMaxOpenConns(4)

	schema := `
CREATE TABLE IF NOT EXISTS voice_transcribe_cache (
	msg_id      TEXT PRIMARY KEY,
	text        TEXT NOT NULL,
	engine      TEXT NOT NULL DEFAULT 'local',
	created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
`
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}

	return &Cache{db: db}, nil
}

// Close 关闭缓存库。
func (c *Cache) Close() error {
	if c == nil || c.db == nil {
		return nil
	}
	return c.db.Close()
}

// Entry 缓存条目。
type Entry struct {
	Text   string
	Engine string
}

// Get 查询单条缓存。
func (c *Cache) Get(msgID string) (Entry, bool, error) {
	if c == nil || c.db == nil || msgID == "" {
		return Entry{}, false, nil
	}
	var e Entry
	err := c.db.QueryRow(
		`SELECT text, engine FROM voice_transcribe_cache WHERE msg_id = ?`, msgID,
	).Scan(&e.Text, &e.Engine)
	if err == sql.ErrNoRows {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, fmt.Errorf("query cache: %w", err)
	}
	return e, true, nil
}

// GetMany 批量查询，未命中的 id 不出现在结果里。
func (c *Cache) GetMany(ids []string) (map[string]Entry, error) {
	result := make(map[string]Entry, len(ids))
	if c == nil || c.db == nil || len(ids) == 0 {
		return result, nil
	}

	// 去重
	seen := make(map[string]struct{}, len(ids))
	uniq := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniq = append(uniq, id)
	}
	if len(uniq) == 0 {
		return result, nil
	}

	// 分批 IN 查询，单批上限 500 避免 SQL 参数超限
	const batchSize = 500
	for start := 0; start < len(uniq); start += batchSize {
		end := start + batchSize
		if end > len(uniq) {
			end = len(uniq)
		}
		batch := uniq[start:end]

		placeholders := make([]byte, 0, len(batch)*2)
		args := make([]interface{}, 0, len(batch))
		for i, id := range batch {
			if i > 0 {
				placeholders = append(placeholders, ',')
			}
			placeholders = append(placeholders, '?')
			args = append(args, id)
		}
		q := fmt.Sprintf(
			`SELECT msg_id, text, engine FROM voice_transcribe_cache WHERE msg_id IN (%s)`,
			string(placeholders),
		)
		rows, err := c.db.Query(q, args...)
		if err != nil {
			return nil, fmt.Errorf("batch query cache: %w", err)
		}
		for rows.Next() {
			var id string
			var e Entry
			if err := rows.Scan(&id, &e.Text, &e.Engine); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan cache row: %w", err)
			}
			result[id] = e
		}
		rows.Close()
	}
	return result, nil
}

// Put 写入/覆盖一条缓存。engine 留空则默认 "local"。
func (c *Cache) Put(msgID, text, engine string) error {
	if c == nil || c.db == nil {
		return nil
	}
	if msgID == "" {
		return fmt.Errorf("empty msg_id")
	}
	if engine == "" {
		engine = "local"
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.db.Exec(
		`INSERT INTO voice_transcribe_cache (msg_id, text, engine, created_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(msg_id) DO UPDATE SET
		   text = excluded.text,
		   engine = excluded.engine,
		   created_at = excluded.created_at`,
		msgID, text, engine, time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("put cache: %w", err)
	}
	return nil
}

// Count 统计条数（调试用）。
func (c *Cache) Count() (int, error) {
	if c == nil || c.db == nil {
		return 0, nil
	}
	var n int
	err := c.db.QueryRow(`SELECT COUNT(1) FROM voice_transcribe_cache`).Scan(&n)
	return n, err
}
