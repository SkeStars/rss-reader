package utils

import (
	"log"
	"net/url"
	"rss-reader/globals"
	"rss-reader/models"
	"sort"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/mmcdole/gofeed"
	"sync"
	"fmt"
)

func UpdateFeeds() {
	if globals.RssUrls.ReFresh <= 0 {
		return
	}
	refreshDuration := time.Duration(globals.RssUrls.ReFresh) * time.Minute
	
    // Set initial next update time
	globals.Lock.Lock()
    globals.NextUpdateTime = time.Now().Add(refreshDuration)
	globals.Lock.Unlock()

	var (
		tick = time.Tick(refreshDuration)
	)
	for {
		formattedTime := time.Now().Format("2006-01-02 15:04:05")
		for _, source := range globals.RssUrls.Sources {
			if source.IsFolder() {
				for _, feedUrl := range source.Urls {
					go UpdateFeed(feedUrl.URL, formattedTime)
				}
			} else if source.URL != "" {
				go UpdateFeed(source.URL, formattedTime)
			}
		}
		<-tick
		
		globals.Lock.Lock()
		globals.NextUpdateTime = time.Now().Add(refreshDuration)
		globals.Lock.Unlock()
	}
}

// GetFaviconURL 根据 RSS URL 获取对应的 favicon URL
func GetFaviconURL(rssURL string) string {
	parsedURL, err := url.Parse(rssURL)
	if err != nil {
		return ""
	}
	// 使用 Google 的 favicon 服务
	if parsedURL.Host != "" {
		return "https://www.google.com/s2/favicons?domain=" + parsedURL.Host + "&sz=64"
	}
	return ""
}

// IsRankingMode 检查指定URL是否启用了榜单模式
func IsRankingMode(rssURL string) bool {
	for _, source := range globals.RssUrls.Sources {
		if source.URL == rssURL {
			return source.RankingMode
		}
		// 检查文件夹内的 URLs
		if source.IsFolder() {
			for _, feedUrl := range source.Urls {
				if feedUrl.URL == rssURL {
					return feedUrl.RankingMode
				}
			}
		}
	}
	return false
}

// GetMaxItems 获取指定URL的最大读取条目数限制，返回0表示不限制
func GetMaxItems(rssURL string) int {
	for _, source := range globals.RssUrls.Sources {
		if source.URL == rssURL {
			return source.MaxItems
		}
		// 检查文件夹内的 URLs
		if source.IsFolder() {
			for _, feedUrl := range source.Urls {
				if feedUrl.URL == rssURL {
					return feedUrl.MaxItems
				}
			}
		}
	}
	return 0
}

// GetCustomIconURL 从配置中获取自定义图标，如果没有则自动获取 favicon
func GetCustomIconURL(rssURL string, customIcon string) string {
	if customIcon != "" {
		return customIcon
	}
	return GetFaviconURL(rssURL)
}

