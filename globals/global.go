package globals

import (
	"embed"
	"fmt"
	"rss-reader/models"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mmcdole/gofeed"
)

var (
	DbMap    map[string]models.Feed
	RssUrls  models.Config
	Upgrader = websocket.Upgrader{}
	Lock     sync.RWMutex

	//go:embed static
	DirStatic embed.FS

	HtmlContent []byte

	Fp = gofeed.NewParser()

	// 过滤结果缓存: map[文章Link] -> 过滤决策
	FilterCache     map[string]models.FilterCacheEntry
	FilterCacheLock sync.RWMutex

	// 已读状态: map[文章Link] -> 已读时间戳
	ReadState     map[string]int64
	ReadStateLock sync.RWMutex

	// 下次更新时间
	NextUpdateTime time.Time
)

// Init 首次初始化，创建所有缓存
func Init() {
	conf, err := models.ParseConf()
	if err != nil {
		panic(err)
	}
	RssUrls = conf
	// 读取 index.html 内容
	HtmlContent, err = DirStatic.ReadFile("static/index.html")
	if err != nil {
		panic(err)
	}

	DbMap = make(map[string]models.Feed)
	FilterCache = make(map[string]models.FilterCacheEntry)
	ReadState = make(map[string]int64)
}

// ReloadConfig 重新加载配置，保留现有缓存
func ReloadConfig() error {
	oldConfig := RssUrls
	
	conf, err := models.ParseConf()
	if err != nil {
		return fmt.Errorf("解析配置文件失败: %w", err)
	}
	RssUrls = conf
	// 读取 index.html 内容
	HtmlContent, err = DirStatic.ReadFile("static/index.html")
	if err != nil {
		return fmt.Errorf("读取HTML文件失败: %w", err)
	}
	
	// 智能清理缓存
	cleanupCaches(oldConfig, conf)
	return nil
}

// cleanupCaches 清理不再需要的缓存
func cleanupCaches(oldConfig, newConfig models.Config) {
	// 获取新配置中所有的URL
	newUrls := make(map[string]bool)
	for _, url := range newConfig.GetAllUrls() {
		newUrls[url] = true
	}
	
	// 获取需要启用AI过滤的URL（用于判断是否清理FilterCache）
	filterEnabledUrls := make(map[string]bool)
	if newConfig.AIFilter.Enabled && newConfig.AIFilter.APIKey != "" {
		for _, source := range newConfig.Sources {
			if source.IsFolder() {
				for _, feedUrl := range source.Urls {
					if shouldFilterURL(feedUrl.Filter, source.Filter) {
						filterEnabledUrls[feedUrl.URL] = true
					}
				}
			} else if source.URL != "" {
				if shouldFilterURL(source.Filter, nil) {
					filterEnabledUrls[source.URL] = true
				}
			}
		}
	}
	
	// 清理DbMap中不存在的源
	Lock.Lock()
	for url := range DbMap {
		if !newUrls[url] {
			delete(DbMap, url)
		}
	}
	Lock.Unlock()
	
	// 如果AI过滤全局关闭，清空所有FilterCache
	if !newConfig.AIFilter.Enabled || newConfig.AIFilter.APIKey == "" {
		FilterCacheLock.Lock()
		FilterCache = make(map[string]models.FilterCacheEntry)
		FilterCacheLock.Unlock()
		return
	}
	
	// 清理FilterCache中属于已删除源或AI过滤已关闭源的文章
	// 需要先收集当前启用AI过滤的源的所有文章链接
	Lock.RLock()
	validArticleLinks := make(map[string]bool)
	for url, feed := range DbMap {
		if filterEnabledUrls[url] {
			for _, item := range feed.Items {
				validArticleLinks[item.Link] = true
			}
		}
	}
	Lock.RUnlock()
	
	// 只保留仍然需要的缓存条目
	FilterCacheLock.Lock()
	for link := range FilterCache {
		if !validArticleLinks[link] {
			delete(FilterCache, link)
		}
	}
	FilterCacheLock.Unlock()
}

// shouldFilterURL 判断URL是否应该启用AI过滤
func shouldFilterURL(urlFilter, folderFilter *models.FilterStrategy) bool {
	// 优先使用URL自己的过滤策略
	if urlFilter != nil {
		return urlFilter.IsEnabled(false)
	}
	// 其次使用文件夹的过滤策略
	if folderFilter != nil {
		return folderFilter.IsEnabled(false)
	}
	return false
}
