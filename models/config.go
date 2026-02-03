package models

import (
	"encoding/json"
	"os"
)

func ParseConf() (Config, error) {
	var conf Config
	data, err := os.ReadFile("config.json")
	if err != nil {
		return conf, err
	}
	// 解析JSON数据到Config结构体
	err = json.Unmarshal(data, &conf)

	return conf, err
}

// AIFilterConfig AI过滤配置
type AIFilterConfig struct {
	// 是否全局启用AI过滤
	Enabled bool `json:"enabled"`
	// API Key
	APIKey string `json:"apiKey"`
	// API Base URL (兼容 OpenAI 格式的 API)
	APIBase string `json:"apiBase,omitempty"`
	// 模型名称
	Model string `json:"model,omitempty"`
	// 系统提示词
	SystemPrompt string `json:"systemPrompt,omitempty"`
	// 分类阈值 (0-1)，置信度高于此值才过滤
	Threshold float64 `json:"threshold,omitempty"`
	// 最大 token 数
	MaxTokens int `json:"maxTokens,omitempty"`
	// Temperature
	Temperature float64 `json:"temperature,omitempty"`
	// 请求超时时间（秒）
	Timeout int `json:"timeout,omitempty"`
	// 并发数，同时进行的AI过滤请求数量
	Concurrency int `json:"concurrency,omitempty"`
}

// GetAPIBase 获取 API Base URL，默认为火山引擎
func (c AIFilterConfig) GetAPIBase() string {
	if c.APIBase == "" {
		return "https://ark.cn-beijing.volces.com/api/v3"
	}
	return c.APIBase
}

// GetModel 获取模型名称，默认为 doubao-seed-1.8
func (c AIFilterConfig) GetModel() string {
	if c.Model == "" {
		return "doubao-seed-1.8"
	}
	return c.Model
}

// GetSystemPrompt 获取系统提示词
func (c AIFilterConfig) GetSystemPrompt() string {
	if c.SystemPrompt == "" {
		return `你是一个严格的内容分类器。判断给定的RSS文章是否为广告、推广内容、软文或低质量内容。
请返回JSON格式：{"is_filtered": boolean, "confidence": 0-1之间的浮点数, "reason": "过滤原因"}
- is_filtered: true表示应该过滤掉，false表示保留
- confidence: 你判断的置信度
- reason: 简短说明判断理由`
	}
	return c.SystemPrompt
}

// GetThreshold 获取阈值，默认为 0.7
func (c AIFilterConfig) GetThreshold() float64 {
	if c.Threshold == 0 {
		return 0.7
	}
	return c.Threshold
}

// GetMaxTokens 获取最大 token 数，默认为 500
func (c AIFilterConfig) GetMaxTokens() int {
	if c.MaxTokens == 0 {
		return 500
	}
	return c.MaxTokens
}

// GetTemperature 获取 temperature，默认为 0.1
func (c AIFilterConfig) GetTemperature() float64 {
	if c.Temperature == 0 {
		return 0.1
	}
	return c.Temperature
}

// GetTimeout 获取超时时间，默认为 30 秒
func (c AIFilterConfig) GetTimeout() int {
	if c.Timeout == 0 {
		return 30
	}
	return c.Timeout
}

// GetConcurrency 获取并发数，默认为 5
func (c AIFilterConfig) GetConcurrency() int {
	if c.Concurrency <= 0 {
		return 5
	}
	return c.Concurrency
}

// FetchSchedule 抓取计划规则
type FetchSchedule struct {
	StartTime    string `json:"startTime"`    // HH:mm:ss
	EndTime      string `json:"endTime"`      // HH:mm:ss
	BaseRefresh  int    `json:"baseRefresh"`  // 基准频率 (分钟)
	DefaultCount int    `json:"defaultCount"` // 默认次数
}

// FilterStrategy 过滤策略配置
type FilterStrategy struct {
	// 是否启用关键词过滤
	KeywordEnabled *bool `json:"keywordEnabled,omitempty"`
	// 是否启用AI过滤
	AIEnabled *bool `json:"aiEnabled,omitempty"`
	// 自定义提示词（覆盖全局设置）
	CustomPrompt string `json:"customPrompt,omitempty"`
	// 自定义阈值（覆盖全局设置）
	Threshold *float64 `json:"threshold,omitempty"`
	// 过滤关键词（包含这些关键词的文章将被过滤）
	FilterKeywords []string `json:"filterKeywords,omitempty"`
	// 保留关键词（包含这些关键词的文章将被保留，优先级高于过滤）
	KeepKeywords []string `json:"keepKeywords,omitempty"`
}

// IsKeywordEnabled 检查是否启用关键词过滤
func (f FilterStrategy) IsKeywordEnabled() bool {
	if f.KeywordEnabled != nil {
		return *f.KeywordEnabled
	}
	return false
}

// IsAIEnabled 检查是否启用AI过滤
func (f FilterStrategy) IsAIEnabled() bool {
	if f.AIEnabled != nil {
		return *f.AIEnabled
	}
	return false
}

// GetThreshold 获取阈值
func (f FilterStrategy) GetThreshold(globalThreshold float64) float64 {
	if f.Threshold != nil {
		return *f.Threshold
	}
	return globalThreshold
}