func UpdateFeed(url, formattedTime string) error {
	result, err := globals.Fp.ParseURL(url)
	if err != nil {
		log.Printf("Error fetching feed: %v | %v", url, err)
		return err
	}
	
	// 检查是否为榜单模式
	isRanking := IsRankingMode(url)
	
	//feed内容无更新时无需更新缓存（非榜单模式下的快速判断）
	if !isRanking {
		globals.Lock.RLock()
		cache, ok := globals.DbMap[url]
		globals.Lock.RUnlock()

		if ok &&
			len(result.Items) > 0 &&
			len(cache.Items) > 0 &&
			result.Items[0].Link == cache.Items[0].Link {
			return nil
		}
	}
	
	// 获取图标：优先级 1.配置的自定义图标 2.RSS feed的image 3.自动生成favicon
	icon := GetIconForFeed(url, result)
	
	// 先构建所有Items
	allItems := make([]models.Item, 0, len(result.Items))
	for _, v := range result.Items {
		pubDate := ""
		// 榜单模式下不使用RSS源自带的发布时间，留空让processRankingItems处理
		if !isRanking {
			if v.PublishedParsed != nil {
				pubDate = v.PublishedParsed.Format("2006-01-02 15:04:05")
			} else if v.UpdatedParsed != nil {
				pubDate = v.UpdatedParsed.Format("2006-01-02 15:04:05")
			} else {
				// 如果RSS条目没有时间戳，使用当前抓取时间作为备用
				// 这样可以确保所有条目都有时间戳，避免排序问题
				pubDate = formattedTime
			}
		}
		// 榜单模式下 pubDate 为空，将由 processRankingItems 统一分配时间戳
		allItems = append(allItems, models.Item{
			Link:        v.Link,
			Title:       v.Title,
			Description: v.Description,
			Source:      result.Title,
			PubDate:     pubDate,
		})
	}

	// 应用最大条目数限制
	maxItems := GetMaxItems(url)
	if maxItems > 0 && len(allItems) > maxItems {
		allItems = allItems[:maxItems]
	}

	// 应用AI过滤
	originalCount := len(allItems)
	filteredItems := allItems
	passedLinks := make(map[string]bool)
	
	if ShouldFilter(url) {
		filteredItems = FilterItems(allItems, url)
		for _, item := range filteredItems {
			passedLinks[item.Link] = true
		}
	} else {
		// 如果不启用过滤，所有条目都视为通过
		for _, item := range allItems {
			passedLinks[item.Link] = true
		}
	}

	// 榜单模式下的处理：将新增条目放在前面
	if isRanking {
		allItems = processRankingItems(url, allItems, passedLinks)
		
		// 重新构建过滤后的列表，以反映 processRankingItems 可能带来的排序变化
		if len(passedLinks) < len(allItems) {
			newFilteredItems := make([]models.Item, 0, len(filteredItems))
			for _, item := range allItems {
				if passedLinks[item.Link] {
					newFilteredItems = append(newFilteredItems, item)
				}
			}
			filteredItems = newFilteredItems
		} else {
			filteredItems = allItems
		}
	}

	// 应用后处理
	if ShouldPostProcess(url) {
		filteredItems = PostProcessItems(filteredItems, url)
	}

	// 应用条目缓存逻辑：将旧条目与新条目合并
	cacheItems := GetCacheItems(url)
	if cacheItems > 0 {
		filteredItems = mergeWithCachedItems(url, filteredItems, cacheItems)
	}

	// 记录过滤前的所有文章链接，用于清理时判断
	allItemLinks := make([]string, 0, len(allItems))
	for _, item := range allItems {
		allItemLinks = append(allItemLinks, item.Link)
	}

	// 即时清理该源已不存在的文章缓存（AI过滤缓存、后处理缓存、榜单时间戳等）
	// 这确保了在两次全量清理之间，单个源的更新也能保持缓存精简
	// 注意：先在主线程中获取旧缓存数据的快照，避免在 goroutine 中读取时 DbMap 已被更新
	var oldLinks []string
	var oldItemLinks []string
	globals.Lock.RLock()
	if cache, ok := globals.DbMap[url]; ok {
		oldLinks = make([]string, len(cache.AllItemLinks))
		copy(oldLinks, cache.AllItemLinks)
		if len(oldLinks) == 0 {
			for _, item := range cache.Items {
				oldLinks = append(oldLinks, item.Link)
			}
		}
		// 获取旧的展示条目链接
		for _, item := range cache.Items {
			oldItemLinks = append(oldItemLinks, item.Link)
		}
	}
	globals.Lock.RUnlock()
	
	// 同时获取 ItemsCache 中的条目链接
	if cachedItems, ok := GetItemsCache(url); ok {
		for _, item := range cachedItems {
			oldItemLinks = append(oldItemLinks, item.Link)
		}
	}
	
	go func(u string, newLinks []string, oldLinks []string, oldItemLinks []string, newFilteredItems []models.Item) {
		// 构建当前源的所有有效链接（包括过滤后的和过滤前的备选）
		currentLinks := make(map[string]bool)
		for _, l := range newLinks {
			currentLinks[l] = true
		}
		// 包含新的过滤后条目链接
		for _, item := range newFilteredItems {
			currentLinks[item.Link] = true
		}
		// 包含之前已经在展示中的条目（特别是对于有缓存条目功能的情况）
		for _, l := range oldItemLinks {
			currentLinks[l] = true
		}
		
		// 清理 AI 过滤缓存（基于旧的 AllItemLinks）
		if len(oldLinks) > 0 {
			cleanupRankingFilterCache(oldLinks, currentLinks)
		}
		
		// 清理后处理缓存
		if len(oldLinks) > 0 && ShouldPostProcess(u) {
			cleanupPostProcessCacheForSource(oldLinks, currentLinks)
		}
		
		// 清理榜单时间戳
		if len(oldLinks) > 0 {
			if !IsRankingMode(u) {
				// 如果不再是榜单模式，清理该源在持久化中的所有时间戳
				CleanupRankingTimestamps(oldLinks, make(map[string]bool))
			} else {
				// 如果仍是榜单模式，清理已不存在的条目
				CleanupRankingTimestamps(oldLinks, currentLinks)
			}
		}
	}(url, allItemLinks, oldLinks, oldItemLinks, filteredItems)

	customFeed := models.Feed{
		Title:         result.Title,
		Link:          url,
		Icon:          icon,
		Custom:        map[string]string{"lastupdate": formattedTime},
		Items:         filteredItems,
		FilteredCount: originalCount - len(filteredItems),
		AllItemLinks:  allItemLinks,
	}

	globals.Lock.Lock()
	defer globals.Lock.Unlock()
	globals.DbMap[url] = customFeed
	return nil
}

