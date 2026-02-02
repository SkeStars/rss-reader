package globals

import (
	"embed"
	"fmt"
	"rss-reader/models"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mmcdole/gofeed"
	"html/template"
	"encoding/json"
	"net/http"
)

var (
	DbMap    map[string]models.Feed
	RssUrls  models.Config
	Upgrader = websocket.Upgrader{}
	Lock     sync.RWMutex

	//go:embed static
	DirStatic embed.FS

	HtmlContent []byte

	Fp = func() *gofeed.Parser {
		fp := gofeed.NewParser()
		fp.Client = &http.Client{
			Transport: &userAgentTransport{
				base: http.DefaultTransport,
			},
			Timeout: 30 * time.Second,
		}
		return fp
	}()

	// 过滤结果缓存: map[文章Link] -> 过滤决策
	FilterCache     map[string]models.FilterCacheEntry
	FilterCacheLock sync.RWMutex

	// 已读状态: map[文章Link] -> 已读时间戳
	ReadState     map[string]int64
	ReadStateLock sync.RWMutex

	// 条目缓存: map[RSS URL] -> []Item（用于保留旧条目）
	ItemsCache     map[string][]models.Item
	ItemsCacheLock sync.RWMutex

	// 下次更新时间
	NextUpdateTime time.Time

	// 缓存的页面模板
	Tpl *template.Template
)

type userAgentTransport struct {
	base http.RoundTripper
}

func (t *userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	}
	// 增加一些常见的头信息
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml, text/xml, */*")
	}
	return t.base.RoundTrip(req)
}

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
	ItemsCache = make(map[string][]models.Item)

	// 初始化模板
	InitTemplate()
}

// InitTemplate 初始化模板缓存
func InitTemplate() {
	funcMap := template.FuncMap{
		"inc": func(i int) int {
			return i + 1
		},
		"json": func(v interface{}) template.JS {
			a, _ := json.Marshal(v)
			return template.JS(a)
		},
	}
	tmpl, err := template.New("index.html").Delims("<<", ">>").Funcs(funcMap).ParseFS(DirStatic, "static/index.html")
	if err != nil {
		fmt.Printf("初始化模板失败: %v\n", err)
		return
	}
	Tpl = tmpl
}

// ReloadConfig 重新加载配置，保留现有缓存，返回旧配置用于比较差异
func ReloadConfig() (models.Config, error) {
	oldConfig := RssUrls
	
	conf, err := models.ParseConf()
	if err != nil {
		return oldConfig, fmt.Errorf("解析配置文件失败: %w", err)
	}
	RssUrls = conf
	// 读取 index.html 内容
	HtmlContent, err = DirStatic.ReadFile("static/index.html")
	if err != nil {
		return oldConfig, fmt.Errorf("读取HTML文件失败: %w", err)
	}
	
	// 智能清理缓存
	cleanupCaches(oldConfig, conf)

	// 重新加载模板（以防 index.html 变化）
	InitTemplate()

	return oldConfig, nil
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
	// 需要先收集当前启用AI过滤的源的所有文章链接（包括过滤前的）
	Lock.RLock()
	validArticleLinks := make(map[string]bool)
	for url, feed := range DbMap {
		if filterEnabledUrls[url] {
			// 收集过滤前的所有文章
			for _, link := range feed.AllItemLinks {
				validArticleLinks[link] = true
			}
			// 收集当前展示的所有文章（包含缓存合并的）
			for _, item := range feed.Items {
				validArticleLinks[item.Link] = true
			}
		}
	}
	dbMapEmpty := len(DbMap) == 0
	Lock.RUnlock()
	
	// 如果 DbMap 为空，不执行清理（可能是启动初期或网络问题）
	if dbMapEmpty {
		return
	}
	
	// 只保留仍然需要的缓存条目
	FilterCacheLock.Lock()
	for link := range FilterCache {
		if !validArticleLinks[link] {
			delete(FilterCache, link)
		}
	}
	FilterCacheLock.Unlock()
}

// shouldFilterURL 判断URL是否应该启用过滤（关键词或AI）
func shouldFilterURL(urlFilter, folderFilter *models.FilterStrategy) bool {
	// 优先使用URL自己的过滤策略
	if urlFilter != nil {
		return urlFilter.IsKeywordEnabled() || urlFilter.IsAIEnabled()
	}
	// 其次使用文件夹的过滤策略
	if folderFilter != nil {
		return folderFilter.IsKeywordEnabled() || folderFilter.IsAIEnabled()
	}
	return false
}
