package store

import (
	"context"
	"fmt"

	"github.com/afumu/wetrace/internal/model"
	"github.com/afumu/wetrace/internal/transcribe"
	"github.com/afumu/wetrace/store/bind"
	"github.com/afumu/wetrace/store/core"
	"github.com/afumu/wetrace/store/repo"
	"github.com/afumu/wetrace/store/strategy"
	"github.com/afumu/wetrace/store/types"
	"github.com/fsnotify/fsnotify"
)

// DefaultStore 是 Store 接口的默认实现
type DefaultStore struct {
	pool       *core.ConnectionPool
	router     *bind.TimelineRouter
	watcher    *core.Watcher
	repo       *repo.Repository
	voiceCache *transcribe.Cache // 可选，nil 表示未启用本地转写缓存
}

// SetVoiceCache 注入语音转写缓存。nil 表示关闭。
// 注入后，GetMessages 返回的语音消息会把本地缓存的 voiceText 注入到 Contents。
func (s *DefaultStore) SetVoiceCache(c *transcribe.Cache) {
	s.voiceCache = c
}

// VoiceCache 暴露缓存句柄供上层读写（例如 TranscribeVoice handler 写入结果）。
func (s *DefaultStore) VoiceCache() *transcribe.Cache {
	return s.voiceCache
}

// NewStore 初始化一个新的存储实例
func NewStore(baseDir string) (*DefaultStore, error) {
	// 1. 初始化核心组件
	pool := core.NewConnectionPool(baseDir)
	watcher, err := core.NewWatcher(baseDir)
	if err != nil {
		pool.CloseAll()
		return nil, err
	}

	// 2. 策略层
	strat := strategy.NewV4()
	router := bind.NewTimelineRouter(baseDir, pool, strat)

	// 3. 构建索引 (这一步可能比较耗时，但必须在启动时完成)
	if err := router.RebuildIndex(context.Background()); err != nil {
		pool.CloseAll()
		watcher.Stop()
		return nil, fmt.Errorf("构建时间线索引失败: %w", err)
	}

	// 4. 初始化仓储
	r := repo.New(router, pool)

	// 5. 启动文件监听
	watcher.Start()

	// 注册自动刷新逻辑：当有新文件生成时，重建索引
	watcher.AddCallback(func(event fsnotify.Event) {
		if event.Op&fsnotify.Create == fsnotify.Create {
			// 只有当新文件被策略识别为消息数据库时，才重建索引
			if meta, ok := strat.Identify(event.Name); ok && meta.Type == strategy.Message {
				_ = router.RebuildIndex(context.Background())
			}
		}
	})

	return &DefaultStore{
		pool:    pool,
		router:  router,
		watcher: watcher,
		repo:    r,
	}, nil
}

func (s *DefaultStore) Close() error {
	s.watcher.Stop()
	if s.voiceCache != nil {
		_ = s.voiceCache.Close()
	}
	return s.pool.CloseAll()
}

// --- 下面是 Store 接口的代理实现 ---

func (s *DefaultStore) GetMessages(ctx context.Context, query types.MessageQuery) ([]*model.Message, error) {
	msgs, err := s.repo.GetMessages(ctx, query)
	if err != nil {
		return nil, err
	}
	s.injectVoiceCache(msgs)
	return msgs, nil
}

// injectVoiceCache 把本地转写缓存的文本挂到语音消息的 Contents 上。
// 规则：
//   - 微信原生（Contents["voiceText"] 已存在）→ 标记 voiceTextSource="wechat"，不覆盖；
//   - 本地缓存命中 → 填 voiceText + voiceTextSource="local"。
func (s *DefaultStore) injectVoiceCache(msgs []*model.Message) {
	if s.voiceCache == nil || len(msgs) == 0 {
		return
	}
	// 收集需要查询的 voice id
	ids := make([]string, 0)
	for _, m := range msgs {
		if m == nil || m.Type != model.MessageTypeVoice || m.Contents == nil {
			continue
		}
		// 微信原生已有文本：打标记，跳过本地查询
		if vt, _ := m.Contents["voiceText"].(string); vt != "" {
			m.Contents["voiceTextSource"] = "wechat"
			continue
		}
		if vid, ok := m.Contents["voice"].(string); ok && vid != "" {
			ids = append(ids, vid)
		}
	}
	if len(ids) == 0 {
		return
	}
	cached, err := s.voiceCache.GetMany(ids)
	if err != nil || len(cached) == 0 {
		return
	}
	for _, m := range msgs {
		if m == nil || m.Type != model.MessageTypeVoice || m.Contents == nil {
			continue
		}
		if vt, _ := m.Contents["voiceText"].(string); vt != "" {
			continue
		}
		vid, _ := m.Contents["voice"].(string)
		if vid == "" {
			continue
		}
		if e, ok := cached[vid]; ok && e.Text != "" {
			m.Contents["voiceText"] = e.Text
			if e.Engine != "" {
				m.Contents["voiceTextSource"] = e.Engine
			} else {
				m.Contents["voiceTextSource"] = "local"
			}
		}
	}
}