// processRankingItems 处理榜单模式的条目：将新增条目放在前面，并清理过时的缓存
func processRankingItems(url string, newItems []models.Item, passedLinks map[string]bool) []models.Item {
	globals.Lock.RLock()
	cache, hasCached := globals.DbMap[url]
	globals.Lock.RUnlock()
	
	// 构建新条目的链接集合，用于清理过期缓存
	newLinks := make(map[string]bool)
	for _, item := range newItems {
		newLinks[item.Link] = true
	}
	
	// 如果没有内存缓存，尝试从持久化存储恢复时间戳
	if !hasCached || len(cache.Items) == 0 {
		// 从持久化存储恢复时间戳
		for i, item := range newItems {
			if ts, ok := GetRankingTimestamp(item.Link); ok {
				newItems[i].PubDate = ts
			} else if passedLinks[item.Link] {
				// 新条目且通过了过滤，设置当前时间并持久化
				nowTs := time.Now().Format("2006-01-02 15:04:05")
				newItems[i].PubDate = nowTs
				SetRankingTimestamp(item.Link, nowTs)
			} else {
				// 未通过过滤的新条目，仅在内存中给个时间戳用于可能的排序，不持久化
				newItems[i].PubDate = time.Now().Format("2006-01-02 15:04:05")
			}
		}
		return newItems
	}
	
	// 构建旧条目的链接集合，用于快速查找
	oldLinks := make(map[string]bool)
	for _, item := range cache.Items {
		oldLinks[item.Link] = true
	}
	
	// 分离新增条目和已存在条目
	addedItems := make([]models.Item, 0)
	existingItems := make([]models.Item, 0)
	
	// 为新增条目设置更新时间作为时间戳
	rankingNewTimestamp := time.Now().Format("2006-01-02 15:04:05")
	
	for i, item := range newItems {
		if !oldLinks[item.Link] {
			// 这是新增的条目
			// 检查持久化存储中是否有时间戳（服务重启后恢复）
			if ts, ok := GetRankingTimestamp(item.Link); ok {
				item.PubDate = ts
			} else if passedLinks[item.Link] {
				// 真正的新条目且通过过滤，设置当前时间并持久化
				item.PubDate = rankingNewTimestamp
				SetRankingTimestamp(item.Link, rankingNewTimestamp)
			} else {
				// 未通过过滤的新条目，不持久化
				item.PubDate = rankingNewTimestamp
			}
			newItems[i] = item
			addedItems = append(addedItems, item)
		} else {
			// 这是已存在的条目，需要从缓存或持久化存储中恢复时间戳
			// 优先从内存缓存中获取
			foundInCache := false
			for _, cachedItem := range cache.Items {
				if cachedItem.Link == item.Link && cachedItem.PubDate != "" {
					item.PubDate = cachedItem.PubDate
					foundInCache = true
					break
				}
			}
			// 如果内存缓存中没有，从持久化存储中获取
			if !foundInCache {
				if ts, ok := GetRankingTimestamp(item.Link); ok {
					item.PubDate = ts
				}
			}
			
			// 确保有时间戳
			if item.PubDate == "" {
				item.PubDate = rankingNewTimestamp
				// 只有通过过滤才持久化
				if passedLinks[item.Link] {
					SetRankingTimestamp(item.Link, rankingNewTimestamp)
				}
			} else {
				// 处理模式切换或过滤状态变化：
				// 如果已通过过滤但没在持久化中，补上
				// 如果没通过过滤但在持久化中，CleanupRankingTimestamps 会处理删除
				if passedLinks[item.Link] {
					if _, ok := GetRankingTimestamp(item.Link); !ok {
						SetRankingTimestamp(item.Link, item.PubDate)
					}
				}
			}
			
			newItems[i] = item
			existingItems = append(existingItems, item)
		}
	}
	
	// 清理不再存在的条目在FilterCache和RankingTimestamps中的记录
	// 优先使用 AllItemLinks 以确保包含被AI过滤的记录
	oldLinksList := cache.AllItemLinks
	if len(oldLinksList) == 0 {
		// 兼容旧数据
		for _, item := range cache.Items {
			oldLinksList = append(oldLinksList, item.Link)
		}
	}
	
	// FilterCache 清理基于 RSS 源全量链接，防止重复 AI 过滤
	cleanupRankingFilterCache(oldLinksList, newLinks)
	
	// RankingTimestamps 清理基于“通过过滤的链接”，确保被过滤掉的条目不被记录
	CleanupRankingTimestamps(oldLinksList, passedLinks)
	
	// 将新增条目放在前面
	result := make([]models.Item, 0, len(newItems))
	result = append(result, addedItems...)
	result = append(result, existingItems...)
	
	return result
}

