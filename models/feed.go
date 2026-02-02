package models

type Feed struct {
	Title    string            `json:"title,omitempty"`
	Link     string            `json:"link"`
	Icon     string            `json:"icon,omitempty"`    // RSS源的图标URL
	Custom   map[string]string `json:"custom,omitempty"`
	Items    []Item            `json:"items,omitempty"`
	IsFolder bool              `json:"isFolder,omitempty"` // 是否为文件夹类型
	// AI过滤统计
	FilteredCount int      `json:"filteredCount,omitempty"` // 被过滤的文章数量
	AllItemLinks  []string `json:"-"`                      // 过滤前的所有文章链接（不输出到JSON，用于内容变动检测和内部清理）
	AllItemTitles []string `json:"-"`                      // 过滤前的所有文章标题（不输出到JSON，用于内容变动检测）
	Group         string   `json:"group,omitempty"`        // 分组名称
}

type Item struct {
	Title        string `json:"title"`
	Link         string `json:"link"`
	OriginalLink string `json:"originalLink,omitempty"` // 原始链接（后处理前），用于缓存查询
	Description  string `json:"description"`
	Source       string `json:"source,omitempty"`  // 来源（用于文件夹内区分不同源）
	PubDate      string `json:"pubDate,omitempty"` // 发布时间
}

// FilterCacheEntry 过滤结果缓存条目（只保留上一次的过滤结果）
type FilterCacheEntry struct {
	IsFiltered bool // 是否被过滤
}

// PostProcessCacheEntry 后处理结果缓存条目
type PostProcessCacheEntry struct {
	// 处理后的标题
	Title string `json:"title,omitempty"`
	// 处理后的链接
	Link string `json:"link,omitempty"`
	// 处理后的发布时间
	PubDate string `json:"pubDate,omitempty"`
	// 处理时间戳
	ProcessedAt string `json:"processedAt"`
}