func (s *DefaultStore) SearchGlobalMessages(ctx context.Context, query types.MessageQuery) ([]*model.Message, error) {
	return s.repo.SearchGlobalMessages(ctx, query)
}

func (s *DefaultStore) GetContacts(ctx context.Context, query types.ContactQuery) ([]*model.Contact, error) {
	return s.repo.GetContacts(ctx, query)
}

func (s *DefaultStore) GetChatRooms(ctx context.Context, query types.ChatRoomQuery) ([]*model.ChatRoom, error) {
	return s.repo.GetChatRooms(ctx, query)
}

func (s *DefaultStore) GetSessions(ctx context.Context, query types.SessionQuery) ([]*model.Session, error) {
	return s.repo.GetSessions(ctx, query)
}

func (s *DefaultStore) DeleteSession(ctx context.Context, username string) error {
	return s.repo.DeleteSession(ctx, username)
}

func (s *DefaultStore) GetMedia(ctx context.Context, mediaType string, key string) (*model.Media, error) {
	return s.repo.GetMedia(ctx, mediaType, key)
}

func (s *DefaultStore) GetHourlyActivity(ctx context.Context, sessionID string) ([]*model.HourlyStat, error) {
	return s.repo.GetHourlyActivity(ctx, sessionID)
}

func (s *DefaultStore) GetDailyActivity(ctx context.Context, sessionID string) ([]*model.DailyStat, error) {
	return s.repo.GetDailyActivity(ctx, sessionID)
}

func (s *DefaultStore) GetWeekdayActivity(ctx context.Context, sessionID string) ([]*model.WeekdayStat, error) {
	return s.repo.GetWeekdayActivity(ctx, sessionID)
}

func (s *DefaultStore) GetMonthlyActivity(ctx context.Context, sessionID string) ([]*model.MonthlyStat, error) {
	return s.repo.GetMonthlyActivity(ctx, sessionID)
}

func (s *DefaultStore) GetMessageTypeDistribution(ctx context.Context, sessionID string) ([]*model.MessageTypeStat, error) {
	return s.repo.GetMessageTypeDistribution(ctx, sessionID)
}

func (s *DefaultStore) GetMemberActivity(ctx context.Context, sessionID string) ([]*model.MemberActivity, error) {
	return s.repo.GetMemberActivity(ctx, sessionID)
}

func (s *DefaultStore) GetRepeatAnalysis(ctx context.Context, sessionID string) ([]*model.RepeatStat, error) {
	return s.repo.GetRepeatAnalysis(ctx, sessionID)
}

func (s *DefaultStore) GetPersonalTopContacts(ctx context.Context, limit int) ([]*model.PersonalTopContact, error) {
	return s.repo.GetPersonalTopContacts(ctx, limit)
}

func (s *DefaultStore) GetDashboardData(ctx context.Context) (*model.DashboardData, error) {
	return s.repo.GetDashboardData(ctx)
}

func (s *DefaultStore) SearchMessages(ctx context.Context, query types.MessageQuery) (*model.SearchResult, error) {
	return s.repo.SearchMessages(ctx, query)
}

func (s *DefaultStore) GetMessageContext(ctx context.Context, talker string, seq int64, before, after int) ([]*model.Message, error) {
	return s.repo.GetMessageContext(ctx, talker, seq, before, after)
}

func (s *DefaultStore) GetAnnualReport(ctx context.Context, year int) (*model.AnnualReport, error) {
	return s.repo.GetAnnualReport(ctx, year)
}

func (s *DefaultStore) Watch(group string, callback func(event fsnotify.Event) error) error {
	s.watcher.AddCallback(func(event fsnotify.Event) {
		_ = callback(event)
	})
	return nil
}

func (s *DefaultStore) GetNeedContactList(ctx context.Context, days int) ([]*model.NeedContactItem, error) {
	return s.repo.GetNeedContactList(ctx, days)
}

// Reload 重新加载存储（重建索引、刷新连接等）
func (s *DefaultStore) Reload() error {
	// 1. 关闭所有现有连接（这将强制下次查询时重新打开连接）
	if err := s.pool.CloseAll(); err != nil {
		return fmt.Errorf("reload: close all connections failed: %w", err)
	}

	// 2. 重新构建时间线索引（扫描目录，重新发现文件）
	if err := s.router.RebuildIndex(context.Background()); err != nil {
		return fmt.Errorf("reload: rebuild index failed: %w", err)
	}

	return nil
}