// cleanupRankingFilterCache 清理榜单源中已不存在的条目的过滤缓存
func cleanupRankingFilterCache(oldLinks []string, newLinks map[string]bool) {
	globals.FilterCacheLock.Lock()
	defer globals.FilterCacheLock.Unlock()
	
	for _, link := range oldLinks {
		if !newLinks[link] {
			// 该条目不再存在于新列表中，清理其过滤缓存
			delete(globals.FilterCache, link)
		}
	}
}

// cleanupPostProcessCacheForSource 清理指定源中已不存在条目的后处理缓存
func cleanupPostProcessCacheForSource(oldLinks []string, newLinks map[string]bool) {
	PostProcessCacheLock.Lock()
	defer PostProcessCacheLock.Unlock()
	
	for _, link := range oldLinks {
		if !newLinks[link] {
			delete(PostProcessCache, link)
		}
	}
}

// mergeWithCachedItems 将新条目与缓存的旧条目合并，保持总数达到 cacheItems
func mergeWithCachedItems(url string, newItems []models.Item, cacheItems int) []models.Item {
	// 构建新条目的链接集合，用于去重
	newLinks := make(map[string]bool)
	for _, item := range newItems {
		newLinks[item.Link] = true
	}
	
	// 从缓存中获取旧条目
	cachedItems, hasCached := GetItemsCache(url)
	
	// 合并条目：新条目 + 不在新条目中的旧条目
	mergedItems := make([]models.Item, 0, cacheItems)
	mergedItems = append(mergedItems, newItems...)
	
	if hasCached {
		for _, item := range cachedItems {
			// 只添加不在新条目中的旧条目
			if !newLinks[item.Link] {
				mergedItems = append(mergedItems, item)
			}
			// 达到缓存数量限制后停止
			if len(mergedItems) >= cacheItems {
				break
			}
		}
	}
	
	// 限制总数不超过 cacheItems
	if len(mergedItems) > cacheItems {
		mergedItems = mergedItems[:cacheItems]
	}
	
	// 清除 description 字段后保存到缓存（节省存储空间）
	cachedItemsToSave := make([]models.Item, len(mergedItems))
	for i, item := range mergedItems {
		cachedItemsToSave[i] = models.Item{
			Title:        item.Title,
			Link:         item.Link,
			OriginalLink: item.OriginalLink, // 保留原始链接用于后处理缓存查询
			Source:       item.Source,
			PubDate:      item.PubDate,
			// Description 字段不保存到缓存
		}
	}
	SetItemsCache(url, cachedItemsToSave)
	
	return mergedItems
}