// PostProcessConfig 后处理配置
type PostProcessConfig struct {
	// 是否启用后处理
	Enabled bool `json:"enabled"`
	// 处理模式: "ai" 或 "script"
	Mode string `json:"mode,omitempty"`
	// AI模式的提示词
	Prompt string `json:"prompt,omitempty"`
	// 脚本模式的脚本路径（二选一）
	ScriptPath string `json:"scriptPath,omitempty"`
	// 脚本模式的脚本内容（二选一，优先级高于ScriptPath）
	ScriptContent string `json:"scriptContent,omitempty"`
	// 是否修改标题
	ModifyTitle bool `json:"modifyTitle,omitempty"`
	// 是否修改链接
	ModifyLink bool `json:"modifyLink,omitempty"`
	// 是否修改发布时间
	ModifyPubDate bool `json:"modifyPubDate,omitempty"`
}

// GetMode 获取处理模式，默认为ai
func (p PostProcessConfig) GetMode() string {
	if p.Mode == "" {
		return "ai"
	}
	return p.Mode
}

// FeedSource 表示单个RSS源或一个文件夹
type FeedSource struct {
	// 单个RSS源的URL（与Folder互斥）
	URL string `json:"url,omitempty"`
	// 自定义名称（单个源或文件夹都可用）
	Name string `json:"name,omitempty"`
	// 自定义图标URL
	Icon string `json:"icon,omitempty"`
	// 文件夹包含的RSS源配置列表
	Urls []FeedURL `json:"urls,omitempty"`
	// AI过滤策略
	Filter *FilterStrategy `json:"filter,omitempty"`
	// 榜单模式：启用后，新增条目将放在列表前面（适用于无发布时间的榜单类RSS源）
	RankingMode bool `json:"rankingMode,omitempty"`
	// 最大读取条目数，超过此数量的条目将不会被加载（0或不设置表示不限制）
	MaxItems int `json:"maxItems,omitempty"`
	// 缓存条目数，获取到新条目后保留旧条目使总数达到此值（0或不设置表示不缓存）
	CacheItems int `json:"cacheItems,omitempty"`
	// 分组名称，默认为"关注"
	Group string `json:"group,omitempty"`
	// 后处理配置
	PostProcess *PostProcessConfig `json:"postProcess,omitempty"`
	// 自定义刷新次数，与时段规则中的基准频率相乘
	RefreshCount int `json:"refreshCount,omitempty"`
	// 是否在条目后显示发布时间（如"1小时前"）
	ShowPubDate bool `json:"showPubDate,omitempty"`
}

// FeedURL 表示文件夹内的单个RSS源
type FeedURL struct {
	URL  string `json:"url"`
	Name string `json:"name,omitempty"` // 自定义来源名称
	Icon string `json:"icon,omitempty"` // 自定义图标URL
	// AI过滤策略
	Filter *FilterStrategy `json:"filter,omitempty"`
	// 榜单模式：启用后，新增条目将放在列表前面（适用于无发布时间的榜单类RSS源）
	RankingMode bool `json:"rankingMode,omitempty"`
	// 最大读取条目数，超过此数量的条目将不会被加载（0或不设置表示不限制）
	MaxItems int `json:"maxItems,omitempty"`
	// 缓存条目数，获取到新条目后保留旧条目使总数达到此值（0或不设置表示不缓存）
	CacheItems int `json:"cacheItems,omitempty"`
	// 后处理配置
	PostProcess *PostProcessConfig `json:"postProcess,omitempty"`
	// 自定义刷新次数，与时段规则中的基准频率相乘
	RefreshCount int `json:"refreshCount,omitempty"`
	// 是否在条目后显示发布时间（如"1小时前"）
	ShowPubDate bool `json:"showPubDate,omitempty"`
}

// IsFolder 判断是否为文件夹类型
func (f FeedSource) IsFolder() bool {
	return len(f.Urls) > 0
}

type Config struct {
	Sources        []FeedSource   `json:"sources,omitempty"`
	// 抓取计划规则列表
	Schedules []FetchSchedule `json:"schedules,omitempty"`
	// 夜间模式起始时间
	NightStartTime string `json:"nightStartTime,omitempty"`
	// 夜间模式结束时间
	NightEndTime string `json:"nightEndTime,omitempty"`
	// 是否启用夜间模式 (手动覆盖)
	DarkMode bool `json:"darkMode,omitempty"`
	// Settings password
	Password string `json:"password,omitempty"`
	// Session duration in hours (default: 24)
	SessionDuration int `json:"sessionDuration,omitempty"`
	// AI过滤配置
	AIFilter AIFilterConfig `json:"aiFilter,omitempty"`
	// 分组顺序（可选，如果不设置则按订阅源出现顺序）
	GroupOrder []string `json:"groupOrder,omitempty"`
	// 默认选中的分组（可选，默认为第一个分组）
	DefaultGroup string `json:"defaultGroup,omitempty"`
}

// GetAllUrls 获取所有RSS源URL（包括文件夹内的）
func (c Config) GetAllUrls() []string {
	urls := make([]string, 0)
	// sources配置
	for _, source := range c.Sources {
		if source.IsFolder() {
			for _, feedUrl := range source.Urls {
				urls = append(urls, feedUrl.URL)
			}
		} else if source.URL != "" {
			urls = append(urls, source.URL)
		}
	}
	return urls
}

func (older Config) GetIncrement(newer Config) []string {
	var (
		urlMap    = make(map[string]struct{})
		increment = make([]string, 0)
	)
	for _, item := range older.GetAllUrls() {
		urlMap[item] = struct{}{}
	}

	for _, item := range newer.GetAllUrls() {
		if _, ok := urlMap[item]; ok {
			continue
		}
		increment = append(increment, item)
	}

	return increment
}

// GetSessionDuration 获取会话有效期（小时），默认为 24
func (c Config) GetSessionDuration() int {
	if c.SessionDuration <= 0 {
		return 24
	}
	return c.SessionDuration
}