// GetIconForURL 从配置中获取 URL 对应的自定义图标，如果没有则自动生成 favicon
func GetIconForURL(rssURL string) string {
	// 检查 sources 配置中是否有自定义图标
	for _, source := range globals.RssUrls.Sources {
		if source.URL == rssURL && source.Icon != "" {
			return source.Icon
		}
		// 检查文件夹内的 URLs
		if source.IsFolder() {
			for _, feedURL := range source.Urls {
				if feedURL.URL == rssURL && feedURL.Icon != "" {
					return feedURL.Icon
				}
			}
		}
	}
	// 没有自定义图标，使用自动获取的 favicon
	return GetFaviconURL(rssURL)
}

// GetIconForFeed 获取feed的图标，优先级：1.配置的自定义图标 2.RSS的image字段 3.自动生成favicon
func GetIconForFeed(rssURL string, feed interface{}) string {
	// 1. 先检查配置中是否有自定义图标
	for _, source := range globals.RssUrls.Sources {
		if source.URL == rssURL && source.Icon != "" {
			return source.Icon
		}
		// 检查文件夹内的 URLs
		if source.IsFolder() {
			for _, feedURL := range source.Urls {
				if feedURL.URL == rssURL && feedURL.Icon != "" {
					return feedURL.Icon
				}
			}
		}
	}
	
	// 2. 尝试从RSS feed的image字段获取
	if feedResult, ok := feed.(*gofeed.Feed); ok {
		if feedResult.Image != nil && feedResult.Image.URL != "" {
			return feedResult.Image.URL
		}
	}
	
	// 3. 最后使用自动获取的 favicon
	return GetFaviconURL(rssURL)
}

// GetFeeds 获取feeds列表，支持文件夹
func GetFeeds() []models.Feed {
	feeds := make([]models.Feed, 0)

	for _, source := range globals.RssUrls.Sources {
		// 获取分组名称，默认为"关注"
		group := source.Group
		if group == "" {
			group = "关注"
		}

		if source.IsFolder() {
			// 文件夹类型：聚合多个源
			folderFeed := buildFolderFeed(source)
			if folderFeed != nil {
				folderFeed.Group = group
				feeds = append(feeds, *folderFeed)
			}
		} else if source.URL != "" {
			// 单个源
			globals.Lock.RLock()
			cache, ok := globals.DbMap[source.URL]
			globals.Lock.RUnlock()
			if !ok {
				log.Printf("Error getting feed from db is null %v", source.URL)
				// 返回空的Feed对象，展示卡片但内容为空
				title := "加载失败"
				if source.Name != "" {
					title = source.Name
				}
				feeds = append(feeds, models.Feed{
					Title:  title,
					Link:   source.URL,
					Icon:   source.Icon,
					Custom: map[string]string{"lastupdate": "加载失败，请稍后重试"},
					Items:  []models.Item{},
					Group:  group,
				})
				continue
			}
			// 支持自定义名称
			if source.Name != "" {
				cache.Title = source.Name
			}
			// 支持自定义图标
			if source.Icon != "" {
				cache.Icon = source.Icon
			}
			cache.Group = group
			feeds = append(feeds, cache)
		}
	}

	return feeds
}

// buildFolderFeed 构建文件夹Feed，聚合多个源的内容
func buildFolderFeed(source models.FeedSource) *models.Feed {
	if !source.IsFolder() {
		return nil
	}

	folderFeed := &models.Feed{
		Title:    source.Name,
		Link:     "folder:" + source.Name,
		Icon:     source.Icon, // 文件夹的自定义图标
		IsFolder: true,
		Custom:   map[string]string{"lastupdate": "加载失败，请稍后重试"},
		Items:    make([]models.Item, 0),
	}

	// 收集所有源的items
	for _, feedUrl := range source.Urls {
		globals.Lock.RLock()
		cache, ok := globals.DbMap[feedUrl.URL]
		globals.Lock.RUnlock()
		if !ok {
			log.Printf("Error getting feed from db is null %v", feedUrl.URL)
			// 为文件夹添加一个提示项，表明某个源加载失败
			sourceName := "未知源"
			if feedUrl.Name != "" {
				sourceName = feedUrl.Name
			}
			folderFeed.Items = append(folderFeed.Items, models.Item{
				Title:       "⚠️ " + sourceName + " 加载失败",
				Link:        feedUrl.URL,
				Description: "该订阅源暂时无法加载，请稍后重试",
				Source:      sourceName,
				PubDate:     "",
			})
			continue
		}

		// 更新文件夹的最后更新时间为最新的源更新时间
		// 只有当前文件夹时间是错误消息，或者新时间是有效时间戳且更新时才更新
		currentTime := folderFeed.Custom["lastupdate"]
		cacheTime := cache.Custom["lastupdate"]
		
		// 如果当前是错误消息，且缓存有有效时间，则使用缓存时间
		if currentTime == "加载失败，请稍后重试" && cacheTime != "加载失败，请稍后重试" {
			folderFeed.Custom["lastupdate"] = cacheTime
		} else if currentTime != "加载失败，请稍后重试" && cacheTime != "加载失败，请稍后重试" && cacheTime > currentTime {
			// 两者都是有效时间戳时，使用较新的
			folderFeed.Custom["lastupdate"] = cacheTime
		}

		// 确定来源名称：只有设置了自定义名称时才添加Source标识
		sourceName := ""
		if feedUrl.Name != "" {
			sourceName = feedUrl.Name
		}

		// 添加items，带上来源信息
		for _, item := range cache.Items {
			newItem := item
			newItem.Source = sourceName
			folderFeed.Items = append(folderFeed.Items, newItem)
		}
	}

	// 先按发布时间倒序排列，确保较新的条目在前
	// 对于没有时间戳的条目，将其排到后面
	sort.SliceStable(folderFeed.Items, func(i, j int) bool {
		pubDateI := folderFeed.Items[i].PubDate
		pubDateJ := folderFeed.Items[j].PubDate
		
		// 如果两者都为空，保持原顺序
		if pubDateI == "" && pubDateJ == "" {
			return false
		}
		// 如果i为空，j不为空，i排后面
		if pubDateI == "" {
			return false
		}
		// 如果j为空，i不为空，i排前面
		if pubDateJ == "" {
			return true
		}
		// 两者都不为空，按时间倒序排列
		return pubDateI > pubDateJ
	})

	// 根据标题去重，保留最新的条目
	// 使用规范化的标题作为键，避免空格等细微差异导致的重复
	seenTitles := make(map[string]bool)
	uniqueItems := make([]models.Item, 0, len(folderFeed.Items))
	for _, item := range folderFeed.Items {
		// 规范化标题：去除首尾空格
		normalizedTitle := strings.TrimSpace(item.Title)
		if normalizedTitle == "" {
			continue
		}
		
		if !seenTitles[normalizedTitle] {
			seenTitles[normalizedTitle] = true
			uniqueItems = append(uniqueItems, item)
		}
	}
	folderFeed.Items = uniqueItems

	return folderFeed
}

func WatchConfigFileChanges(filePath string) {
	// 创建一个新的监控器
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	defer watcher.Close()

	// 添加要监控的文件
	err = watcher.Add(filePath)
	if err != nil {
		log.Printf("添加监控失败: %v", err)
	}

	// 启动一个 goroutine 来处理文件变化事件
	go func() {
		var debounceTimer *time.Timer
		const debounceInterval = 500 * time.Millisecond

		reloadFunc := func() {
			log.Println("文件已修改，重新加载配置")

			// 等待文件完全写入，然后重试读取配置
			var oldConfig models.Config
			var err error
			for i := 0; i < 3; i++ {
				if i > 0 {
					time.Sleep(100 * time.Millisecond)
				}
				oldConfig, err = globals.ReloadConfig()
				if err == nil {
					break
				}
				log.Printf("重载配置失败（尝试 %d/3）: %v", i+1, err)
			}

			if err != nil {
				log.Printf("配置重载最终失败，保持使用旧配置: %v", err)
				return
			}

			log.Println("配置重载成功")
			
			// 1. 立即清理 DbMap 中已删除的源
			// 这由 globals.ReloadConfig -> cleanupCaches 处理
			
			// 2. 立即清理后处理缓存
			CleanupPostProcessCacheOnConfigChange()
			
			// 3. 立即清理条目缓存（清理不再启用缓存的源）
			CleanupItemsCacheOnConfigChange()

			// 4. 立即清理榜单数据（清理不再处于榜单模式的源）
			CleanupRankingTimestampsOnConfigChange()

			// 5. 立即清理已读状态（清理已删除源的数据）
			CleanupReadStateOnConfigChange()
			
			// 收集受影响的源（配置发生变化的源）
			affectedUrls := collectAffectedUrls(oldConfig, globals.RssUrls)
			
			if len(affectedUrls) == 0 {
				log.Println("配置更新：无源受影响，跳过更新")
				return
			}
			
			log.Printf("配置更新：%d 个源受影响，开始更新", len(affectedUrls))
			formattedTime := time.Now().Format("2006-01-02 15:04:05")

			for url := range affectedUrls {
				go UpdateFeed(url, formattedTime)
			}
		}

		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}

				// 忽略无用事件
				if event.Op&fsnotify.Chmod == fsnotify.Chmod {
					continue
				}

				if event.Op&fsnotify.Write == fsnotify.Write ||
					event.Op&fsnotify.Create == fsnotify.Create ||
					event.Op&fsnotify.Rename == fsnotify.Rename {

					// 如果是重命名或创建，尝试重新添加监控（针对某些原子写操作）
					if event.Op&fsnotify.Rename == fsnotify.Rename || event.Op&fsnotify.Remove == fsnotify.Remove {
						// 稍微延迟以确保新文件存在
						go func() {
							time.Sleep(100 * time.Millisecond)
							watcher.Add(filePath)
						}()
					}

					// 防抖动
					if debounceTimer != nil {
						debounceTimer.Stop()
					}
					debounceTimer = time.AfterFunc(debounceInterval, reloadFunc)
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Println("错误:", err)
			}
		}
	}()

	select {}
}

// RefreshSingleFeed 刷新单个源或文件夹内的所有源
func RefreshSingleFeed(link string) error {
	formattedTime := time.Now().Format("2006-01-02 15:04:05")
	log.Printf("手动刷新源: %s", link)
	
	// 查找匹配的源
	for _, source := range globals.RssUrls.Sources {
		if source.IsFolder() {
			// 如果是文件夹，检查link是否匹配文件夹的name或任一子源
			folderMatch := false
			for _, feedUrl := range source.Urls {
				if feedUrl.URL == link {
					folderMatch = true
					break
				}
			}
			
			// 如果匹配到文件夹中的任一源，刷新整个文件夹
			// 处理 link 格式，文件夹的 link 可能是 "folder:" + Name
			isFolderLink := link == "folder:"+source.Name
			
			if folderMatch || source.Name == link || isFolderLink {
				log.Printf("刷新文件夹: %s", source.Name)
				var wg sync.WaitGroup
				errChan := make(chan error, len(source.Urls))
				
				for _, feedUrl := range source.Urls {
					wg.Add(1)
					go func(url string) {
						defer wg.Done()
						if err := UpdateFeed(url, formattedTime); err != nil {
							log.Printf("文件夹[%s]中源[%s]刷新失败: %v", source.Name, url, err)
							errChan <- err
						}
					}(feedUrl.URL)
				}
				wg.Wait()
				close(errChan)

				errorCount := len(errChan)
				if errorCount > 0 {
					log.Printf("文件夹[%s]刷新完成，共有 %d 个源失败", source.Name, errorCount)
					// 如果全部失败才返回错误，部分失败认为刷新过程已完成
					if errorCount == len(source.Urls) {
						return fmt.Errorf("所有源刷新失败")
					}
				}
				return nil
			}
		} else if source.URL == link {
			// 单个源直接刷新
			log.Printf("刷新单个源: %s", link)
			return UpdateFeed(source.URL, formattedTime)
		}
	}
	
	log.Printf("未找到匹配的源: %s", link)
	return fmt.Errorf("feed not found")
}

// ClearFeedCacheForPostProcessSources 清除启用了后处理的源的Feed缓存
// 这样在配置变更后，即使文章内容未变，也会重新获取和处理
func ClearFeedCacheForPostProcessSources() {
	globals.Lock.Lock()
	defer globals.Lock.Unlock()
	
	cleared := 0
	for rssURL := range globals.DbMap {
		// 检查该源是否启用了后处理
		if ShouldPostProcess(rssURL) {
			delete(globals.DbMap, rssURL)
			cleared++
		}
	}
	
	if cleared > 0 {
		log.Printf("已清除 %d 个启用后处理的源的Feed缓存", cleared)
	}
}

// collectAffectedUrls 比较新旧配置，收集受影响的源URL
func collectAffectedUrls(oldConfig, newConfig models.Config) map[string]bool {
	affectedUrls := make(map[string]bool)
	
	// 创建新旧配置的源映射
	oldSources := make(map[string]*models.FeedSource)
	oldFeedUrls := make(map[string]*models.FeedURL)
	
	for i := range oldConfig.Sources {
		source := &oldConfig.Sources[i]
		if source.IsFolder() {
			for j := range source.Urls {
				feedUrl := &source.Urls[j]
				oldFeedUrls[feedUrl.URL] = feedUrl
			}
		} else if source.URL != "" {
			oldSources[source.URL] = source
		}
	}
	
	// 检查新配置中的每个源
	for i := range newConfig.Sources {
		source := &newConfig.Sources[i]
		if source.IsFolder() {
			for j := range source.Urls {
				feedUrl := &source.Urls[j]
				// 检查是否是新增的源或配置发生了变化
				if oldFeedUrl, exists := oldFeedUrls[feedUrl.URL]; !exists || feedUrlChanged(oldFeedUrl, feedUrl) {
					affectedUrls[feedUrl.URL] = true
				}
			}
		} else if source.URL != "" {
			// 检查是否是新增的源或配置发生了变化
			if oldSource, exists := oldSources[source.URL]; !exists || sourceChanged(oldSource, source) {
				affectedUrls[source.URL] = true
			}
		}
	}
	
	return affectedUrls
}

// sourceChanged 检查源配置是否发生了变化
func sourceChanged(old, new *models.FeedSource) bool {
	// 检查影响数据获取或处理的关键配置
	if old.MaxItems != new.MaxItems ||
		old.CacheItems != new.CacheItems ||
		old.RankingMode != new.RankingMode {
		return true
	}
	
	// 检查过滤配置是否变化
	if filterChanged(old.Filter, new.Filter) {
		return true
	}
	
	// 检查后处理配置是否变化
	if postProcessChanged(old.PostProcess, new.PostProcess) {
		return true
	}
	
	return false
}

// feedUrlChanged 检查文件夹内的源配置是否发生了变化
func feedUrlChanged(old, new *models.FeedURL) bool {
	// 检查影响数据获取或处理的关键配置
	if old.MaxItems != new.MaxItems ||
		old.CacheItems != new.CacheItems ||
		old.RankingMode != new.RankingMode {
		return true
	}
	
	// 检查过滤配置是否变化
	if filterChanged(old.Filter, new.Filter) {
		return true
	}
	
	// 检查后处理配置是否变化
	if postProcessChanged(old.PostProcess, new.PostProcess) {
		return true
	}
	
	return false
}

// filterChanged 检查过滤配置是否变化
func filterChanged(old, new *models.FilterStrategy) bool {
	if (old == nil) != (new == nil) {
		return true
	}
	if old == nil {
		return false
	}
	
	// 比较关键字段
	if (old.Enabled == nil) != (new.Enabled == nil) {
		return true
	}
	if old.Enabled != nil && new.Enabled != nil && *old.Enabled != *new.Enabled {
		return true
	}
	
	if (old.Threshold == nil) != (new.Threshold == nil) {
		return true
	}
	if old.Threshold != nil && new.Threshold != nil && *old.Threshold != *new.Threshold {
		return true
	}
	
	if old.CustomPrompt != new.CustomPrompt {
		return true
	}
	
	// 检查关键词列表
	if len(old.FilterKeywords) != len(new.FilterKeywords) || len(old.KeepKeywords) != len(new.KeepKeywords) {
		return true
	}
	
	return false
}

// postProcessChanged 检查后处理配置是否变化
func postProcessChanged(old, new *models.PostProcessConfig) bool {
	if (old == nil) != (new == nil) {
		return true
	}
	if old == nil {
		return false
	}
	
	// 比较关键字段
	if old.Enabled != new.Enabled ||
		old.Mode != new.Mode ||
		old.Prompt != new.Prompt ||
		old.ScriptPath != new.ScriptPath ||
		old.ScriptContent != new.ScriptContent ||
		old.ModifyTitle != new.ModifyTitle ||
		old.ModifyLink != new.ModifyLink ||
		old.ModifyPubDate != new.ModifyPubDate {
		return true
	}
	
	return false
}
